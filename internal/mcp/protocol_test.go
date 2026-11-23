package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, cfg Config) (*Server, *stubBackend) {
	t.Helper()
	backend := &stubBackend{}
	return NewServer(backend, cfg), backend
}

func decodeResult(t *testing.T, out Outcome) map[string]any {
	t.Helper()
	if out.Payload == nil {
		t.Fatal("no payload")
	}
	var payload struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(out.Payload, &payload); err != nil {
		t.Fatalf("payload %s: %v", out.Payload, err)
	}
	return payload.Result
}

func TestInitializeNegotiatesVersion(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	principal := readerPrincipal(t)

	cases := []struct {
		requested string
		want      string
		session   bool
	}{
		{Version20250618, Version20250618, true},
		{Version20250326, Version20250326, true},
		{Version20241105, Version20241105, false},
		{"1999-01-01", LatestVersion(), true},
	}
	for _, tc := range cases {
		t.Run(tc.requested, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + tc.requested + `"}}`
			out, failure := server.Handle(context.Background(), principal, []byte(body), Options{})
			if failure != nil {
				t.Fatalf("failure = %+v", failure)
			}
			result := decodeResult(t, out)
			if result["protocolVersion"] != tc.want {
				t.Fatalf("version = %v, want %s", result["protocolVersion"], tc.want)
			}
			if tc.session && (out.SessionID == "" || out.Version != tc.want) {
				t.Fatalf("outcome = %+v", out)
			}
			if !tc.session && out.SessionID != "" {
				t.Fatalf("2024-11-05 must stay stateless: %+v", out)
			}
		})
	}
}

func TestUnsupportedHeaderVersionFailsClosed(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	_, failure := server.Handle(context.Background(), readerPrincipal(t),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`),
		Options{Version: "1999-01-01"})
	if failure == nil || failure.Status != http.StatusBadRequest || failure.Code != CodeInvalidRequest {
		t.Fatalf("failure = %+v", failure)
	}
}

func TestStatefulRevisionRequiresSession(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	principal := readerPrincipal(t)
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + Version20250618 + `"}}`
	out, failure := server.Handle(context.Background(), principal, []byte(initBody), Options{})
	if failure != nil {
		t.Fatal(failure)
	}
	ping := `{"jsonrpc":"2.0","id":2,"method":"ping"}`

	// The header already announces a stateful revision, so a missing session
	// is rejected instead of served stateless.
	_, failure = server.Handle(context.Background(), principal, []byte(ping), Options{Version: Version20250618})
	if failure == nil || failure.Status != http.StatusNotFound || failure.Code != CodeSession {
		t.Fatalf("missing session = %+v", failure)
	}
	_, failure = server.Handle(context.Background(), principal, []byte(ping), Options{SessionID: "unknown"})
	if failure == nil || failure.Status != http.StatusNotFound {
		t.Fatalf("unknown session = %+v", failure)
	}
	if _, failure := server.Handle(context.Background(), principal, []byte(ping), Options{SessionID: out.SessionID}); failure != nil {
		t.Fatalf("valid session rejected: %+v", failure)
	}
	if !server.DeleteSession(out.SessionID) {
		t.Fatal("session must be deletable")
	}
	if _, failure := server.Handle(context.Background(), principal, []byte(ping), Options{SessionID: out.SessionID}); failure == nil {
		t.Fatal("deleted session must be rejected")
	}
}

func TestStatelessTransportSkipsSessions(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	principal := readerPrincipal(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + Version20250618 + `"}}`
	out, failure := server.Handle(context.Background(), principal, []byte(body), Options{Stateless: true})
	if failure != nil {
		t.Fatal(failure)
	}
	if out.SessionID != "" {
		t.Fatalf("stdio must not issue sessions: %+v", out)
	}
	if _, failure := server.Handle(context.Background(), principal,
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`), Options{Stateless: true}); failure != nil {
		t.Fatalf("stdio call rejected: %+v", failure)
	}
}

func TestBatchAndNotifications(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	principal := readerPrincipal(t)

	out, failure := server.Handle(context.Background(), principal,
		[]byte(`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`), Options{})
	if failure != nil {
		t.Fatal(failure)
	}
	var responses []Response
	if err := json.Unmarshal(out.Payload, &responses); err != nil || len(responses) != 2 {
		t.Fatalf("batch = %s (%v)", out.Payload, err)
	}

	out, failure = server.Handle(context.Background(), principal,
		[]byte(`[{"jsonrpc":"2.0","method":"notifications/initialized"}]`), Options{})
	if failure != nil || out.Payload != nil {
		t.Fatalf("notification batch = %+v, %+v", out, failure)
	}

	out, failure = server.Handle(context.Background(), principal,
		[]byte(`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/cancelled"},7]`), Options{})
	if failure != nil {
		t.Fatal(failure)
	}
	if err := json.Unmarshal(out.Payload, &responses); err != nil || len(responses) != 2 {
		t.Fatalf("mixed batch = %s", out.Payload)
	}
	if responses[1].Error == nil || responses[1].Error.Code != CodeParse {
		t.Fatalf("non-object element = %+v", responses[1])
	}

	for _, body := range []string{`[]`, `[` + strings.Repeat(`{"jsonrpc":"2.0","id":1,"method":"ping"},`, 33) + `{"jsonrpc":"2.0","id":1,"method":"ping"}]`} {
		_, failure = server.Handle(context.Background(), principal, []byte(body), Options{})
		if failure == nil || failure.Status != http.StatusBadRequest {
			t.Fatalf("batch bound = %+v", failure)
		}
	}

	// A notification with no id is acknowledged and carries no body.
	out, failure = server.Handle(context.Background(), principal,
		[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), Options{})
	if failure != nil || out.Payload != nil {
		t.Fatalf("notification = %+v, %+v", out, failure)
	}
}

func TestProtocolErrorsAreStable(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	principal := readerPrincipal(t)

	cases := []struct {
		name   string
		body   string
		status int
		code   int
	}{
		{"empty", ``, http.StatusBadRequest, CodeParse},
		{"malformed", `{`, http.StatusBadRequest, CodeParse},
		{"wrong jsonrpc", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, http.StatusOK, CodeInvalidRequest},
		{"missing method", `{"jsonrpc":"2.0","id":1}`, http.StatusOK, CodeInvalidRequest},
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, http.StatusOK, CodeMethodNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, failure := server.Handle(context.Background(), principal, []byte(tc.body), Options{})
			if failure != nil {
				if failure.Status != tc.status || failure.Code != tc.code {
					t.Fatalf("failure = %+v", failure)
				}
				return
			}
			if tc.status != http.StatusOK {
				t.Fatalf("expected a transport failure, got %s", out.Payload)
			}
			var payload Response
			if err := json.Unmarshal(out.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Error == nil || payload.Error.Code != tc.code {
				t.Fatalf("payload = %+v", payload)
			}
		})
	}
}

func TestCapabilitiesAndPing(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	principal := readerPrincipal(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + Version20250618 + `"}}`
	out, failure := server.Handle(context.Background(), principal, []byte(body), Options{})
	if failure != nil {
		t.Fatal(failure)
	}
	raw := decodeResult(t, out)
	caps, ok := raw["capabilities"].(map[string]any)
	if !ok || caps["tools"] == nil {
		t.Fatalf("capabilities = %v", raw["capabilities"])
	}
	info, ok := raw["serverInfo"].(map[string]any)
	if !ok || info["name"] != "agentmem" {
		t.Fatalf("serverInfo = %v", raw["serverInfo"])
	}

	out, failure = server.Handle(context.Background(), principal,
		[]byte(`{"jsonrpc":"2.0","id":3,"method":"ping"}`), Options{SessionID: out.SessionID})
	if failure != nil {
		t.Fatal(failure)
	}
	if result := decodeResult(t, out); result == nil {
		t.Fatalf("ping = %s", out.Payload)
	}
}
