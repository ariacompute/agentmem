package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpAndUnknownCommand(t *testing.T) {
	var buf bytes.Buffer
	if err := Run(nil, &buf); err != nil || !strings.Contains(buf.String(), "Commands:") {
		t.Fatalf("help = %q (%v)", buf.String(), err)
	}
	if err := Run([]string{"nope"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown command must fail")
	}
}

func TestValidateCommand(t *testing.T) {
	var buf bytes.Buffer
	if err := Run([]string{"validate", "--env", "../../nonexistent.env"}, &buf); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil || out["status"] != "ok" {
		t.Fatalf("validate output = %q (%v)", buf.String(), err)
	}
}

func TestEvalCommand(t *testing.T) {
	var buf bytes.Buffer
	if err := Run([]string{"eval", "--dataset", "../../testdata/eval.jsonl", "--k", "3"}, &buf); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Samples int     `json:"samples"`
		Recall  float64 `json:"recall_at_k"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil || out.Samples != 4 {
		t.Fatalf("eval output = %q (%v)", buf.String(), err)
	}
	if out.Recall <= 0 || out.Recall > 1 {
		t.Fatalf("recall = %f", out.Recall)
	}
}

func TestPutQueryCommandsOffline(t *testing.T) {
	t.Setenv("AGENTMEM_GIT_ROOT", filepath.Join(t.TempDir(), "bundle"))
	var buf bytes.Buffer
	if err := Run([]string{
		"put", "--env", "../../nonexistent.env",
		"--id", "metrics/test", "--subject", "alice", "--type", "Metric",
		"--title", "Test", "--body", "# Body", "--visibility", "public",
	}, &buf); err != nil {
		t.Fatal(err)
	}
	var put struct {
		ContentHash string `json:"content_hash"`
		CommitSHA   string `json:"commit_sha"`
	}
	if err := json.Unmarshal(buf.Bytes(), &put); err != nil || put.ContentHash == "" {
		t.Fatalf("put output = %q (%v)", buf.String(), err)
	}
}
