package cli

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"agentmem/internal/auth"
	"agentmem/internal/config"
	"agentmem/internal/mcp"
)

func authEnabledConfig(token string) *config.Config {
	cfg := config.Default()
	cfg.Auth.Enabled = true
	cfg.Auth.Tokens = []config.AuthTokenSpec{{
		TokenSHA256: auth.Digest(token),
		Subject:     "alice",
		Scopes:      []string{"knowledge:read", "knowledge:write"},
	}}
	return cfg
}

func TestResolveMCPPrincipalWithToken(t *testing.T) {
	cfg := authEnabledConfig("s3cret")
	authenticator, err := buildAuth(cfg)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := resolveMCPPrincipal(cfg, authenticator, "s3cret", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject() != "alice" || !principal.Allows(auth.PermKnowledgeWrite) {
		t.Fatalf("principal = %+v", principal)
	}

	if _, err := resolveMCPPrincipal(cfg, authenticator, "wrong", "", "", ""); err == nil {
		t.Fatal("an unknown token must be rejected")
	}
	// With authentication enabled the declared fallback is refused.
	_, err = resolveMCPPrincipal(cfg, authenticator, "", "", "alice", "knowledge:read")
	if err == nil || !strings.Contains(err.Error(), "requires --token") {
		t.Fatalf("declared fallback must not bypass authentication: %v", err)
	}
	if strings.Contains(err.Error(), ".env sets") {
		t.Fatalf("the .env hint must stay off when the file declares no token: %v", err)
	}
}

func TestResolveMCPPrincipalExplainsTokenInDotEnv(t *testing.T) {
	cfg := authEnabledConfig("s3cret")
	authenticator, err := buildAuth(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// A token pasted into .env is deliberately ignored; the refusal says so
	// instead of only demanding a token the operator believes they configured.
	cfg.MCP.TokenInEnvFile = true
	_, err = resolveMCPPrincipal(cfg, authenticator, "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "requires --token") || !strings.Contains(err.Error(), ".env sets AGENTMEM_MCP_TOKEN") {
		t.Fatalf("auth-enabled refusal = %v", err)
	}

	// The same confusion reaches the other refusal when authentication is off.
	open := config.Default()
	open.MCP.TokenInEnvFile = true
	openAuth, err := buildAuth(open)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTMEM_MCP_TOKEN", "")
	_, err = resolveMCPPrincipal(open, openAuth, "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "provide --token") || !strings.Contains(err.Error(), ".env sets AGENTMEM_MCP_TOKEN") {
		t.Fatalf("auth-disabled refusal = %v", err)
	}
}

func TestResolveMCPPrincipalTokenSources(t *testing.T) {
	cfg := authEnabledConfig("from-env")
	authenticator, err := buildAuth(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_TEST_TOKEN", "from-env")
	principal, err := resolveMCPPrincipal(cfg, authenticator, "", "MCP_TEST_TOKEN", "", "")
	if err != nil || principal.Subject() != "alice" {
		t.Fatalf("token-env = %+v, %v", principal, err)
	}
	t.Setenv("AGENTMEM_MCP_TOKEN", "from-env")
	if _, err := resolveMCPPrincipal(cfg, authenticator, "", "", "", ""); err != nil {
		t.Fatalf("AGENTMEM_MCP_TOKEN = %v", err)
	}
}

func TestResolveMCPPrincipalDeclaredFallback(t *testing.T) {
	cfg := config.Default() // authentication disabled
	authenticator, err := buildAuth(cfg)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := resolveMCPPrincipal(cfg, authenticator, "", "", "bob", "knowledge:read,graph:write")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject() != "bob" || !principal.Allows(auth.PermGraphWrite) || principal.Allows(auth.PermKnowledgeWrite) {
		t.Fatalf("principal = %+v", principal)
	}
	for _, tc := range []struct{ subject, scopes string }{
		{"", "knowledge:read"},
		{"bob", ""},
		{"bob", "knowledge:read,"},
		{"bob", "root:all"},
	} {
		if _, err := resolveMCPPrincipal(cfg, authenticator, "", "", tc.subject, tc.scopes); err == nil {
			t.Fatalf("fallback %q/%q must fail closed", tc.subject, tc.scopes)
		}
	}
	// No token and no declaration is a hard error.
	if _, err := resolveMCPPrincipal(cfg, authenticator, "", "", "", ""); err == nil {
		t.Fatal("missing identity must fail closed")
	}
}

func TestRunMCPStdioLoop(t *testing.T) {
	server := mcp.NewServer(nil, mcp.Config{})
	principal, err := auth.DeclaredPrincipal("local", []auth.Permission{auth.PermKnowledgeRead})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{`,
		``,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
	}, "\n")
	var out bytes.Buffer
	if err := runMCPStdio(strings.NewReader(input), &out, server, principal); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines = %d: %s", len(lines), out.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, `"jsonrpc":"2.0"`) {
			t.Fatalf("non-protocol line %q", line)
		}
	}
	if !strings.Contains(lines[2], "-32700") {
		t.Fatalf("malformed line = %q", lines[2])
	}
}

func TestRunMCPStdioOversizedLine(t *testing.T) {
	server := mcp.NewServer(nil, mcp.Config{})
	principal, err := auth.DeclaredPrincipal("local", []auth.Permission{auth.PermKnowledgeRead})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat("x", mcpStdioMaxLine+10) + "\n" + `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	var out bytes.Buffer
	if err := runMCPStdio(strings.NewReader(input), &out, server, principal); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "too large") || !strings.Contains(lines[1], `"result"`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestCmdMCPStdio(t *testing.T) {
	t.Setenv("AGENTMEM_GIT_ROOT", filepath.Join(t.TempDir(), "bundle"))
	t.Setenv("AGENTMEM_MCP_TOKEN", "")
	previous := mcpStdin
	mcpStdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n")
	t.Cleanup(func() { mcpStdin = previous })

	var out bytes.Buffer
	if err := cmdMCP([]string{"--env", "../../nonexistent.env", "--subject", "alice", "--scopes", "knowledge:read"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "protocolVersion") || !strings.Contains(out.String(), "2025-06-18") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestCmdMCPFailsWithoutIdentity(t *testing.T) {
	t.Setenv("AGENTMEM_GIT_ROOT", filepath.Join(t.TempDir(), "bundle"))
	t.Setenv("AGENTMEM_MCP_TOKEN", "")
	if err := cmdMCP([]string{"--env", "../../nonexistent.env"}, &bytes.Buffer{}); err == nil {
		t.Fatal("mcp without an identity must refuse to start")
	}
}

func TestCmdMCPRequiresTheGraph(t *testing.T) {
	t.Setenv("AGENTMEM_GIT_ROOT", filepath.Join(t.TempDir(), "bundle"))
	t.Setenv("AGENTMEM_GRAPH_ENABLED", "false")
	err := cmdMCP([]string{"--env", "../../nonexistent.env", "--subject", "alice", "--scopes", "knowledge:read"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "graph") {
		t.Fatalf("disabled graph = %v", err)
	}
}

func TestReadMCPLineHandlesPartialLines(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader("abc"), 2)
	line, tooLarge, err := readMCPLine(reader, 16)
	if err != nil || tooLarge || string(line) != "abc" {
		t.Fatalf("line = %q %v %v", line, tooLarge, err)
	}
}
