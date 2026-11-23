package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentmem/internal/meta"
	"agentmem/internal/netsafe"
	"agentmem/internal/okf"
)

// RemoteConfig configures the optional read-only HTTP graph engine.
type RemoteConfig struct {
	Endpoint     string
	Token        string
	Timeout      time.Duration
	AllowedHosts []string
	// Client is injectable for tests; nil builds a hardened client.
	Client *http.Client
}

// Remote is a read-only Query backed by an external graph engine. It never
// writes: the canonical body and the local index remain agentmem's job, so a
// remote outage can never lose knowledge.
//
// The subject is forwarded in a dedicated header, never in the URL or a body:
// it was derived from the bearer token at the handler boundary and must not
// travel through anything that gets logged as a query string.
type Remote struct {
	base   *url.URL
	token  string
	client *http.Client
}

// maxRemoteBody bounds one engine response.
const maxRemoteBody = 8 << 20

// NewRemote fails closed: a missing endpoint, a missing token or a host outside
// AGENTMEM_EGRESS_ALLOWED_HOSTS is a startup error, never a silent fallback.
// parseURL is a seam: netsafe already parsed the endpoint, so this failure is
// only reachable with an injected parser. Production always uses net/url.
var parseURL = url.Parse

func NewRemote(cfg RemoteConfig) (*Remote, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("graph: remote endpoint is empty")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("graph: remote token is empty")
	}
	if err := netsafe.ValidateEndpoint(endpoint, cfg.AllowedHosts); err != nil {
		return nil, fmt.Errorf("graph: remote endpoint rejected: %w", err)
	}
	base, err := parseURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("graph: remote endpoint is invalid")
	}
	client := cfg.Client
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		client = &http.Client{
			Timeout:       timeout,
			CheckRedirect: netsafe.RedirectPolicy(cfg.AllowedHosts, 5),
		}
	}
	return &Remote{base: base, token: cfg.Token, client: client}, nil
}

func (r *Remote) endpoint(path string, q url.Values) string {
	u := *r.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = q.Encode()
	return u.String()
}

// call performs one request. Errors are redacted at this edge: callers only
// learn the operation and the status class, never the URL, query or body.
func (r *Remote) call(ctx context.Context, subject, method, path string, q url.Values, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%w: encode remote request: %v", ErrUnhealthy, err)
		}
		reader = bytes.NewReader(data)
	}
	target := r.endpoint(path, q)
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return fmt.Errorf("%w: build remote request", ErrUnhealthy)
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "application/json")
	if subject != "" {
		req.Header.Set("X-Agentmem-Subject", subject)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: remote graph engine unreachable", ErrUnhealthy)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Drain a bounded amount so the connection can be reused without ever
		// surfacing engine text to the client.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("%w: remote graph engine returned %d", ErrUnhealthy, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRemoteBody)).Decode(out); err != nil {
		return fmt.Errorf("%w: remote graph response is not valid JSON", ErrUnhealthy)
	}
	return nil
}

func (r *Remote) Node(ctx context.Context, subject, conceptID string) (NodeView, error) {
	var out NodeView
	err := r.call(ctx, subject, http.MethodGet, "/v1/graph/node", url.Values{"id": {conceptID}}, nil, &out)
	return out, err
}

func (r *Remote) Neighbors(ctx context.Context, subject string, q WalkQuery) (SubgraphView, error) {
	values := url.Values{
		"from":  {q.From},
		"depth": {fmt.Sprint(q.Depth)}, "direction": {q.Direction},
		"limit": {fmt.Sprint(q.Limit)},
	}
	if len(q.EdgeTypes) > 0 {
		values.Set("edge_types", strings.Join(q.EdgeTypes, ","))
	}
	if len(q.NodeTypes) > 0 {
		values.Set("node_types", strings.Join(q.NodeTypes, ","))
	}
	var out SubgraphView
	err := r.call(ctx, subject, http.MethodGet, "/v1/graph/neighbors", values, nil, &out)
	return out, err
}

func (r *Remote) Path(ctx context.Context, subject string, q PathQuery) (PathView, error) {
	values := url.Values{
		"from": {q.From}, "to": {q.To},
		"max_depth": {fmt.Sprint(q.MaxDepth)}, "direction": {q.Direction},
	}
	if len(q.EdgeTypes) > 0 {
		values.Set("edge_types", strings.Join(q.EdgeTypes, ","))
	}
	var out PathView
	err := r.call(ctx, subject, http.MethodGet, "/v1/graph/path", values, nil, &out)
	return out, err
}

func (r *Remote) Search(ctx context.Context, subject string, q SearchQuery) ([]NodeHit, error) {
	values := url.Values{"q": {q.Text}, "limit": {fmt.Sprint(q.Limit)}}
	if len(q.NodeTypes) > 0 {
		values.Set("node_types", strings.Join(q.NodeTypes, ","))
	}
	var out []NodeHit
	err := r.call(ctx, subject, http.MethodGet, "/v1/graph/search", values, nil, &out)
	if out == nil {
		out = []NodeHit{}
	}
	return out, err
}

func (r *Remote) Answerability(ctx context.Context, subject, question string, minimum float64) (Coverage, error) {
	payload := map[string]any{"question": question, "minimum": minimum}
	var out Coverage
	err := r.call(ctx, subject, http.MethodPost, "/v1/graph/answerability", nil, payload, &out)
	return out, err
}

func (r *Remote) Stats(ctx context.Context, subject string) (meta.GraphStats, error) {
	var out meta.GraphStats
	err := r.call(ctx, subject, http.MethodGet, "/v1/graph/stats", nil, nil, &out)
	return out, err
}

type remoteSchema struct {
	NodeTypes map[string]struct {
		Description string `json:"description"`
	} `json:"node_types"`
	EdgeTypes map[string]struct {
		From        []string `json:"from"`
		To          []string `json:"to"`
		Description string   `json:"description"`
	} `json:"edge_types"`
}

func (r *Remote) Schema(ctx context.Context) (okf.GraphSchema, []string, error) {
	var out remoteSchema
	if err := r.call(ctx, "", http.MethodGet, "/v1/graph/schema", nil, nil, &out); err != nil {
		return okf.GraphSchema{}, nil, err
	}
	schema := okf.GraphSchema{NodeTypes: map[string]okf.NodeType{}, EdgeTypes: map[string]okf.EdgeType{}}
	for name, nt := range out.NodeTypes {
		schema.NodeTypes[name] = okf.NodeType{Description: nt.Description}
	}
	for name, et := range out.EdgeTypes {
		schema.EdgeTypes[name] = okf.EdgeType{From: et.From, To: et.To, Description: et.Description}
	}
	return schema, nil, nil
}
