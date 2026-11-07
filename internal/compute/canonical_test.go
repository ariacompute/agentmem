package compute

import "testing"

func TestCanonicalSQLNormalisesCommentsCaseAndWhitespace(t *testing.T) {
	a := "select  sum(amount)\nfrom orders\nwhere year = $1  and status = 'booked';"
	b := "-- auditor note\nSELECT SUM( amount ) FROM orders WHERE year = $1 AND status = 'booked'"
	if CanonicalSQL(a) != CanonicalSQL(b) {
		t.Fatalf("canonical mismatch:\n%q\n%q", CanonicalSQL(a), CanonicalSQL(b))
	}
	if CanonicalSQL(a) != CanonicalSQL(a) {
		t.Fatal("canonicalisation is not deterministic")
	}
}

func TestCanonicalSQLPreservesPlaceholderOrder(t *testing.T) {
	if CanonicalSQL("SELECT a FROM t WHERE x = $1 AND y = $2") ==
		CanonicalSQL("SELECT a FROM t WHERE x = $2 AND y = $1") {
		t.Fatal("reordered positional placeholders must not compare equal")
	}
}

func TestCanonicalSQLNormalisesNamedPlaceholders(t *testing.T) {
	if CanonicalSQL("SELECT * FROM t WHERE y = :year") != CanonicalSQL("SELECT * FROM t WHERE y = ?") {
		t.Fatalf("named placeholder not normalised: %q", CanonicalSQL("SELECT * FROM t WHERE y = :year"))
	}
}

func TestCanonicalSQLKeepsStringLiterals(t *testing.T) {
	if CanonicalSQL("SELECT 'a  b' FROM t") == CanonicalSQL("SELECT 'a b' FROM t") {
		t.Fatal("string literal content must not be whitespace-collapsed")
	}
}
