package adapter

import (
	"context"
	"errors"
	"testing"

	"agentmem/internal/okf"
)

type lifecycleProbe struct {
	name   string
	events *[]string
	start  error
	close  error
}

func (p *lifecycleProbe) startMarker() string { return "start:" + p.name }
func (p *lifecycleProbe) closeMarker() string { return "close:" + p.name }
func (p *lifecycleProbe) Name() string        { return p.name }
func (p *lifecycleProbe) Capabilities() Capabilities {
	return Capabilities{Query: true}
}
func (p *lifecycleProbe) Fingerprint() string { return "probe/" + p.name }
func (p *lifecycleProbe) Health(context.Context) error {
	return nil
}
func (p *lifecycleProbe) Put(context.Context, string, *okf.Document) error {
	return ErrUnsupported
}
func (p *lifecycleProbe) Query(context.Context, Query) ([]Hit, error) {
	return []Hit{}, nil
}
func (p *lifecycleProbe) Delete(context.Context, string) error { return ErrUnsupported }
func (p *lifecycleProbe) Start(context.Context) error {
	*p.events = append(*p.events, p.startMarker())
	return p.start
}
func (p *lifecycleProbe) Close(context.Context) error {
	*p.events = append(*p.events, p.closeMarker())
	return p.close
}

func TestRegistryStartAndCloseLifecycle(t *testing.T) {
	var events []string
	r := NewRegistry()
	for _, name := range []string{"a", "b"} {
		if err := r.RegisterAdapter(&lifecycleProbe{name: name, events: &events}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := r.StartAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"start:a", "start:b", "close:b", "close:a"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestRegistryStartAllFailsClosed(t *testing.T) {
	var events []string
	boom := errors.New("sidecar unavailable")
	r := NewRegistry()
	_ = r.RegisterAdapter(&lifecycleProbe{name: "a", events: &events})
	_ = r.RegisterAdapter(&lifecycleProbe{name: "b", events: &events, start: boom})
	if err := r.StartAll(context.Background()); err == nil {
		t.Fatal("unhealthy plugin must fail assembly")
	}
	if len(events) != 2 || events[1] != "start:b" {
		t.Fatalf("start aborted at the wrong point: %v", events)
	}
}

func TestRegistryCloseAggregatesErrors(t *testing.T) {
	var events []string
	boom := errors.New("close failed")
	r := NewRegistry()
	_ = r.RegisterAdapter(&lifecycleProbe{name: "a", events: &events, close: boom})
	_ = r.RegisterAdapter(&lifecycleProbe{name: "b", events: &events})
	if err := r.Close(context.Background()); err == nil {
		t.Fatal("close error must be surfaced")
	}
	if len(events) != 2 || events[0] != "close:b" || events[1] != "close:a" {
		t.Fatalf("all plugins must be closed even if one fails: %v", events)
	}
}
