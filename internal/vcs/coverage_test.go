package vcs

import "testing"

func TestSortStringsCoversSwap(t *testing.T) {
	in := []string{"c", "a", "b"}
	sortStrings(in)
	want := []string{"a", "b", "c"}
	for i := range want {
		if in[i] != want[i] {
			t.Fatalf("sortStrings = %v", in)
		}
	}
	// Already-sorted and single-element inputs exercise the other branches.
	single := []string{"only"}
	sortStrings(single)
	if single[0] != "only" {
		t.Fatal("single element changed")
	}
}
