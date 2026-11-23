package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmem/internal/auth"
	"agentmem/internal/config"
	"agentmem/internal/mcp"
	"agentmem/internal/service"
)

// graphCommands are the actions of `agentmem graph`, each with the minimum
// flags it needs to reach the service call.
var graphCommands = map[string][]string{
	"stats":      {"stats"},
	"node":       {"node", "--id", "metrics/x"},
	"neighbors":  {"neighbors", "--from", "metrics/x"},
	"path":       {"path", "--from", "a", "--to", "b"},
	"search":     {"search", "--q", "x"},
	"answerable": {"answerable", "--q", "x"},
	"retrieve":   {"retrieve", "--q", "x"},
	"put":        {"put", "--id", "metrics/x"},
	"edge":       {"edge", "--id", "metrics/x", "--to", "metrics/y", "--type", "related"},
	"reindex":    {"reindex"},
}

func TestGraphCommandsPropagateServiceErrors(t *testing.T) {
	withBrokenService(t)
	env := envFile(t)
	for name, args := range graphCommands {
		t.Run(name, func(t *testing.T) {
			full := append([]string{"graph"}, args...)
			full = append(full, "--env", env)
			if err := Run(full, io.Discard); err == nil {
				t.Fatalf("%s must propagate the service error", name)
			}
		})
	}
}

func TestGraphCommandsPropagateAssemblyErrors(t *testing.T) {
	previous := assemble
	assemble = func(string) (*service.KnowledgeService, error) { return nil, errors.New("assembly refused") }
	t.Cleanup(func() { assemble = previous })

	env := envFile(t)
	for name, args := range graphCommands {
		t.Run(name, func(t *testing.T) {
			full := append([]string{"graph"}, args...)
			full = append(full, "--env", env)
			if err := Run(full, io.Discard); err == nil || !strings.Contains(err.Error(), "assembly refused") {
				t.Fatalf("%s: error = %v", name, err)
			}
		})
	}
}

func TestGraphCommandsRejectUnknownFlags(t *testing.T) {
	env := envFile(t)
	for name, args := range graphCommands {
		t.Run(name, func(t *testing.T) {
			full := append([]string{"graph"}, args...)
			full = append(full, "--env", env, "--bogus")
			if err := Run(full, io.Discard); err == nil {
				t.Fatal("an unknown flag must fail the command")
			}
		})
	}
}

func TestMCPDispatchAndStartupFailures(t *testing.T) {
	// The root dispatcher reaches cmdMCP; a missing identity fails closed.
	t.Setenv("AGENTMEM_MCP_TOKEN", "")
	env := writeTempEnv(t, "AGENTMEM_GRAPH_ENABLED=true\n")
	if err := Run([]string{"mcp", "--env", env}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "provide --token") {
		t.Fatalf("dispatch error = %v", err)
	}

	if err := Run([]string{"mcp", "--bogus"}, io.Discard); err == nil {
		t.Fatal("an unknown flag must fail")
	}

	bad := writeTempEnv(t, "AGENTMEM_UNKNOWN_KEY=1\n")
	if err := Run([]string{"mcp", "--env", bad}, io.Discard); err == nil {
		t.Fatal("an invalid configuration must fail")
	}

	// An authenticator that refuses to build stops the command before assembly,
	// even though a valid configuration should make that impossible.
	previousAuth := buildAuth
	buildAuth = func(*config.Config) (*auth.Authenticator, error) { return nil, errors.New("auth refused") }
	err := Run([]string{"mcp", "--env", env, "--subject", "a", "--scopes", "knowledge:read"}, io.Discard)
	buildAuth = previousAuth
	if err == nil || !strings.Contains(err.Error(), "auth refused") {
		t.Fatalf("auth error = %v", err)
	}

	// A failed assembly surfaces unchanged.
	previousAssemble := assembleService
	assembleService = func(context.Context, *config.Config) (*service.KnowledgeService, error) {
		return nil, errors.New("assembly refused")
	}
	err = Run([]string{"mcp", "--env", env, "--subject", "a", "--scopes", "knowledge:read"}, io.Discard)
	assembleService = previousAssemble
	if err == nil || !strings.Contains(err.Error(), "assembly refused") {
		t.Fatalf("assembly error = %v", err)
	}
}

func writeTempEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConsoleModeReportsThePosture(t *testing.T) {
	cfg := config.Default()
	if got := consoleMode(cfg); got != "embedded" {
		t.Fatalf("consoleMode = %q", got)
	}
	cfg.Console.Enabled = false
	if got := consoleMode(cfg); got != "disabled" {
		t.Fatalf("consoleMode = %q", got)
	}
}

func TestRunMCPStdioPropagatesReadErrors(t *testing.T) {
	principal, err := auth.DeclaredPrincipal("local", []auth.Permission{auth.PermKnowledgeRead})
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(nil, mcp.Config{})
	if err := runMCPStdio(errReader{}, io.Discard, server, principal); err == nil {
		t.Fatal("a read error must surface")
	}
}

func TestRunMCPStdioPropagatesWriteErrors(t *testing.T) {
	principal, err := auth.DeclaredPrincipal("local", []auth.Permission{auth.PermKnowledgeRead})
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(nil, mcp.Config{})
	valid := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"

	cases := map[string]string{
		"response":  valid,
		"parse":     "{\n",
		"oversized": strings.Repeat("x", mcpStdioMaxLine+1) + "\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if err := runMCPStdio(strings.NewReader(input), failingWriter{}, server, principal); err == nil {
				t.Fatal("a write error must surface")
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read refused") }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write refused") }

// shortWriter succeeds for the payload and fails on the trailing newline, so
// both write calls inside writeMCPStdio are exercised.
type shortWriter struct {
	calls int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls > 1 {
		return 0, errors.New("newline refused")
	}
	return len(p), nil
}

func TestWriteMCPStdioReportsEachFailure(t *testing.T) {
	if err := writeMCPStdio(failingWriter{}, []byte("payload")); err == nil {
		t.Fatal("a payload write error must surface")
	}
	if err := writeMCPStdio(&shortWriter{}, []byte("payload")); err == nil {
		t.Fatal("a newline write error must surface")
	}
	// A plain buffer has no Flush method and must still succeed.
	var plain bytes.Buffer
	if err := writeMCPStdio(&plain, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if plain.String() != "payload\n" {
		t.Fatalf("output = %q", plain.String())
	}
}

func TestReadMCPLineHandlesEOFAndDraining(t *testing.T) {
	// A line longer than the read buffer with no newline followed by EOF keeps
	// the bytes already read instead of dropping them.
	line, tooLarge, err := readMCPLine(bufio.NewReaderSize(strings.NewReader(strings.Repeat("a", 32)), 16), 64)
	if err != nil || tooLarge || string(line) != strings.Repeat("a", 32) {
		t.Fatalf("partial line = %d bytes %v %v", len(line), tooLarge, err)
	}

	// An oversized line is drained so the next line still parses.
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 64)+"\nok\n"), 8)
	if _, tooLarge, err := readMCPLine(reader, 5); err != nil || !tooLarge {
		t.Fatalf("oversized = %v %v", tooLarge, err)
	}
	line, tooLarge, err = readMCPLine(reader, 5)
	if err != nil || tooLarge || string(line) != "ok" {
		t.Fatalf("next line = %q %v %v", line, tooLarge, err)
	}
}
