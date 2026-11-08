package adapter

import (
	"context"
	"errors"
	"testing"
)

func TestHashingEmbedderDimension(t *testing.T) {
	if NewHashingEmbedder(64).Dimension() != 64 {
		t.Fatal("dimension must match")
	}
	if NewHashingEmbedder(-1).Dimension() != 64 {
		t.Fatal("negative dim must default")
	}
}

func TestUnsupportedErrorIs(t *testing.T) {
	if !errors.Is(ErrUnsupported, ErrUnsupported) {
		t.Fatal("self is unsupported")
	}
	if errors.Is(context.Canceled, ErrUnsupported) {
		t.Fatal("unrelated error must not match")
	}
}

func TestRegistryHasKind(t *testing.T) {
	r := NewRegistry()
	if !r.HasKind("memory") {
		t.Fatal("memory must be registered")
	}
	if r.HasKind("does-not-exist") {
		t.Fatal("unknown kind must report false")
	}
}

func TestSortHitsOrdersByScore(t *testing.T) {
	h := []Hit{
		{Score: 0.1, ConceptID: "low"},
		{Score: 0.9, ConceptID: "high"},
		{Score: 0.5, ConceptID: "mid"},
	}
	sortHits(h)
	if h[0].ConceptID != "high" || h[2].ConceptID != "low" {
		t.Fatalf("order = %+v", h)
	}
}
