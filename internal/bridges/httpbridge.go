// Package bridges adapts third-party RAG engines to the adapter contract.
//
// Python-ecosystem engines (LlamaIndex/PageIndex/GraphRAG/LightRAG) are reached
// over HTTP or CLI. The wire format below is the agentmem bridge protocol; a
// real engine needs a thin sidecar implementing it. TODO(real): replace with
// each engine's native protocol when credentials/services are available.
package bridges

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
)

type putRequest struct {
	ConceptID   string   `json:"concept_id"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Body        string   `json:"body"`
	Tags        []string `json:"tags,omitempty"`
}

type queryRequest struct {
	Text   string    `json:"text"`
	TopK   int       `json:"top_k"`
	Vector []float32 `json:"vector,omitempty"`
}

type queryResponse struct {
	Hits []adapter.Hit `json:"hits"`
}

// HTTPBridge is a generic JSON-over-HTTP RAG channel.
type HTTPBridge struct {
	name       string
	endpoint   string
	client     *http.Client
	maxRetries int
	offline    adapter.Adapter
	engineCaps adapter.Capabilities
}

func NewHTTPBridge(cfg config.AdapterConfig, spec EngineSpec) (*HTTPBridge, error) {
	if cfg.Name == "" {
		return nil, errors.New("bridges: http adapter requires a name")
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	caps := spec.Capabilities
	caps.Put, caps.Query, caps.Delete = true, true, true
	b := &HTTPBridge{
		name:       cfg.Name,
		endpoint:   strings.TrimRight(cfg.Endpoint, "/"),
		client:     &http.Client{Timeout: timeout},
		maxRetries: 1,
		engineCaps: caps,
	}
	// An empty endpoint selects the deterministic in-process mock. This is
	// explicit (visible via Capabilities/Health), never a silent no-op.
	if b.endpoint == "" {
		b.offline = adapter.NewMemoryAdapter(adapter.NewHashingEmbedder(64))
	}
	return b, nil
}

func (b *HTTPBridge) Name() string                       { return b.name }
func (b *HTTPBridge) Capabilities() adapter.Capabilities { return b.engineCaps }
func (b *HTTPBridge) Fingerprint() string                { return "bridge-http/v1:" + b.name }
func (b *HTTPBridge) Health(ctx context.Context) error {
	if b.offline != nil {
		return b.offline.Health(ctx)
	}
	_, err := b.do(ctx, http.MethodGet, "/healthz", nil)
	return err
}

func (b *HTTPBridge) Put(ctx context.Context, conceptID string, d *okf.Document) error {
	if b.offline != nil {
		return b.offline.Put(ctx, conceptID, d)
	}
	payload := putRequest{
		ConceptID: conceptID, Type: d.Type, Title: d.Title,
		Description: d.Description, Body: d.Body, Tags: d.Tags,
	}
	_, err := b.do(ctx, http.MethodPost, "/put", payload)
	return err
}

func (b *HTTPBridge) Query(ctx context.Context, q adapter.Query) ([]adapter.Hit, error) {
	if b.offline != nil {
		return b.offline.Query(ctx, q)
	}
	payload := queryRequest{Text: q.Text, TopK: q.TopK}
	if len(q.Vector) > 0 {
		payload.Vector = []float32(q.Vector)
	}
	body, err := b.do(ctx, http.MethodPost, "/query", payload)
	if err != nil {
		return nil, err
	}
	var resp queryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("bridges: decode query response: %w", err)
	}
	for i := range resp.Hits {
		if resp.Hits[i].Source == "" {
			resp.Hits[i].Source = b.name
		}
		if resp.Hits[i].Citation == "" {
			resp.Hits[i].Citation = b.name + ":/" + resp.Hits[i].ConceptID
		}
	}
	return resp.Hits, nil
}

func (b *HTTPBridge) Delete(ctx context.Context, conceptID string) error {
	if b.offline != nil {
		return b.offline.Delete(ctx, conceptID)
	}
	_, err := b.do(ctx, http.MethodPost, "/delete", map[string]string{"concept_id": conceptID})
	return err
}

// do performs one request with a bounded retry count. Retry only covers
// transport errors and 5xx; 4xx fails immediately.
func (b *HTTPBridge) do(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	var lastErr error
	for attempt := 0; attempt <= b.maxRetries; attempt++ {
		var reader io.Reader
		if body != nil {
			// Recreate the reader for each attempt.
			if raw, ok := body.(*bytes.Reader); ok {
				_, _ = raw.Seek(0, io.SeekStart)
				reader = raw
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, b.endpoint+path, reader)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := b.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("bridges: %s returned %d", path, resp.StatusCode)
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("bridges: %s returned %d", path, resp.StatusCode)
		}
		return data, nil
	}
	return nil, fmt.Errorf("bridges: %s failed after %d attempts: %w", path, b.maxRetries+1, lastErr)
}
