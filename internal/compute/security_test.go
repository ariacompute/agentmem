package compute

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

func TestSafeStatementRejectsDangerousFunctions(t *testing.T) {
	for _, sql := range []string{
		"SELECT pg_read_file('/etc/passwd')",
		"SELECT pg_ls_dir('/')",
		"SELECT dblink('host=1', 'SELECT 1')",
		"SELECT set_config('search_path', 'evil', false)",
		"SELECT pg_sleep(10)",
		"SELECT lo_export(1, '/tmp/x')",
		"SELECT pg_read_binary_file('/etc/passwd')",
		"WITH x AS (SELECT pg_stat_file('/etc/passwd')) SELECT * FROM x",
	} {
		if _, err := SafeStatement(sql); err == nil {
			t.Fatalf("dangerous SQL must be rejected: %s", sql)
		}
	}
	if _, err := SafeStatement("SELECT SUM(amount) FROM orders WHERE year = :year"); err != nil {
		t.Fatalf("a sanctioned read-only statement must remain valid: %v", err)
	}
}

func TestBindStatementSkipsBlockAndLineComments(t *testing.T) {
	sql := "SELECT :year /* :not_a_parameter is documentation */ FROM orders -- :also_documentation\n"
	got, err := bindStatement(sql, []okf.Parameter{{Name: "year"}})
	if err != nil {
		t.Fatalf("placeholders inside comments must be ignored: %v", err)
	}
	if !strings.Contains(got, "$1") {
		t.Fatalf("declared placeholder was not bound: %s", got)
	}
}

func TestCanonicalSQLLargeInputIsNotCached(t *testing.T) {
	canonicalCache.Lock()
	before := len(canonicalCache.values)
	canonicalCache.Unlock()

	big := "SELECT " + strings.Repeat("x ", maxCanonicalSQLSize)
	if got := CanonicalSQL(big); got == "" {
		t.Fatal("oversized SQL must still be normalised")
	}
	canonicalCache.Lock()
	after := len(canonicalCache.values)
	canonicalCache.Unlock()
	if after > before {
		t.Fatalf("oversized SQL polluted the cache: %d -> %d", before, after)
	}
}

// failingAudit replaces only the audit write while inheriting every other
// MetaStore method from the offline fake.
type failingAudit struct {
	*meta.FakeStore
	driverErr error
}

func (f failingAudit) AppendComputation(context.Context, meta.ComputationRecord) error {
	return f.driverErr
}

func TestAuditFailureDoesNotLeakDriverError(t *testing.T) {
	svc := &Service{
		Audit: failingAudit{
			FakeStore: meta.NewFakeStore(),
			driverErr: errors.New("postgres://agentmem:s3cr3t@db:5432/agentmem password=s3cr3t"),
		},
		Timeout: time.Second,
	}
	_, err := svc.Run(context.Background(), Request{ConceptID: "missing/contract"})
	if err == nil {
		t.Fatal("an audit write failure must be visible to the caller")
	}
	for _, secret := range []string{"s3cr3t", "postgres://", "password="} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("audit failure leaked %q: %s", secret, err)
		}
	}
	if !errors.Is(err, ErrExecution) {
		t.Fatalf("expected a typed execution error, got %v", err)
	}
}
