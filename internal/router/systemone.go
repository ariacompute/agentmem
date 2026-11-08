package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"agentmem/internal/netsafe"
)

// Numeric contracts mirror router/afm_d to avoid drift.
const (
	DefaultTimeoutMS     = 5000
	DefaultCacheCapacity = 256
	DefaultMaxChoices    = 255
	DefaultMinConfidence = 0.5
	CacheHitConfidence   = 0.5
	DefaultConfidence    = 1.0
)

// SystemOneConfig is the AFM-D System One choice service configuration.
type SystemOneConfig struct {
	Endpoint      string
	TimeoutMS     int
	CacheCapacity int
	MaxChoices    int
	MinConfidence float64
	Fallback      string
	// AllowedHosts is the closed outbound allowlist used to re-check every
	// redirect hop. Fail-closed behaviour is unconditional and has no switch.
	AllowedHosts []string
}

func DefaultSystemOneConfig() SystemOneConfig {
	return SystemOneConfig{
		TimeoutMS: DefaultTimeoutMS, CacheCapacity: DefaultCacheCapacity,
		MaxChoices: DefaultMaxChoices, MinConfidence: DefaultMinConfidence,
		Fallback: CanonicalTarget,
	}
}

type systemOneQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type systemOneRequest struct {
	State     string                       `json:"state"`
	Questions map[string]systemOneQuestion `json:"questions"`
}

type answerPayload struct {
	Choice     string   `json:"choice"`
	Confidence *float64 `json:"confidence"`
	Reason     string   `json:"reason"`
}

type systemOneResponse struct {
	Answers    map[string]answerPayload `json:"answers"`
	Choice     string                   `json:"choice"`
	Confidence *float64                 `json:"confidence"`
	Reason     string                   `json:"reason"`
}

// SystemOneDecisioner calls POST {endpoint}/v1/systemone for ambiguous tasks.
// Deterministic rules always run before it (see Composite), so it can never
// over-promote a probabilistic channel over an authoritative one.
type SystemOneDecisioner struct {
	cfg    SystemOneConfig
	client *http.Client
	hosts  []string
	stub   *StubDecisioner

	mu    sync.Mutex
	cache map[string]RouteDecision
	order []string
}

func NewSystemOneDecisioner(cfg SystemOneConfig) *SystemOneDecisioner {
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = DefaultTimeoutMS
	}
	if cfg.CacheCapacity <= 0 {
		cfg.CacheCapacity = DefaultCacheCapacity
	}
	if cfg.MaxChoices <= 0 {
		cfg.MaxChoices = DefaultMaxChoices
	}
	if cfg.MinConfidence <= 0 {
		cfg.MinConfidence = DefaultMinConfidence
	}
	if cfg.Fallback == "" {
		cfg.Fallback = CanonicalTarget
	}
	hosts := cfg.AllowedHosts
	if len(hosts) == 0 && strings.TrimSpace(cfg.Endpoint) != "" {
		// Standalone construction (tests, CLI helpers) pins the client to the
		// explicitly configured host. The service assembly always injects the
		// global allowlist and validates the endpoint before startup.
		if u, err := url.Parse(cfg.Endpoint); err == nil && u.Hostname() != "" {
			hosts = []string{u.Hostname()}
		}
	}
	return &SystemOneDecisioner{
		cfg:   cfg,
		hosts: hosts,
		client: &http.Client{
			Timeout:       time.Duration(cfg.TimeoutMS) * time.Millisecond,
			CheckRedirect: netsafe.RedirectPolicy(hosts, netsafe.DefaultMaxHops),
		},
		stub:  NewStubDecisioner(),
		cache: map[string]RouteDecision{},
	}
}

func (s *SystemOneDecisioner) Decide(ctx context.Context, task DecisionTask) (RouteDecision, error) {
	eligible := append([]string(nil), task.Eligible...)
	sort.Strings(eligible)
	if len(eligible) > s.cfg.MaxChoices {
		eligible = eligible[:s.cfg.MaxChoices]
	}
	if len(eligible) == 0 {
		return RouteDecision{}, ErrNoEligibleTargets
	}
	key := cacheKey(task, eligible)
	if d, ok := s.cacheGet(key); ok {
		return d, nil
	}
	if s.cfg.Endpoint == "" {
		d, err := s.stub.Decide(ctx, task)
		if err == nil {
			s.cachePut(key, d)
		}
		return d, err
	}
	d, err := s.call(ctx, task, eligible)
	if err != nil {
		// Timeout and transport errors never use the configured fallback:
		// the composite layer fails closed to the canonical body instead.
		return RouteDecision{}, err
	}
	s.cachePut(key, d)
	return d, nil
}

func (s *SystemOneDecisioner) call(ctx context.Context, task DecisionTask, eligible []string) (RouteDecision, error) {
	// Re-check the endpoint on the way out: assembly validates it once, but a
	// standalone constructor must not be able to reach an unlisted host.
	if err := netsafe.ValidateEndpoint(s.cfg.Endpoint, s.hosts); err != nil {
		return RouteDecision{}, fmt.Errorf("systemone: %w", err)
	}
	payload, err := json.Marshal(s.buildBody(task, eligible))
	if err != nil {
		return RouteDecision{}, err
	}
	url := strings.TrimRight(s.cfg.Endpoint, "/") + "/v1/systemone"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return RouteDecision{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return RouteDecision{}, fmt.Errorf("systemone: request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RouteDecision{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RouteDecision{}, fmt.Errorf("systemone: status %d", resp.StatusCode)
	}
	var parsed systemOneResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return RouteDecision{}, fmt.Errorf("systemone: decode: %w", err)
	}
	choice, confidence, reason := parseSystemOneAnswer(parsed)
	if choice == "" {
		return RouteDecision{}, errors.New("systemone: empty choice")
	}
	if err := validateTarget(choice, eligible); err != nil {
		return RouteDecision{}, fmt.Errorf("systemone: %w", err)
	}
	if confidence < s.cfg.MinConfidence {
		// Low-score protection: use the configured fallback, but only if it is
		// inside the eligible pool. Otherwise fail closed.
		if err := validateTarget(s.cfg.Fallback, eligible); err != nil {
			return RouteDecision{}, fmt.Errorf("systemone: low confidence and %w", err)
		}
		return RouteDecision{
			Targets: []string{s.cfg.Fallback}, Confidence: confidence,
			Reason: "low-confidence protection: " + reason, RuleID: "systemone_min_confidence",
			Source: SourceFallback,
		}, nil
	}
	return RouteDecision{
		Targets: []string{choice}, Confidence: confidence,
		Reason: reason, RuleID: "systemone_choice", Source: SourceSystemOne,
	}, nil
}

func (s *SystemOneDecisioner) buildBody(task DecisionTask, eligible []string) systemOneRequest {
	criteria := make(map[string]string, len(eligible))
	for _, e := range eligible {
		if e == CanonicalTarget {
			criteria[e] = "authoritative, auditable, canonical OKF+Git body"
		} else {
			criteria[e] = "probabilistic long-tail retrieval channel " + e
		}
	}
	state := fmt.Sprintf(
		"kind=%s;query_kind=%s;bytes=%d;authoritative=%t;sensitive=%t;structured=%t;has_canonical=%t",
		task.Kind, task.Features.QueryKind, task.Features.ByteSize,
		task.Features.Authoritative, task.Features.Sensitive,
		task.Features.Structured, task.Features.HasCanonical,
	)
	return systemOneRequest{
		State: state,
		Questions: map[string]systemOneQuestion{
			"route": {
				Type:         "choice",
				Instructions: "Choose exactly one eligible target for this knowledge task. Prefer the canonical body for authoritative facts.",
				Criteria:     criteria,
			},
		},
	}
}

func parseSystemOneAnswer(r systemOneResponse) (string, float64, string) {
	choice, confidence, reason := r.Choice, DefaultConfidence, r.Reason
	if p, ok := r.Answers["route"]; ok {
		if p.Choice != "" {
			choice = p.Choice
		}
		if p.Reason != "" {
			reason = p.Reason
		}
		if p.Confidence != nil {
			confidence = *p.Confidence
		}
	}
	if r.Confidence != nil {
		confidence = *r.Confidence
	}
	if reason == "" {
		reason = "system one choice"
	}
	return choice, confidence, reason
}

func cacheKey(task DecisionTask, eligible []string) string {
	return normalizeText(task.Text) + "\x00" + task.Kind + "\x00" + strings.Join(eligible, "\x00")
}

func normalizeText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func (s *SystemOneDecisioner) cacheGet(key string) (RouteDecision, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.cache[key]
	if !ok {
		return RouteDecision{}, false
	}
	// AFM-D contract: cache hits report 0.5 confidence and a cache: reason.
	return RouteDecision{
		Targets: d.Targets, Confidence: CacheHitConfidence,
		Reason: "cache:" + d.Reason, RuleID: "systemone_cache", Source: SourceCache,
	}, true
}

func (s *SystemOneDecisioner) cachePut(key string, d RouteDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.cache[key]; exists {
		return
	}
	if len(s.order) >= s.cfg.CacheCapacity {
		delete(s.cache, s.order[0])
		s.order = s.order[1:]
	}
	s.cache[key] = d
	s.order = append(s.order, key)
}

// CacheSize is exposed for tests and observability.
func (s *SystemOneDecisioner) CacheSize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cache)
}
