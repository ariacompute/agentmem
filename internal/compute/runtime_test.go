package compute

import (
	"context"
	"errors"
	"testing"
)

func TestRuntimeRegistryCoversAllFiveContracts(t *testing.T) {
	r := NewRuntimeRegistry(FakeExecutor{}, 100)
	names := r.Names()
	if len(names) != len(RuntimeNames) {
		t.Fatalf("names = %v", names)
	}
	for i, n := range names {
		if n != RuntimeNames[i] {
			t.Fatalf("names = %v, want %v", names, RuntimeNames)
		}
	}
	if err := r.Register(&unsupportedRuntime{name: "postgres"}); err == nil {
		t.Fatal("duplicate runtime must be rejected")
	}
}

func TestOnlyPostgresExecutes(t *testing.T) {
	b, _ := validPair(t)
	r := NewRuntimeRegistry(FakeExecutor{}, 100)
	for _, name := range RuntimeNames {
		rt, ok := r.Get(name)
		if !ok {
			t.Fatalf("runtime %q missing", name)
		}
		receipt, err := rt.Execute(context.Background(), b)
		if name == "postgres" {
			if err != nil || receipt.JobID == "" || receipt.Result == "" {
				t.Fatalf("postgres run: %v / %+v", err, receipt)
			}
			continue
		}
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s: got %v, want ErrUnsupported", name, err)
		}
		if receipt.Result != "" || receipt.DisplayValue != "" {
			t.Fatalf("%s returned evidence without execution: %+v", name, receipt)
		}
	}
}

func TestExecutorFailureIsTyped(t *testing.T) {
	b, _ := validPair(t)
	rt := &postgresRuntime{exec: FailingExecutor{}, maxRows: 10}
	if _, err := rt.Execute(context.Background(), b); !errors.Is(err, ErrExecution) {
		t.Fatalf("got %v, want ErrExecution", err)
	}
	if _, err := (&postgresRuntime{}).Execute(context.Background(), b); !errors.Is(err, ErrExecution) {
		t.Fatalf("nil executor: got %v, want ErrExecution", err)
	}
}
