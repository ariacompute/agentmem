package router

import "testing"

func TestRuleIDsListsAllDefaultRules(t *testing.T) {
	ids := NewRuleEngine().RuleIDs()
	if len(ids) != 7 {
		t.Fatalf("expected 7 default rule ids, got %d: %v", len(ids), ids)
	}
	for _, id := range ids {
		if id == "" {
			t.Fatal("rule id must not be empty")
		}
	}
}
