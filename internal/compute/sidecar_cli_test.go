package compute

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agentmem/internal/bridges"
)

// cliRunnerFake mimics a sidecar process: `run` reads the JSON request on
// stdin and writes the JSON response to stdout.
func cliRunnerFake(t *testing.T, rows [][]any, tamperDigest bool) bridges.Runner {
	t.Helper()
	return func(_ context.Context, command string, args []string, stdin []byte) ([]byte, error) {
		if command != "agentmem-py" || len(args) != 1 || args[0] != "run" {
			return nil, errors.New("unexpected command line")
		}
		var req sidecarRequest
		if err := json.Unmarshal(stdin, &req); err != nil {
			return nil, err
		}
		digest := BodyDigest(req.Body)
		if tamperDigest {
			digest = strings.Repeat("f", 64)
		}
		return json.Marshal(map[string]any{
			"columns": []string{"revenue"}, "rows": rows, "body_digest": digest,
		})
	}
}

func TestSidecarCLIRuntimeExecutesAndAttests(t *testing.T) {
	b := testBundleWith(t, "computations/py.md", pythonDoc)
	r := NewRuntimeRegistry(FakeExecutor{}, 10)
	if err := r.Configure([]SidecarSpec{{Runtime: "python", Bridge: "cli", Command: "agentmem-py"}},
		nil, cliRunnerFake(t, [][]any{{1250}}, false)); err != nil {
		t.Fatal(err)
	}
	env, err := NewService(b, r, nil, 2*time.Second, 10).Run(context.Background(), Request{
		ConceptID: "computations/py", Parameters: map[string]any{"year": 2026},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !env.DisplayAllowed || env.DisplayValue != "1250" || !env.Verdict.Passed {
		t.Fatalf("cli sidecar run not attested: %+v", env)
	}
}

func TestSidecarCLIDigestMismatchRefusesDisplay(t *testing.T) {
	b := testBundleWith(t, "computations/py.md", pythonDoc)
	r := NewRuntimeRegistry(FakeExecutor{}, 10)
	if err := r.Configure([]SidecarSpec{{Runtime: "python", Bridge: "cli", Command: "agentmem-py"}},
		nil, cliRunnerFake(t, [][]any{{1250}}, true)); err != nil {
		t.Fatal(err)
	}
	env, err := NewService(b, r, nil, 2*time.Second, 10).Run(context.Background(), Request{
		ConceptID: "computations/py", Parameters: map[string]any{"year": 2026},
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.DisplayAllowed || env.DisplayValue != "" {
		t.Fatalf("mismatched digest must not be displayed: %+v", env)
	}
}

func TestSidecarCLIProcessFailureIsRedacted(t *testing.T) {
	b := testBundleWith(t, "computations/py.md", pythonDoc)
	runner := func(context.Context, string, []string, []byte) ([]byte, error) {
		return nil, errors.New("/usr/local/bin/agentmem-py: secret body leaked")
	}
	r := NewRuntimeRegistry(FakeExecutor{}, 10)
	if err := r.Configure([]SidecarSpec{{Runtime: "python", Bridge: "cli", Command: "agentmem-py"}},
		nil, runner); err != nil {
		t.Fatal(err)
	}
	_, err := NewService(b, r, nil, 2*time.Second, 10).Run(context.Background(), Request{
		ConceptID: "computations/py", Parameters: map[string]any{"year": 2026},
	})
	if !errors.Is(err, ErrExecution) {
		t.Fatalf("got %v, want ErrExecution", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/usr/local") {
		t.Fatalf("sidecar stderr must never be surfaced: %v", err)
	}
}

func TestSidecarScriptContractIsNotSQL(t *testing.T) {
	b := testBundleWith(t, "computations/py.md", pythonDoc)
	c, err := LoadContract(b, "computations/py")
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != KindScript {
		t.Fatalf("kind = %q, want %q", c.Kind, KindScript)
	}
	if !strings.Contains(c.SQL, "import json") {
		t.Fatalf("body = %q", c.SQL)
	}
	// A script keeps its own text: no :name -> $N rewriting may happen.
	bound, err := Bind(c, map[string]any{"year": 2026})
	if err != nil {
		t.Fatal(err)
	}
	if bound.Statement != c.SQL {
		t.Fatalf("script body was rewritten: %q", bound.Statement)
	}
	if len(bound.Args) != 1 || bound.Args[0] != int64(2026) {
		t.Fatalf("args = %#v", bound.Args)
	}
}

func TestSafeBodyByKind(t *testing.T) {
	if _, err := SafeBody(KindScript, "print(1);print(2) # semicolons are legal python"); err != nil {
		t.Fatalf("a script keeps its own syntax: %v", err)
	}
	if _, err := SafeBody(KindScript, "print\x00(1)"); err == nil {
		t.Fatal("a NUL byte must be rejected")
	}
	if _, err := SafeBody(KindScript, "  "); err == nil {
		t.Fatal("an empty body must be rejected")
	}
	if _, err := SafeBody(KindModel, "model: a\n---\nmodel: b"); err == nil {
		t.Fatal("a second YAML document must be rejected")
	}
	if _, err := SafeBody(KindQuery, "---\nexplore: orders\nquery_id: q\n"); err != nil {
		t.Fatalf("a leading document marker is fine: %v", err)
	}
	if _, err := SafeBody(KindSQL, "DELETE FROM orders"); err == nil {
		t.Fatal("SQL kinds keep the read-only guard")
	}
}
