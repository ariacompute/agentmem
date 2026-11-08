package main

import (
	"testing"
)

func TestRunSuccessAndError(t *testing.T) {
	// --help returns nil (success path through main.run).
	if err := run([]string{"--help"}, nil); err != nil {
		t.Fatalf("--help should succeed: %v", err)
	}

	// serve with an invalid config (System One endpoint but no egress
	// allowlist) fails validation, so run must return an error.
	t.Setenv("AGENTMEM_ROUTER_SYSTEMONE_ENDPOINT", "http://unreachable.example")
	t.Setenv("AGENTMEM_EGRESS_ALLOWED_HOSTS", "")
	if err := run([]string{"serve"}, nil); err == nil {
		t.Fatal("serve with bad config must fail")
	}

	// An unknown argument is rejected by the CLI.
	if err := run([]string{"definitely-not-a-command"}, nil); err == nil {
		t.Fatal("unknown command must fail")
	}
}
