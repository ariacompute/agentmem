package plugins

import (
	"errors"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/plugins/manifest"
)

func withEntries(t *testing.T, list []Entry) {
	t.Helper()
	previous := entries
	entries = func() []Entry { return list }
	t.Cleanup(func() { entries = previous })
}

func TestRegisterAllPropagatesRegisterError(t *testing.T) {
	withEntries(t, []Entry{{
		Name: "boom",
		Register: func(*adapter.Registry) error {
			return errors.New("registration refused")
		},
	}})
	if err := RegisterAll(adapter.NewRegistry()); err == nil {
		t.Fatal("a failing plugin registration must be reported")
	}
}

func TestRegisterAllSkipsRegisteredKinds(t *testing.T) {
	r := adapter.NewRegistry()
	if err := RegisterAll(r); err != nil {
		t.Fatal(err)
	}
	// Second call must be a no-op, not a duplicate-kind error.
	if err := RegisterAll(r); err != nil {
		t.Fatalf("registering twice must be idempotent: %v", err)
	}
}

func TestDescriptorsPropagatesDescriptorError(t *testing.T) {
	withEntries(t, []Entry{{
		Name: "broken",
		Descriptor: func() (manifest.Descriptor, error) {
			return manifest.Descriptor{}, errors.New("unreadable manifest")
		},
	}})
	if _, err := Descriptors(); err == nil {
		t.Fatal("a failing descriptor must be reported")
	}
}

func TestDescriptorsRejectsInconsistentDescriptor(t *testing.T) {
	withEntries(t, []Entry{{
		Name: "expected",
		Descriptor: func() (manifest.Descriptor, error) {
			return manifest.Descriptor{Name: "other", Kind: "other"}, nil
		},
	}})
	if _, err := Descriptors(); err == nil {
		t.Fatal("a descriptor whose name differs from its entry must be rejected")
	}
}

func TestDescriptorsRejectsDuplicateDescriptor(t *testing.T) {
	dup := func() (manifest.Descriptor, error) {
		return manifest.Descriptor{Name: "dup", Kind: "dup"}, nil
	}
	withEntries(t, []Entry{
		{Name: "dup", Descriptor: dup},
		{Name: "dup", Descriptor: dup},
	})
	if _, err := Descriptors(); err == nil {
		t.Fatal("a duplicate descriptor must be rejected")
	}
}
