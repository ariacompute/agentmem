package pluginbase

import (
	"context"
	"errors"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
)

func TestNewHTTPBridgeRejectsUnsafeEndpoint(t *testing.T) {
	desc := testDesc()
	desc.Bridge = "http"
	if _, err := New(desc, config.AdapterConfig{Name: "stub", Endpoint: "ftp://engine.invalid"}); err == nil {
		t.Fatal("a non-http endpoint must fail plugin construction")
	}
}

func TestNewCLIBridgeRejectsUnsafeCommand(t *testing.T) {
	desc := testDesc()
	desc.Bridge = "cli"
	if _, err := New(desc, config.AdapterConfig{Name: "stub", Endpoint: "not a command"}); err == nil {
		t.Fatal("a command containing whitespace must fail plugin construction")
	}
}

// failingInner fails queries so Plugin can propagate the bridge error.
type failingInner struct{ err error }

func (f *failingInner) Name() string                       { return "failing" }
func (f *failingInner) Capabilities() adapter.Capabilities { return adapter.Capabilities{Query: true} }
func (f *failingInner) Fingerprint() string                { return "failing/v1" }
func (f *failingInner) Health(context.Context) error       { return nil }
func (f *failingInner) Put(context.Context, string, *okf.Document) error {
	return f.err
}
func (f *failingInner) Query(context.Context, adapter.Query) ([]adapter.Hit, error) {
	return nil, f.err
}
func (f *failingInner) Delete(context.Context, string) error { return f.err }

func TestPluginQueryPropagatesInnerError(t *testing.T) {
	p := &Plugin{desc: testDesc(), inner: &failingInner{err: errors.New("bridge down")}, state: StateNew}
	if _, err := p.Query(context.Background(), adapter.Query{Text: "q"}); err == nil {
		t.Fatal("an inner query failure must be propagated")
	}
}
