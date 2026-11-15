package main

import (
	"bytes"
	"os"
	"testing"
)

func TestRunSuccessAndError(t *testing.T) {
	// --help returns nil (success path through main.run).
	if err := run([]string{"--help"}, &bytes.Buffer{}); err != nil {
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

func TestRunCLIExitCodes(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runCLI([]string{"--help"}, &out, &errOut); code != 0 {
		t.Fatalf("help exit code = %d", code)
	}
	if code := runCLI([]string{"definitely-not-a-command"}, &out, &errOut); code != 1 {
		t.Fatalf("unknown command exit code = %d", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("a failed command must report on stderr")
	}
}

func TestMainUsesConfiguredExit(t *testing.T) {
	var got int
	originalExit := exit
	exit = func(code int) { got = code }
	t.Cleanup(func() { exit = originalExit })

	originalArgs := os.Args
	os.Args = []string{"agentmem", "--help"}
	t.Cleanup(func() { os.Args = originalArgs })

	main()
	if got != 0 {
		t.Fatalf("help exit code = %d", got)
	}

	os.Args = []string{"agentmem", "definitely-not-a-command"}
	main()
	if got != 1 {
		t.Fatalf("unknown command exit code = %d", got)
	}
}
