package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func systemOneTask(text string) DecisionTask {
	return DecisionTask{
		Kind: "read", Text: text,
		Features: Features{QueryKind: "general", ByteSize: len(text), Authoritative: true},
		Eligible: []string{"okf", "memory"},
	}
}

func configFor(url string) SystemOneConfig {
	c := DefaultSystemOneConfig()
	c.Endpoint = url
	return c
}

func TestSystemOneRequestShape(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/systemone" || r.Method != http.MethodPost {
			t.Errorf("bad request %s %s", r.Method, r.URL.Path)
		}
		var req systemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		q, ok := req.Questions["route"]
		if !ok || q.Type != "choice" || q.Criteria["okf"] == "" || q.Criteria["memory"] == "" {
			t.Errorf("bad questions: %+v", req.Questions)
		}
		if !strings.Contains(req.State, "authoritative=true") {
			t.Errorf("state missing features: %q", req.State)
		}
		if strings.Contains(req.State, "SECRET_BODY_TEXT") {
			t.Error("request leaked document body")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{"route": map[string]any{
				"choice": "memory", "confidence": 0.91, "reason": "long-tail wins",
			}},
		})
	}))
	defer srv.Close()

	d, err := NewSystemOneDecisioner(configFor(srv.URL)).Decide(context.Background(), systemOneTask("find orders"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != SourceSystemOne || len(d.Targets) != 1 || d.Targets[0] != "memory" || d.Confidence != 0.91 {
		t.Fatalf("decision = %+v", d)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestSystemOneCacheContract(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"answers":{"route":{"choice":"okf","confidence":0.9}}}`))
	}))
	defer srv.Close()

	s := NewSystemOneDecisioner(configFor(srv.URL))
	task := systemOneTask("repeated query")
	first, err := s.Decide(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Decide(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || s.CacheSize() != 1 {
		t.Fatalf("cache miss: calls=%d size=%d", calls.Load(), s.CacheSize())
	}
	// AFM-D numeric contract: cache hits report 0.5 and a cache: reason.
	if second.Source != SourceCache || second.Confidence != 0.5 || !strings.HasPrefix(second.Reason, "cache:") {
		t.Fatalf("cache hit decision = %+v", second)
	}
	if first.Source != SourceSystemOne {
		t.Fatalf("first decision = %+v", first)
	}
	// Cache key includes normalized text and the eligible set.
	if _, err := s.Decide(context.Background(), systemOneTask("repeated   QUERY")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("normalized text should hit cache, calls=%d", calls.Load())
	}
}

func TestSystemOneLowConfidenceFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"route":{"choice":"memory","confidence":0.2}}}`))
	}))
	defer srv.Close()
	c := configFor(srv.URL)
	c.Fallback = CanonicalTarget
	d, err := NewSystemOneDecisioner(c).Decide(context.Background(), systemOneTask("uncertain"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != SourceFallback || d.RuleID != "systemone_min_confidence" ||
		len(d.Targets) != 1 || d.Targets[0] != CanonicalTarget {
		t.Fatalf("low-score protection = %+v", d)
	}
}

func TestSystemOneInvalidChoiceFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"route":{"choice":"unknown-channel","confidence":0.99}}}`))
	}))
	defer srv.Close()
	composite := NewComposite(NewRuleEngine(), NewSystemOneDecisioner(configFor(srv.URL)))
	d, err := composite.Decide(context.Background(), systemOneTask("ambiguous question"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != SourceFallback || len(d.Targets) != 1 || d.Targets[0] != CanonicalTarget ||
		!strings.HasPrefix(d.Reason, "fail-closed") {
		t.Fatalf("invalid choice must fail closed: %+v", d)
	}
}

func TestSystemOneTimeoutNeverUsesFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		_, _ = w.Write([]byte(`{"answers":{"route":{"choice":"memory","confidence":0.99}}}`))
	}))
	defer srv.Close()
	c := configFor(srv.URL)
	c.TimeoutMS = 20
	composite := NewComposite(NewRuleEngine(), NewSystemOneDecisioner(c))
	start := time.Now()
	d, err := composite.Decide(context.Background(), systemOneTask("slow ambiguous query"))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout not enforced")
	}
	if len(d.Targets) != 1 || d.Targets[0] != CanonicalTarget || !strings.Contains(d.Reason, "fail-closed") {
		t.Fatalf("timeout must fail closed: %+v", d)
	}
}

func TestSystemOneCacheCapacity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choice":"okf","confidence":1.0}`))
	}))
	defer srv.Close()
	c := configFor(srv.URL)
	c.CacheCapacity = 2
	s := NewSystemOneDecisioner(c)
	for _, text := range []string{"one", "two", "three"} {
		if _, err := s.Decide(context.Background(), systemOneTask(text)); err != nil {
			t.Fatal(err)
		}
	}
	if s.CacheSize() != 2 {
		t.Fatalf("cache cap not enforced: %d", s.CacheSize())
	}
}

func TestSystemOneOfflineStubIsDeterministic(t *testing.T) {
	s := NewSystemOneDecisioner(DefaultSystemOneConfig())
	a, err := s.Decide(context.Background(), systemOneTask("offline"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Decide(context.Background(), systemOneTask("offline"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Source != SourceSystemOne || a.Targets[0] != b.Targets[0] {
		t.Fatalf("stub not deterministic: %+v %+v", a, b)
	}
}

func TestDefaultSystemOneNumbersMatchAFMD(t *testing.T) {
	c := DefaultSystemOneConfig()
	if c.TimeoutMS != 5000 || c.CacheCapacity != 256 || c.MaxChoices != 255 || c.MinConfidence != 0.5 {
		t.Fatalf("contract drift: %+v", c)
	}
}
