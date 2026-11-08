package auth

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
)

func testSpecs() []TokenSpec {
	return []TokenSpec{
		{
			TokenSHA256: Digest("reader-token"),
			Subject:     "alice",
			Scopes:      []Permission{PermHealthRead, PermKnowledgeRead, PermAuditRead},
		},
		{
			TokenSHA256: Digest("admin-token"),
			Subject:     "ops",
			Scopes: []Permission{
				PermHealthRead, PermKnowledgeRead, PermKnowledgeWrite,
				PermMigrateRun, PermAuditRead, PermComputeRun,
			},
		},
	}
}

func TestNewAndAuthenticate(t *testing.T) {
	a, err := New(testSpecs(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Enabled() {
		t.Fatal("authenticator must be enabled")
	}
	p, ok := a.Authenticate("reader-token")
	if !ok || p.Subject() != "alice" {
		t.Fatalf("reader = %+v, %v", p, ok)
	}
	if !p.Allows(PermKnowledgeRead) || p.Allows(PermKnowledgeWrite) {
		t.Fatal("reader scopes leaked write permission")
	}
	admin, ok := a.Authenticate("admin-token")
	if !ok || !admin.Allows(PermMigrateRun) || !admin.Allows(PermComputeRun) {
		t.Fatalf("admin scopes = %+v, %v", admin, ok)
	}
	for _, bad := range []string{"", "wrong", "Reader-token"} {
		if _, ok := a.Authenticate(bad); ok {
			t.Fatalf("accepted invalid token %q", bad)
		}
	}
}

func TestDigestIsHexAndStable(t *testing.T) {
	d := Digest("hunter2")
	if len(d) != 64 || d != strings.ToLower(d) {
		t.Fatalf("digest must be lower-case hex: %q", d)
	}
	if _, err := hex.DecodeString(d); err != nil {
		t.Fatalf("digest must be hexadecimal: %v", err)
	}
	if d != Digest("hunter2") {
		t.Fatal("digest is not deterministic")
	}
	if d == Digest("hunter3") {
		t.Fatal("digest collision")
	}
	// Upper-case configuration values are accepted and normalised.
	if _, err := New([]TokenSpec{{TokenSHA256: strings.ToUpper(d), Subject: "x", Scopes: []Permission{PermHealthRead}}}, true); err != nil {
		t.Fatalf("upper-case digest rejected: %v", err)
	}
}

func TestNewRejectsMalformedSpecs(t *testing.T) {
	cases := map[string][]TokenSpec{
		"no tokens when enabled": nil,
		"short digest":           {{TokenSHA256: "abc", Subject: "x", Scopes: []Permission{PermHealthRead}}},
		"non-hex digest":         {{TokenSHA256: strings.Repeat("z", 64), Subject: "x", Scopes: []Permission{PermHealthRead}}},
		"empty subject":          {{TokenSHA256: Digest("t"), Subject: "  ", Scopes: []Permission{PermHealthRead}}},
		"no scopes":              {{TokenSHA256: Digest("t"), Subject: "x"}},
		"unknown scope":          {{TokenSHA256: Digest("t"), Subject: "x", Scopes: []Permission{"root:all"}}},
		"duplicate digest": {
			{TokenSHA256: Digest("t"), Subject: "x", Scopes: []Permission{PermHealthRead}},
			{TokenSHA256: Digest("t"), Subject: "y", Scopes: []Permission{PermHealthRead}},
		},
	}
	for name, specs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(specs, true); err == nil {
				t.Fatal("malformed token spec must fail closed")
			}
		})
	}
}

func TestDisabledAuthenticatorAndLocalPrincipal(t *testing.T) {
	a, err := New(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if a.Enabled() {
		t.Fatal("disabled authenticator must report false")
	}
	if _, ok := a.Authenticate("anything"); ok {
		t.Fatal("disabled authenticator must not authenticate")
	}
	local := LocalPrincipal("system")
	if local.Subject() != "system" || !local.Allows(PermComputeRun) {
		t.Fatalf("local principal = %+v", local)
	}
	if LocalPrincipal("").Subject() != "local" {
		t.Fatal("empty subject must get a stable fallback")
	}
	if (Principal{}).Allows(PermHealthRead) {
		t.Fatal("zero principal must grant nothing")
	}
}

func TestParsePermission(t *testing.T) {
	if _, err := ParsePermission("knowledge:read"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePermission("knowledge:*"); err == nil {
		t.Fatal("unknown permission must fail closed")
	}
}

func TestPrincipalContextRoundTrip(t *testing.T) {
	a, err := New(testSpecs(), true)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := a.Authenticate("admin-token")
	if !ok {
		t.Fatal("admin token rejected")
	}
	ctx := WithPrincipal(context.Background(), p)
	got, ok := PrincipalFrom(ctx)
	if !ok || got.Subject() != "ops" || !got.Allows(PermComputeRun) {
		t.Fatalf("context principal = %+v, %v", got, ok)
	}
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Fatal("context without principal must not leak one")
	}
}
