package plugins

import "testing"

func TestRegisterAllNilRegistry(t *testing.T) {
	if err := RegisterAll(nil); err == nil {
		t.Fatal("a nil registry must be rejected")
	}
}
