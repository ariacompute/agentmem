package adapter

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

func fixtureDoc(title, body string) *okf.Document {
	return &okf.Document{Type: "Concept", Title: title, Description: title + " definition", Body: body}
}

// contractSuite runs the same assertions against every channel implementation.
func contractSuite(t *testing.T, makeAdapter func(t *testing.T) Adapter, canWrite bool) {
	t.Helper()
	ctx := context.Background()
	a := makeAdapter(t)
	if a.Name() == "" || a.Fingerprint() == "" {
		t.Fatal("adapter must have a name and fingerprint")
	}
	if err := a.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	caps := a.Capabilities()
	if !caps.Query {
		t.Fatal("every adapter must support Query")
	}
	if canWrite {
		if !caps.Put {
			t.Fatal("writeable adapter must declare Put")
		}
		if err := a.Put(ctx, "tables/orders", fixtureDoc("Orders", "one row per order")); err != nil {
			t.Fatalf("put: %v", err)
		}
		if err := a.Put(ctx, "metrics/churn", fixtureDoc("Churn", "monthly churn rate")); err != nil {
			t.Fatalf("put: %v", err)
		}
		hits, err := a.Query(ctx, Query{Text: "orders", TopK: 5})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(hits) == 0 {
			t.Fatal("expected at least one hit")
		}
		if hits[0].Source == "" || hits[0].Citation == "" {
			t.Fatalf("hit lacks provenance: %+v", hits[0])
		}
		if err := a.Delete(ctx, "tables/orders"); err != nil && !errors.Is(err, ErrUnsupported) {
			t.Fatalf("delete: %v", err)
		}
		hits, err = a.Query(ctx, Query{Text: "", TopK: 1})
		if err != nil {
			t.Fatalf("topK query: %v", err)
		}
		if len(hits) > 1 {
			t.Fatalf("topK not honoured: %d hits", len(hits))
		}
	} else {
		if err := a.Put(ctx, "x", fixtureDoc("x", "x")); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("canonical Put must be unsupported, got %v", err)
		}
		if err := a.Delete(ctx, "x"); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("canonical Delete must be unsupported, got %v", err)
		}
	}
}

func builtinAdapters(t *testing.T) map[string]func(*testing.T) Adapter {
	t.Helper()
	emb := NewHashingEmbedder(64)
	bundle, err := okf.Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	bundle.LogWrites = false
	if _, err := bundle.Put("tables/orders", fixtureDoc("Orders", "one row per order")); err != nil {
		t.Fatal(err)
	}
	return map[string]func(*testing.T) Adapter{
		"okf": func(t *testing.T) Adapter { return NewOKFAdapter(bundle) },
		"memory": func(t *testing.T) Adapter {
			return NewMemoryAdapter(emb)
		},
		"pgvector": func(t *testing.T) Adapter {
			a, err := NewPGVectorAdapter(NewRecordingVectorDB(meta.NewFakeStore()), emb)
			if err != nil {
				t.Fatal(err)
			}
			return a
		},
	}
}

func TestAdapterContract(t *testing.T) {
	for name, make := range builtinAdapters(t) {
		t.Run(name, func(t *testing.T) {
			contractSuite(t, make, name != "okf")
		})
	}
}

func TestPGVectorRecordingBoundary(t *testing.T) {
	ctx := context.Background()
	rec := NewRecordingVectorDB(meta.NewFakeStore())
	a, err := NewPGVectorAdapter(rec, NewHashingEmbedder(32))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, "c1", fixtureDoc("Alpha", "alpha body")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Query(ctx, Query{Text: "alpha", TopK: 3}); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	calls := rec.Calls()
	if len(calls) != 3 || calls[0].Method != "PutVector" || calls[1].Method != "QueryVector" || calls[2].Method != "DeleteVector" {
		t.Fatalf("call sequence = %+v", calls)
	}
	if calls[0].Dimensions != 32 || calls[1].TopK != 3 {
		t.Fatalf("vector boundary wrong: %+v", calls)
	}
}

func TestEmbedderDeterministicAndIdentityDrift(t *testing.T) {
	e := NewHashingEmbedder(16)
	ctx := context.Background()
	a, _ := e.Embed(ctx, "hello world")
	b, _ := e.Embed(ctx, "hello world")
	if len(a) != 16 || a[0] != b[0] && a[1] != b[1] {
		t.Fatal("embedding not deterministic")
	}
	fp1 := FingerprintFor("memory", e)
	e2 := NewHashingEmbedder(32)
	if fp1 == FingerprintFor("memory", e2) {
		t.Fatal("embedding identity change must change fingerprint")
	}
}

func TestCanonicalAdapterIsDeterministic(t *testing.T) {
	bundle, _ := okf.Open(filepath.Join(t.TempDir(), "bundle"))
	bundle.LogWrites = false
	_, _ = bundle.Put("metrics/churn", fixtureDoc("Churn", "definition"))
	a := NewOKFAdapter(bundle)
	h1, _ := a.Query(context.Background(), Query{Text: "churn"})
	h2, _ := a.Query(context.Background(), Query{Text: "churn"})
	if len(h1) != 1 || len(h2) != 1 || h1[0].ConceptID != h2[0].ConceptID || h1[0].Score != h2[0].Score {
		t.Fatalf("canonical lookup not deterministic: %+v %+v", h1, h2)
	}
	if h1[0].Citation != "okf:/metrics/churn.md" {
		t.Fatalf("citation = %q", h1[0].Citation)
	}
}
