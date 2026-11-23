package graph

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

func TestEdgeTargetRejectsInvalidIDs(t *testing.T) {
	cases := map[string]bool{
		"metrics/revenue":        true,
		"metrics/revenue.md":     true,
		"references/revenue.sql": false,
		"../escape":              false,
		"":                       false,
	}
	for raw, want := range cases {
		clean, ok := edgeTarget(raw)
		if ok != want {
			t.Fatalf("edgeTarget(%q) = %q, %v", raw, clean, ok)
		}
		if ok && clean == "" {
			t.Fatalf("edgeTarget(%q) returned an empty id", raw)
		}
	}
}

func TestNewRemoteRejectsAnUnparsableEndpoint(t *testing.T) {
	previous := parseURL
	parseURL = func(string) (*url.URL, error) { return nil, errors.New("unparsable") }
	t.Cleanup(func() { parseURL = previous })

	_, err := NewRemote(RemoteConfig{
		Endpoint:     "https://example.test",
		Token:        "token",
		AllowedHosts: []string{"example.test"},
	})
	if err == nil {
		t.Fatal("an unparsable endpoint must be refused")
	}
}

func TestWriterReportsAConverterFailure(t *testing.T) {
	previous := nodeFrom
	nodeFrom = func(string, *okf.Document, string, string) (meta.GraphNodeRecord, []meta.GraphEdgeRecord, []string, error) {
		return meta.GraphNodeRecord{}, nil, nil, errors.New("convert refused")
	}
	t.Cleanup(func() { nodeFrom = previous })

	store := &docsStub{
		get: func(context.Context, string, string) (*okf.Document, error) {
			return nil, errors.New("missing")
		},
	}
	writer := writerWith(store, &bodyStub{}, &metaStub{}, &indexStub{})
	if _, _, err := writer.PutNode(context.Background(), "alice", NodeInput{ConceptID: "metrics/a"}); err == nil {
		t.Fatal("a converter failure must surface")
	}
}
