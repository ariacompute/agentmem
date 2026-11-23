package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"agentmem/internal/auth"
	"agentmem/internal/config"
	"agentmem/internal/mcp"
	"agentmem/internal/service"
)

// mcpStdin is the stdio transport's input. It is a seam so the command can be
// tested without replacing the process's standard input.
var mcpStdin io.Reader = os.Stdin

// mcpStdioMaxLine mirrors the HTTP body bound: one oversized frame is answered
// with a parse error instead of growing the process.
const mcpStdioMaxLine = 4 << 20

// cmdMCP serves MCP over stdio (JSON Lines), which is how local clients such as
// Claude Desktop and Cursor launch a server. Standard output carries protocol
// frames only; every diagnostic goes to standard error.
func cmdMCP(args []string, out io.Writer) error {
	fs := newFlags("mcp")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	token := fs.String("token", "", "bearer token (preferred; or AGENTMEM_MCP_TOKEN)")
	tokenEnv := fs.String("token-env", "", "read the bearer token from this environment variable")
	subject := fs.String("subject", "", "fallback subject when authentication is disabled")
	scopes := fs.String("scopes", "", "fallback comma separated scopes, e.g. knowledge:read,graph:write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := mustLoad(*envPath)
	if err != nil {
		return err
	}
	authenticator, err := buildAuth(cfg)
	if err != nil {
		return err
	}
	principal, err := resolveMCPPrincipal(cfg, authenticator, *token, *tokenEnv, *subject, *scopes)
	if err != nil {
		return err
	}
	svc, err := assembleService(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer svc.Close()
	if !svc.GraphEnabled() {
		return errors.New("mcp: the graph feature is disabled (AGENTMEM_GRAPH_ENABLED=false)")
	}
	server := mcp.NewServer(service.NewMCPBackend(svc), mcpConfig(cfg))
	fmt.Fprintf(os.Stderr, "agentmem mcp: stdio ready (subject=%s, write=%t)\n", principal.Subject(), cfg.MCP.Write)
	return runMCPStdio(mcpStdin, out, server, principal)
}

// resolveMCPPrincipal establishes the one principal a stdio process runs as.
// A bearer token wins so the subject and scopes match the HTTP surface exactly;
// the explicit subject/scopes fallback is only accepted with authentication
// disabled, where the process boundary is the trust boundary. Neither means the
// command refuses to start.
func resolveMCPPrincipal(cfg *config.Config, authenticator *auth.Authenticator, token, tokenEnv, subject, scopes string) (auth.Principal, error) {
	raw := strings.TrimSpace(token)
	if raw == "" && strings.TrimSpace(tokenEnv) != "" {
		raw = strings.TrimSpace(os.Getenv(strings.TrimSpace(tokenEnv)))
	}
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("AGENTMEM_MCP_TOKEN"))
	}
	if raw != "" {
		principal, ok := authenticator.Authenticate(raw)
		if !ok {
			return auth.Principal{}, errors.New("mcp: the bearer token is not recognised")
		}
		return principal, nil
	}
	if cfg.Auth.Enabled {
		return auth.Principal{}, errors.New("mcp: AGENTMEM_AUTH_ENABLED=true requires --token, --token-env or AGENTMEM_MCP_TOKEN")
	}
	subject = strings.TrimSpace(subject)
	scopes = strings.TrimSpace(scopes)
	if subject == "" || scopes == "" {
		return auth.Principal{}, errors.New("mcp: provide --token, or --subject with --scopes")
	}
	perms := make([]auth.Permission, 0, 4)
	for _, raw := range strings.Split(scopes, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return auth.Principal{}, errors.New("mcp: --scopes contains an empty entry")
		}
		perm, err := auth.ParsePermission(raw)
		if err != nil {
			return auth.Principal{}, err
		}
		perms = append(perms, perm)
	}
	return auth.DeclaredPrincipal(subject, perms)
}

// runMCPStdio reads one JSON-RPC payload per line and writes one response per
// payload. EOF ends the process successfully; a malformed or oversized line is
// answered and the loop continues.
func runMCPStdio(in io.Reader, out io.Writer, server *mcp.Server, principal auth.Principal) error {
	reader := bufio.NewReaderSize(in, 64*1024)
	writer := bufio.NewWriter(out)
	defer writer.Flush()
	for {
		line, tooLarge, err := readMCPLine(reader, mcpStdioMaxLine)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if tooLarge {
			if err := writeMCPStdio(writer, mcpFailurePayload(mcp.CodeParse, "request body is too large")); err != nil {
				return err
			}
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		outcome, failure := server.Handle(context.Background(), principal, line, mcp.Options{Stateless: true})
		if failure != nil {
			if err := writeMCPStdio(writer, mcpFailurePayload(failure.Code, failure.Message)); err != nil {
				return err
			}
			continue
		}
		if outcome.Payload == nil {
			continue
		}
		if err := writeMCPStdio(writer, outcome.Payload); err != nil {
			return err
		}
	}
}

func writeMCPStdio(w io.Writer, payload []byte) error {
	if _, err := w.Write(payload); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	if flusher, ok := w.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

func mcpFailurePayload(code int, message string) []byte {
	payload, err := json.Marshal(mcp.Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage("null"),
		Error:   &mcp.Error{Code: code, Message: message},
	})
	if err != nil {
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"internal error"}}`)
	}
	return payload
}

// readMCPLine reads one line without letting an attacker-sized line allocate
// without bound. tooLarge reports that the line exceeded max and the remainder
// was discarded, so the next line still parses.
func readMCPLine(r *bufio.Reader, max int) (line []byte, tooLarge bool, err error) {
	var buf []byte
	for {
		chunk, isPrefix, readErr := r.ReadLine()
		if readErr != nil {
			if len(buf) > 0 && errors.Is(readErr, io.EOF) {
				return buf, false, nil
			}
			return nil, false, readErr
		}
		if len(buf)+len(chunk) > max {
			for isPrefix {
				if _, isPrefix, _ = r.ReadLine(); !isPrefix {
					break
				}
			}
			return nil, true, nil
		}
		buf = append(buf, chunk...)
		if !isPrefix {
			return buf, false, nil
		}
	}
}
