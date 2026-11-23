package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestServerAccessorsAndErrorStrings(t *testing.T) {
	server, _ := newTestServer(t, Config{Write: true})
	if !server.WriteEnabled() || server.Sessions() == nil {
		t.Fatal("accessors must reflect the configuration")
	}
	if (&ToolError{Message: "tool"}).Error() != "tool" {
		t.Fatal("ToolError message")
	}
	if (&domainError{msg: "domain"}).Error() != "domain" {
		t.Fatal("domainError message")
	}
	if (&Failure{Message: "failure"}).Error() != "failure" {
		t.Fatal("Failure message")
	}
}

func TestRequestWithoutIDGetsNull(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	out, failure := server.Handle(context.Background(), readerPrincipal(t),
		[]byte(`{"jsonrpc":"2.0","method":"ping"}`), Options{})
	if failure != nil {
		t.Fatal(failure)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if id, ok := payload["id"]; !ok || id != nil {
		t.Fatalf("id = %v", payload["id"])
	}
}

func TestInitializeToleratesNonObjectParams(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	out, failure := server.Handle(context.Background(), readerPrincipal(t),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":"nope"}`), Options{})
	if failure != nil {
		t.Fatal(failure)
	}
	if result := decodeResult(t, out); result["protocolVersion"] != LatestVersion() {
		t.Fatalf("version = %v", result["protocolVersion"])
	}
}

func TestInitializeCapacityIsFixedError(t *testing.T) {
	server, _ := newTestServer(t, Config{MaxSessions: 1})
	principal := readerPrincipal(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + Version20250618 + `"}}`
	if _, failure := server.Handle(context.Background(), principal, []byte(body), Options{}); failure != nil {
		t.Fatal(failure)
	}
	out, failure := server.Handle(context.Background(), principal, []byte(body), Options{})
	if failure != nil {
		t.Fatal(failure)
	}
	var payload Response
	if err := json.Unmarshal(out.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error == nil || payload.Error.Code != CodeSession {
		t.Fatalf("capacity = %+v", payload)
	}
}

func TestSessionIDEntropyFailureFailsClosed(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	server.sessions.newID = func() (string, error) { return "", errors.New("no entropy") }
	out, failure := server.Handle(context.Background(), readerPrincipal(t),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+Version20250618+`"}}`), Options{})
	if failure != nil {
		t.Fatal(failure)
	}
	var payload Response
	if err := json.Unmarshal(out.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error == nil || payload.Error.Code != CodeSession {
		t.Fatalf("entropy failure = %+v", payload)
	}
}

func TestDepthDefaultClampsToConfiguredMaximum(t *testing.T) {
	server, _ := newTestServer(t, Config{MaxDepth: 1})
	response := callTool(t, server, readerPrincipal(t), "path", `{"from":"a","to":"b"}`)
	if response.Error != nil {
		t.Fatalf("path = %+v", response.Error)
	}
	response = callTool(t, server, readerPrincipal(t), "neighbors", `{"concept_id":"a"}`)
	if response.Error != nil {
		t.Fatalf("neighbors = %+v", response.Error)
	}
}

func TestNullArgumentsDecodeAsEmpty(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_node","arguments":null}}`
	out, failure := server.Handle(context.Background(), readerPrincipal(t), []byte(body), Options{})
	if failure != nil {
		t.Fatal(failure)
	}
	var response Response
	if err := json.Unmarshal(out.Payload, &response); err != nil {
		t.Fatal(err)
	}
	// Null decodes as an empty object: the tool validates its own required
	// field instead of reporting a decode failure.
	if response.Error == nil || response.Error.Code != CodeInvalidParams || response.Error.Message != "concept_id is required" {
		t.Fatalf("null arguments = %+v", response.Error)
	}
}
