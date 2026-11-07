// Package cli implements the agentmem command line interface.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"agentmem/internal/compute"
	"agentmem/internal/config"
	"agentmem/internal/eval"
	"agentmem/internal/handlers"
	"agentmem/internal/okf"
	"agentmem/internal/router"
	"agentmem/internal/service"
)

const usage = `agentmem - OKF+Git knowledge hub with pluggable RAG and smart routing

Commands:
  serve       Start the HTTP API
  put         Write a knowledge concept
  query       Route and execute a query
  migrate     Reconcile canonical bodies with RAG projections
  decisions   Show the routing audit trail
  concepts    List canonical concept ids
  compute     Execute an Attested Computation (repeat --param name=value)
  computations  Show the Attested Computation verdict audit
  validate    Validate configuration
  eval        Run ranking/routing evaluation on a JSONL dataset
`

// Run dispatches one CLI invocation. It is separated from main for testing.
func Run(args []string, out io.Writer) error {
	if len(args) == 0 {
		_, _ = io.WriteString(out, usage)
		return nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = io.WriteString(out, usage)
		return nil
	case "serve":
		return cmdServe(args[1:], out)
	case "put":
		return cmdPut(args[1:], out)
	case "query":
		return cmdQuery(args[1:], out)
	case "migrate":
		return cmdMigrate(args[1:], out)
	case "decisions":
		return cmdDecisions(args[1:], out)
	case "concepts":
		return cmdConcepts(args[1:], out)
	case "compute":
		return cmdCompute(args[1:], out)
	case "computations":
		return cmdComputations(args[1:], out)
	case "validate":
		return cmdValidate(args[1:], out)
	case "eval":
		return cmdEval(args[1:], out)
	default:
		return fmt.Errorf("agentmem: unknown command %q\n%s", args[0], usage)
	}
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func mustLoad(envPath string) (*config.Config, error) {
	return config.Load(envPath)
}

func mustAssemble(envPath string) (*service.KnowledgeService, error) {
	cfg, err := mustLoad(envPath)
	if err != nil {
		return nil, err
	}
	return service.Assemble(context.Background(), cfg)
}

func printJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdServe(args []string, out io.Writer) error {
	fs := newFlags("serve")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	addr := fs.String("addr", "", "override listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := mustLoad(*envPath)
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.Server.Addr = *addr
	}
	svc, err := service.Assemble(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer svc.Close()
	fmt.Fprintf(out, "agentmem listening on %s (postgres=%s)\n", cfg.Server.Addr, postgresMode(cfg))
	return http.ListenAndServe(cfg.Server.Addr, handlers.NewHandler(svc))
}

func postgresMode(cfg *config.Config) string {
	if cfg.Postgres.DSN == "" {
		return "offline fake"
	}
	return "configured"
}

func cmdPut(args []string, out io.Writer) error {
	fs := newFlags("put")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	id := fs.String("id", "", "concept id")
	subject := fs.String("subject", "", "requesting subject")
	typ := fs.String("type", "Concept", "OKF type")
	title := fs.String("title", "", "title")
	desc := fs.String("description", "", "description")
	body := fs.String("body", "", "markdown body")
	visibility := fs.String("visibility", "", "private|team|public")
	owner := fs.String("owner", "", "owner")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("put: --id is required")
	}
	svc, err := mustAssemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	d := &okf.Document{Type: *typ, Title: *title, Description: *desc, Body: *body}
	if *visibility != "" {
		d.SetVisibility(*visibility)
	}
	if *owner != "" {
		d.SetOwner(*owner)
	}
	res, err := svc.Put(context.Background(), *subject, *id, d)
	if err != nil {
		return err
	}
	return printJSON(out, res)
}

func cmdQuery(args []string, out io.Writer) error {
	fs := newFlags("query")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	q := fs.String("q", "", "query text")
	subject := fs.String("subject", "", "requesting subject")
	topK := fs.Int("top-k", 5, "result limit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *q == "" {
		return fmt.Errorf("query: --q is required")
	}
	svc, err := mustAssemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	res, err := svc.Query(context.Background(), *subject, *q, *topK)
	if err != nil {
		return err
	}
	return printJSON(out, res)
}

func cmdMigrate(args []string, out io.Writer) error {
	fs := newFlags("migrate")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	job := fs.String("job", "", "job name (resumable)")
	direction := fs.String("direction", router.DirectionToRAG, "canonical_to_rag|rag_to_canonical")
	target := fs.String("target", "memory", "RAG adapter name")
	dryRun := fs.Bool("dry-run", false, "plan only")
	limit := fs.Int("limit", 100, "max concepts per run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := mustAssemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	rep, err := svc.RunMigration(context.Background(), router.MigrationOptions{
		Job: *job, Direction: *direction, Target: *target, DryRun: *dryRun, Limit: *limit,
	})
	if err != nil {
		return err
	}
	return printJSON(out, rep)
}

func cmdDecisions(args []string, out io.Writer) error {
	fs := newFlags("decisions")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	id := fs.String("id", "", "filter by concept id")
	limit := fs.Int("limit", 20, "max records")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := mustAssemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	records, err := svc.Decisions(context.Background(), *id, *limit)
	if err != nil {
		return err
	}
	return printJSON(out, records)
}

func cmdConcepts(args []string, out io.Writer) error {
	fs := newFlags("concepts")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := mustAssemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	ids, err := svc.Concepts(context.Background())
	if err != nil {
		return err
	}
	return printJSON(out, ids)
}

type paramFlags struct{ values map[string]any }

func (p *paramFlags) String() string {
	keys := make([]string, 0, len(p.values))
	for k := range p.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+fmt.Sprint(p.values[k]))
	}
	return strings.Join(parts, ",")
}

func (p *paramFlags) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok || strings.TrimSpace(k) == "" {
		return fmt.Errorf("compute: --param must be name=value")
	}
	if p.values == nil {
		p.values = map[string]any{}
	}
	p.values[k] = v
	return nil
}

func cmdCompute(args []string, out io.Writer) error {
	fs := newFlags("compute")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	id := fs.String("id", "", "Attested Computation concept id")
	subject := fs.String("subject", "", "requesting subject")
	params := &paramFlags{values: map[string]any{}}
	fs.Var(params, "param", "parameter value as name=value (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("compute: --id is required")
	}
	svc, err := mustAssemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	env, err := svc.RunComputation(context.Background(), compute.Request{
		ConceptID: *id, Subject: *subject, Parameters: params.values,
	})
	if err != nil {
		return err
	}
	return printJSON(out, env)
}

func cmdComputations(args []string, out io.Writer) error {
	fs := newFlags("computations")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	id := fs.String("id", "", "filter by concept id")
	limit := fs.Int("limit", 20, "max records")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := mustAssemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	records, err := svc.ComputationAudit(context.Background(), *id, *limit)
	if err != nil {
		return err
	}
	return printJSON(out, records)
}

func cmdValidate(args []string, out io.Writer) error {
	fs := newFlags("validate")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := mustLoad(*envPath)
	if err != nil {
		return err
	}
	return printJSON(out, map[string]any{
		"status": "ok", "mode": cfg.Server.Mode, "adapters": len(cfg.Adapters),
	})
}

func cmdEval(args []string, out io.Writer) error {
	fs := newFlags("eval")
	dataset := fs.String("dataset", "testdata/eval.jsonl", "JSONL ranking dataset")
	k := fs.Int("k", 5, "evaluation cutoff")
	if err := fs.Parse(args); err != nil {
		return err
	}
	f, err := os.Open(*dataset)
	if err != nil {
		return err
	}
	defer f.Close()
	var samples []eval.RankedSample
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if text == "" {
			continue
		}
		var s eval.RankedSample
		if err := json.Unmarshal([]byte(text), &s); err != nil {
			return fmt.Errorf("eval: line %d: %w", line, err)
		}
		samples = append(samples, s)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return printJSON(out, eval.Evaluate(samples, *k))
}
