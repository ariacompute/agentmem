package cli

import (
	"bytes"
	"testing"
)

// An unknown AGENTMEM_* key makes config.Load fail closed, which exercises the
// error-return path of mustLoad/mustAssemble in every command.
func TestCommandsFailOnUnknownEnvKey(t *testing.T) {
	t.Setenv("AGENTMEM_BOGUS_KEY", "x")
	cases := [][]string{
		{"put", "--id", "x"},
		{"query", "--q", "x"},
		{"migrate"},
		{"decisions"},
		{"concepts"},
		{"compute", "--id", "x"},
		{"computations"},
		{"validate"},
		{"serve"},
	}
	for _, args := range cases {
		full := append(append([]string{}, args...), "--env", "../../nonexistent.env")
		if err := Run(full, &bytes.Buffer{}); err == nil {
			t.Fatalf("%v should fail when config.Load rejects an unknown key", args)
		}
	}
}

func TestPutCommandVisibilityAndOwner(t *testing.T) {
	offlineRoot(t)
	var buf bytes.Buffer
	if err := Run([]string{"put", "--env", "../../nonexistent.env", "--id", "concepts/v",
		"--type", "Concept", "--subject", "alice", "--owner", "alice",
		"--visibility", "private", "--title", "t", "--description", "d", "--body", "b"}, &buf); err != nil {
		t.Fatalf("put with visibility/owner: %v / %s", err, buf.String())
	}
}

func TestComputeCommandMissingConcept(t *testing.T) {
	offlineRoot(t)
	var buf bytes.Buffer
	if err := Run([]string{"compute", "--env", "../../nonexistent.env", "--id", "does/not/exist",
		"--param", "x=1"}, &buf); err == nil {
		t.Fatal("missing computation must error")
	}
}
