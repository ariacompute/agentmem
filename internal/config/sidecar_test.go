package config

import (
	"errors"
	"strings"
	"testing"
)

func TestComputeSidecarsLoadStrictly(t *testing.T) {
	p := writeEnv(t, strings.Join([]string{
		"AGENTMEM_COMPUTE_SIDECARS=" + `'[{"runtime":"python","bridge":"cli","command":"agentmem-py","timeout_ms":8000}]'`,
		"",
	}, "\n"))
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Compute.Sidecars) != 1 || cfg.Compute.Sidecars[0].Runtime != "python" {
		t.Fatalf("sidecars = %+v", cfg.Compute.Sidecars)
	}
	if err := Default().Validate(); err != nil {
		t.Fatalf("empty sidecar list must stay valid: %v", err)
	}
}

func TestComputeSidecarsRejectUnknownFields(t *testing.T) {
	p := writeEnv(t, "AGENTMEM_COMPUTE_SIDECARS="+`'[{"runtime":"python","bridge":"cli","command":"py","nope":1}]'`+"\n")
	if _, err := Load(p); err == nil {
		t.Fatal("unknown sidecar fields must fail closed")
	}
}

func TestComputeSidecarsRequireAllowlistForHTTP(t *testing.T) {
	c := Default()
	c.Compute.Sidecars = []ComputeSidecarConfig{
		{Runtime: "bigquery", Bridge: "http", Endpoint: "https://bq.internal"},
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "EGRESS_ALLOWED_HOSTS") {
		t.Fatalf("http sidecar without an allowlist must fail, got %v", err)
	}
	c.Egress.AllowedHosts = []string{"bq.internal"}
	if err := c.Validate(); err != nil {
		t.Fatalf("allowlisted http sidecar must pass: %v", err)
	}
	// A CLI sidecar is an executable name, not an outbound URL.
	c = Default()
	c.Compute.Sidecars = []ComputeSidecarConfig{{Runtime: "python", Bridge: "cli", Command: "agentmem-py"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("cli sidecar must not require an allowlist: %v", err)
	}
}

func TestComputeSidecarValidation(t *testing.T) {
	cases := map[string]ComputeSidecarConfig{
		"postgres is in-process": {Runtime: "postgres", Bridge: "http", Endpoint: "https://x"},
		"unknown runtime":        {Runtime: "spark", Bridge: "cli", Command: "spark"},
		"unknown bridge":         {Runtime: "python", Bridge: "grpc", Command: "py"},
		"http without endpoint":  {Runtime: "bigquery", Bridge: "http"},
		"cli without command":    {Runtime: "python", Bridge: "cli"},
		"cli with a path":        {Runtime: "python", Bridge: "cli", Command: "/usr/bin/python"},
		"cli with shell text":    {Runtime: "python", Bridge: "cli", Command: "python -c 1"},
		"http with a command":    {Runtime: "bigquery", Bridge: "http", Endpoint: "https://x", Command: "py"},
		"negative timeout":       {Runtime: "python", Bridge: "cli", Command: "py", TimeoutMS: -1},
	}
	for name, sc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			cfg.Egress.AllowedHosts = []string{"x"}
			cfg.Compute.Sidecars = []ComputeSidecarConfig{sc}
			if err := cfg.Validate(); err == nil {
				t.Fatalf("sidecar %+v must be rejected", sc)
			}
		})
	}
	cfg := Default()
	cfg.Compute.Sidecars = []ComputeSidecarConfig{
		{Runtime: "python", Bridge: "cli", Command: "py"},
		{Runtime: "python", Bridge: "cli", Command: "py2"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("duplicate runtime must be rejected")
	}
	cfg = Default()
	cfg.Compute.Sidecars = []ComputeSidecarConfig{{Runtime: "postgres", Bridge: "http", Endpoint: "https://x"}}
	cfg.Egress.AllowedHosts = []string{"x"}
	if err := cfg.Validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("postgres sidecar must report ErrUnsupported, got %v", err)
	}
}
