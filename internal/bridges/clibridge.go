package bridges

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
)

// Runner executes a command without a shell. Tests inject a deterministic one.
type Runner func(ctx context.Context, command string, args []string, stdin []byte) ([]byte, error)

var safeArg = regexp.MustCompile(`^[A-Za-z0-9_./:+=@-]+$`)

func defaultRunner(timeout time.Duration) Runner {
	return func(ctx context.Context, command string, args []string, stdin []byte) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, command, args...)
		if len(stdin) > 0 {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("bridges: cli %s: %w: %s", command, err, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
}

// CLIBridge is a generic JSON-over-stdio RAG channel. The subcommands below
// form the formal CLI line protocol: an engine sidecar implements `health`,
// `put --id <id>`, `query --text <t> --top-k <n>` and `delete --id <id>` on
// top of its native SDK. Arguments are white-listed and never passed through a
// shell. Engine plugins declare this boundary in plugin.yaml (sdk_boundary).
type CLIBridge struct {
	name       string
	command    string
	runner     Runner
	offline    adapter.Adapter
	engineCaps adapter.Capabilities
}

func NewCLIBridge(cfg config.AdapterConfig, spec EngineSpec) (*CLIBridge, error) {
	if cfg.Name == "" {
		return nil, errors.New("bridges: cli adapter requires a name")
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	caps := spec.Capabilities
	caps.Put, caps.Query, caps.Delete = true, true, true
	b := &CLIBridge{
		name: cfg.Name, command: cfg.Endpoint, runner: defaultRunner(timeout), engineCaps: caps,
	}
	if spec.Command != "" && b.command == "" {
		b.command = spec.Command
	}
	if b.command == "" {
		b.offline = adapter.NewMemoryAdapter(adapter.NewHashingEmbedder(64))
	}
	return b, nil
}

func (b *CLIBridge) Name() string                       { return b.name }
func (b *CLIBridge) Capabilities() adapter.Capabilities { return b.engineCaps }
func (b *CLIBridge) Fingerprint() string                { return "bridge-cli/v1:" + b.name }
func (b *CLIBridge) Health(ctx context.Context) error {
	if b.offline != nil {
		return b.offline.Health(ctx)
	}
	_, err := b.run(ctx, []string{"health"}, nil)
	return err
}

func (b *CLIBridge) Put(ctx context.Context, conceptID string, d *okf.Document) error {
	if b.offline != nil {
		return b.offline.Put(ctx, conceptID, d)
	}
	payload, err := json.Marshal(putRequest{
		ConceptID: conceptID, Type: d.Type, Title: d.Title,
		Description: d.Description, Body: d.Body, Tags: d.Tags,
	})
	if err != nil {
		return err
	}
	_, err = b.run(ctx, []string{"put", "--id", conceptID}, payload)
	return err
}

func (b *CLIBridge) Query(ctx context.Context, q adapter.Query) ([]adapter.Hit, error) {
	if b.offline != nil {
		return b.offline.Query(ctx, q)
	}
	out, err := b.run(ctx, []string{"query", "--text", q.Text, "--top-k", itoa(q.TopK)}, nil)
	if err != nil {
		return nil, err
	}
	var resp queryResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("bridges: decode cli query output: %w", err)
	}
	for i := range resp.Hits {
		if resp.Hits[i].Source == "" {
			resp.Hits[i].Source = b.name
		}
	}
	return resp.Hits, nil
}

func (b *CLIBridge) Delete(ctx context.Context, conceptID string) error {
	if b.offline != nil {
		return b.offline.Delete(ctx, conceptID)
	}
	_, err := b.run(ctx, []string{"delete", "--id", conceptID}, nil)
	return err
}

func (b *CLIBridge) run(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	for _, a := range args {
		if !safeArg.MatchString(a) {
			return nil, fmt.Errorf("bridges: unsafe CLI argument %q", a)
		}
	}
	if !safeArg.MatchString(b.command) {
		return nil, fmt.Errorf("bridges: unsafe CLI command %q", b.command)
	}
	return b.runner(ctx, b.command, args, stdin)
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
