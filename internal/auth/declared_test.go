package auth

import "testing"

func TestDeclaredPrincipal(t *testing.T) {
	p, err := DeclaredPrincipal("alice", []Permission{PermKnowledgeRead, PermGraphWrite})
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject() != "alice" || !p.Allows(PermGraphWrite) || p.Allows(PermKnowledgeWrite) {
		t.Fatalf("principal = %+v", p)
	}

	cases := []struct {
		name    string
		subject string
		scopes  []Permission
	}{
		{"empty subject", "  ", []Permission{PermKnowledgeRead}},
		{"no scopes", "alice", nil},
		{"unknown scope", "alice", []Permission{"root:all"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DeclaredPrincipal(tc.subject, tc.scopes); err == nil {
				t.Fatal("must fail closed")
			}
		})
	}
}
