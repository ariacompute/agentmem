package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := Run(append(args, "--env", "../../nonexistent.env"), &buf)
	return buf.String(), err
}

func TestGraphCommandHelpAndErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := Run([]string{"graph"}, &buf); err != nil || !strings.Contains(buf.String(), "graph actions") {
		t.Fatalf("graph help = %q (%v)", buf.String(), err)
	}
	if err := Run([]string{"graph", "help"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"graph", "nope"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown action must fail")
	}
	for _, args := range [][]string{
		{"graph", "node"},
		{"graph", "neighbors"},
		{"graph", "path"},
		{"graph", "path", "--from", "a"},
		{"graph", "answerable"},
		{"graph", "retrieve"},
		{"graph", "put"},
		{"graph", "edge"},
	} {
		if _, err := runCLI(t, args...); err == nil {
			t.Fatalf("%v must require its flags", args)
		}
	}
}

func TestGraphCommandWorkflowOffline(t *testing.T) {
	t.Setenv("AGENTMEM_GIT_ROOT", filepath.Join(t.TempDir(), "bundle"))

	out, err := runCLI(t, "graph", "put", "--subject", "alice", "--id", "tables/orders", "--node-type", "table", "--label", "Orders")
	if err != nil || !strings.Contains(out, "commit_sha") {
		t.Fatalf("put table = %q (%v)", out, err)
	}
	out, err = runCLI(t, "graph", "put", "--subject", "alice", "--id", "metrics/revenue", "--node-type", "metric", "--label", "Revenue", "--description", "Net revenue")
	if err != nil || !strings.Contains(out, "content_hash") {
		t.Fatalf("put node = %q (%v)", out, err)
	}
	out, err = runCLI(t, "graph", "edge", "--subject", "alice", "--id", "metrics/revenue", "--to", "tables/orders", "--type", "computed_from", "--evidence", "sources[0]")
	if err != nil || !strings.Contains(out, "warnings") {
		t.Fatalf("edge = %q (%v)", out, err)
	}

	out, err = runCLI(t, "graph", "node", "--subject", "alice", "--id", "metrics/revenue")
	if err != nil || !strings.Contains(out, `"label": "Revenue"`) || !strings.Contains(out, `"out"`) {
		t.Fatalf("node = %q (%v)", out, err)
	}

	out, err = runCLI(t, "graph", "neighbors", "--subject", "alice", "--from", "metrics/revenue", "--depth", "1", "--direction", "out", "--edge-types", "computed_from")
	if err != nil || !strings.Contains(out, "tables/orders") {
		t.Fatalf("neighbors = %q (%v)", out, err)
	}

	out, err = runCLI(t, "graph", "path", "--subject", "alice", "--from", "metrics/revenue", "--to", "tables/orders", "--max-depth", "2")
	if err != nil || !strings.Contains(out, `"found": true`) {
		t.Fatalf("path = %q (%v)", out, err)
	}

	out, err = runCLI(t, "graph", "search", "--subject", "alice", "--q", "revenue", "--node-types", "metric", "--limit", "5")
	if err != nil || !strings.Contains(out, "metrics/revenue") {
		t.Fatalf("search = %q (%v)", out, err)
	}

	out, err = runCLI(t, "graph", "answerable", "--subject", "alice", "--q", "what is revenue")
	if err != nil || !strings.Contains(out, `"verdict"`) {
		t.Fatalf("answerable = %q (%v)", out, err)
	}

	out, err = runCLI(t, "graph", "retrieve", "--subject", "alice", "--q", "revenue", "--top-k", "5")
	if err != nil || !strings.Contains(out, `"hits"`) {
		t.Fatalf("retrieve = %q (%v)", out, err)
	}

	out, err = runCLI(t, "graph", "stats", "--subject", "alice")
	if err != nil {
		t.Fatalf("stats = %q (%v)", out, err)
	}
	var stats struct {
		Nodes int `json:"nodes"`
		Edges int `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &stats); err != nil || stats.Nodes < 2 || stats.Edges < 1 {
		t.Fatalf("stats = %q (%v)", out, err)
	}

	out, err = runCLI(t, "graph", "reindex")
	if err != nil || !strings.Contains(out, "pruned") {
		t.Fatalf("reindex = %q (%v)", out, err)
	}
}
