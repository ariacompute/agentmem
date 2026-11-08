// Package pluginbase implements the shared Adapter + Lifecycle behaviour for
// engine plugins. It composes the generic HTTP/CLI bridges with a validated
// manifest, so each engine package only owns its descriptor.
package pluginbase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"agentmem/internal/adapter"
	"agentmem/internal/bridges"
	"agentmem/internal/config"
	"agentmem/internal/okf"
	"agentmem/internal/plugins/manifest"
)

// State is the observable lifecycle state of a plugin.
type State string

const (
	StateNew     State = "new"
	StateRunning State = "running"
	StateClosed  State = "closed"
)

// ErrClosed is returned after a plugin has been closed.
var ErrClosed = errors.New("plugins: adapter is closed")

// Plugin is the engine-level adapter. It implements adapter.Adapter and the
// optional adapter.Lifecycle port.
type Plugin struct {
	desc    manifest.Descriptor
	inner   adapter.Adapter
	mode    string
	offline bool

	mu    sync.RWMutex
	state State
}

// New builds a plugin from its descriptor and the configuration entry.
func New(desc manifest.Descriptor, cfg config.AdapterConfig) (*Plugin, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = desc.Name
	}
	timeout := cfg.TimeoutMS
	if timeout <= 0 {
		timeout = desc.TimeoutMS
	}
	ac := config.AdapterConfig{
		Name: name, Kind: desc.Kind, Enabled: true,
		Endpoint: cfg.Endpoint, TimeoutMS: timeout,
		EgressHosts: cfg.EgressHosts,
	}
	spec := bridges.EngineSpec{
		Name: desc.Name, Kind: desc.Bridge, Endpoint: cfg.Endpoint,
		TimeoutMS: desc.TimeoutMS, OfflineMock: desc.OfflineMock,
		Notes: desc.SdkBoundary.WireProtocol, Capabilities: desc.Capabilities,
	}
	var inner adapter.Adapter
	switch desc.Bridge {
	case "http":
		b, err := bridges.NewHTTPBridge(ac, spec)
		if err != nil {
			return nil, err
		}
		inner = b
	case "cli":
		b, err := bridges.NewCLIBridge(ac, spec)
		if err != nil {
			return nil, err
		}
		inner = b
	default:
		return nil, fmt.Errorf("plugins: unsupported bridge %q", desc.Bridge)
	}
	return &Plugin{
		desc: desc, inner: inner, state: StateNew,
		mode:    bridgeMode(cfg.Endpoint, desc.OfflineMock),
		offline: strings.TrimSpace(cfg.Endpoint) == "",
	}, nil
}

func bridgeMode(endpoint string, offlineMock bool) string {
	if strings.TrimSpace(endpoint) == "" && offlineMock {
		return "offline-mock"
	}
	return "sidecar"
}

func (p *Plugin) Name() string { return p.inner.Name() }

func (p *Plugin) Capabilities() adapter.Capabilities { return p.desc.Capabilities }

// Fingerprint binds a projection to the manifest version as well as the
// instance name, so a protocol change forces migration rebuilds.
func (p *Plugin) Fingerprint() string {
	return p.desc.Fingerprint + ":" + p.Name()
}

// Status exposes lifecycle and SDK-boundary state without leaking bodies or
// credentials.
func (p *Plugin) Status() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return map[string]string{
		"engine":      p.desc.Engine,
		"version":     p.desc.Version,
		"state":       string(p.state),
		"mode":        p.mode,
		"protocol":    p.desc.ProtocolVersion,
		"sidecar":     p.desc.SdkBoundary.Sidecar,
		"fingerprint": p.Fingerprint(),
	}
}

// State reports the current lifecycle state.
func (p *Plugin) State() State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

// Descriptor returns the immutable manifest descriptor.
func (p *Plugin) Descriptor() manifest.Descriptor { return p.desc }

// Start verifies the channel before it is declared running. A configured
// sidecar that cannot be reached fails closed instead of silently degrading.
func (p *Plugin) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == StateClosed {
		return ErrClosed
	}
	if p.state == StateRunning {
		return nil
	}
	if err := p.inner.Health(ctx); err != nil {
		return fmt.Errorf("plugins: start %s: %w", p.Name(), err)
	}
	p.state = StateRunning
	return nil
}

// Close is idempotent and never touches the backend twice.
func (p *Plugin) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == StateClosed {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.state = StateClosed
	return nil
}

func (p *Plugin) Health(ctx context.Context) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.state == StateClosed {
		return ErrClosed
	}
	return p.inner.Health(ctx)
}

func (p *Plugin) Put(ctx context.Context, conceptID string, d *okf.Document) error {
	if err := p.usable(); err != nil {
		return err
	}
	return p.inner.Put(ctx, conceptID, d)
}

func (p *Plugin) Delete(ctx context.Context, conceptID string) error {
	if err := p.usable(); err != nil {
		return err
	}
	return p.inner.Delete(ctx, conceptID)
}

func (p *Plugin) Query(ctx context.Context, q adapter.Query) ([]adapter.Hit, error) {
	if err := p.usable(); err != nil {
		return nil, err
	}
	hits, err := p.inner.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	generic := p.Name() + ":/"
	for i := range hits {
		hits[i].Source = p.Name()
		fromFallback := p.offline || strings.HasPrefix(hits[i].Citation, "memory:/")
		genericCitation := generic + hits[i].ConceptID
		if hits[i].Citation == "" || hits[i].Citation == genericCitation || fromFallback {
			hits[i].Citation = p.desc.CitationScheme + hits[i].ConceptID
		}
	}
	return hits, nil
}

func (p *Plugin) usable() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.state == StateClosed {
		return ErrClosed
	}
	return nil
}
