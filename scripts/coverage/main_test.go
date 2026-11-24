package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const passProfile = `mode: atomic
agentmem/internal/a/a.go:1.1,2.2 3 1
`

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// stubPackages replaces the go-list seam for the duration of a test.
func stubPackages(t *testing.T, packages []string, err error) {
	t.Helper()
	previous := listPackages
	listPackages = func() ([]string, error) { return packages, err }
	t.Cleanup(func() { listPackages = previous })
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write refused") }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read refused") }

func TestRunPassesAtThreshold(t *testing.T) {
	stubPackages(t, []string{"agentmem/internal/a"}, nil)
	profile := writeTemp(t, "cover.out", passProfile)
	var out, errOut strings.Builder
	if code := run([]string{"-profile", profile}, &out, &errOut); code != exitPass {
		t.Fatalf("code = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "100.0%") || !strings.Contains(out.String(), "total") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestRunFailsBelowThreshold(t *testing.T) {
	stubPackages(t, []string{"agentmem/internal/a"}, nil)
	profile := writeTemp(t, "cover.out", "mode: atomic\nagentmem/internal/a/a.go:1.1,2.2 3 0\nagentmem/internal/a/a.go:3.3,4.4 3 1\n")
	var out, errOut strings.Builder
	if code := run([]string{"-profile", profile}, &out, &errOut); code != exitFail {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "FAIL agentmem/internal/a: 50.0% < 100.0%") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestRunReportsRootEnvironment(t *testing.T) {
	stubPackages(t, []string{"agentmem/internal/a"}, nil)
	profile := writeTemp(t, "cover.out", "mode: atomic\nagentmem/internal/a/a.go:1.1,2.2 1 0\nagentmem/internal/a/a.go:3.3,4.4 1 1\n")
	previous := geteuid
	t.Cleanup(func() { geteuid = previous })

	t.Run("root", func(t *testing.T) {
		geteuid = func() int { return 0 }
		var errOut strings.Builder
		if code := run([]string{"-profile", profile}, io.Discard, &errOut); code != exitFail {
			t.Fatalf("code = %d", code)
		}
		if !strings.Contains(errOut.String(), "running as root") ||
			!strings.Contains(errOut.String(), "below-threshold package under root is unexpected") {
			t.Fatalf("stderr = %s", errOut.String())
		}
	})
	t.Run("non-root", func(t *testing.T) {
		geteuid = func() int { return 1000 }
		var errOut strings.Builder
		if code := run([]string{"-profile", profile}, io.Discard, &errOut); code != exitFail {
			t.Fatalf("code = %d", code)
		}
		if strings.Contains(errOut.String(), "running as root") {
			t.Fatalf("non-root stderr = %s", errOut.String())
		}
	})
}

func TestRunHonoursCustomMinimum(t *testing.T) {
	stubPackages(t, []string{"agentmem/internal/a"}, nil)
	profile := writeTemp(t, "cover.out", "mode: atomic\nagentmem/internal/a/a.go:1.1,2.2 1 1\nagentmem/internal/a/a.go:3.3,4.4 1 0\n")
	var out, errOut strings.Builder
	if code := run([]string{"-profile", profile, "-min", "50"}, &out, &errOut); code != exitPass {
		t.Fatalf("code = %d, output = %s", code, out.String())
	}
}

func TestRunFailsOnMissingPackageData(t *testing.T) {
	stubPackages(t, []string{"agentmem/internal/a", "agentmem/internal/b"}, nil)
	profile := writeTemp(t, "cover.out", passProfile)
	var out strings.Builder
	code := run([]string{"-profile", profile}, &out, io.Discard)
	if code != exitFail {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "agentmem/internal/b: no coverage data (no tests)") {
		t.Fatalf("output = %s", out.String())
	}
	if !strings.Contains(out.String(), "n/a") {
		t.Fatalf("a package without data must not print a percentage: %s", out.String())
	}
}

func TestRunRejectsUnusableInput(t *testing.T) {
	stubPackages(t, []string{"agentmem/internal/a"}, nil)
	cases := []struct {
		name    string
		profile string
		want    string
	}{
		{"empty", "mode: atomic\n", "coverage profile is empty"},
		{"outside", "mode: atomic\nagentmem/other/x.go:1.1,2.2 2 1\n", "coverage entry outside the package set"},
		{"malformed", "mode: atomic\nnot a profile line\n", "malformed coverage line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := writeTemp(t, "cover.out", tc.profile)
			var errOut strings.Builder
			if code := run([]string{"-profile", profile}, io.Discard, &errOut); code != exitError {
				t.Fatalf("code = %d", code)
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("stderr = %s", errOut.String())
			}
		})
	}
}

func TestRunReportsEnvironmentFailures(t *testing.T) {
	t.Run("flag", func(t *testing.T) {
		if code := run([]string{"-nope"}, io.Discard, io.Discard); code != exitError {
			t.Fatalf("code = %d", code)
		}
	})
	t.Run("missing profile", func(t *testing.T) {
		stubPackages(t, []string{"agentmem/internal/a"}, nil)
		var errOut strings.Builder
		if code := run([]string{"-profile", filepath.Join(t.TempDir(), "nope.out")}, io.Discard, &errOut); code != exitError {
			t.Fatalf("code = %d", code)
		}
		if !strings.Contains(errOut.String(), "no such file") {
			t.Fatalf("stderr = %s", errOut.String())
		}
	})
	t.Run("list error", func(t *testing.T) {
		stubPackages(t, nil, errors.New("go missing"))
		var errOut strings.Builder
		if code := run(nil, io.Discard, &errOut); code != exitError {
			t.Fatalf("code = %d", code)
		}
		if !strings.Contains(errOut.String(), "go missing") {
			t.Fatalf("stderr = %s", errOut.String())
		}
	})
	t.Run("write error", func(t *testing.T) {
		stubPackages(t, []string{"agentmem/internal/a"}, nil)
		profile := writeTemp(t, "cover.out", passProfile)
		var errOut strings.Builder
		if code := run([]string{"-profile", profile}, failingWriter{}, &errOut); code != exitError {
			t.Fatalf("code = %d", code)
		}
		if !strings.Contains(errOut.String(), "write refused") {
			t.Fatalf("stderr = %s", errOut.String())
		}
	})
}

func TestRunHonoursExclusions(t *testing.T) {
	stubPackages(t, []string{"agentmem/internal/a", "agentmem/internal/b"}, nil)
	profile := writeTemp(t, "cover.out", passProfile)
	exclude := writeTemp(t, "exclude.txt", "# documented exceptions\nagentmem/internal/b: needs a live service\n")
	var out strings.Builder
	if code := run([]string{"-profile", profile, "-exclude", exclude}, &out, io.Discard); code != exitPass {
		t.Fatalf("code = %d, output = %s", code, out.String())
	}
	if !strings.Contains(out.String(), "agentmem/internal/b (excluded)") || !strings.Contains(out.String(), "excluded: 1 package(s)") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestRunReportsExclusionProblems(t *testing.T) {
	stubPackages(t, []string{"agentmem/internal/a"}, nil)
	profile := writeTemp(t, "cover.out", passProfile)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"stale", "agentmem/internal/missing: no longer needed\n", "stale exclusion: agentmem/internal/missing"},
		{"no reason", "agentmem/internal/a:\n", "must be '<package-prefix>:<reason>'"},
		{"no prefix", ":reason\n", "must be '<package-prefix>:<reason>'"},
		{"no entries", "# nothing here\n", "exclusion file has no entries"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exclude := writeTemp(t, "exclude.txt", tc.body)
			var errOut strings.Builder
			if code := run([]string{"-profile", profile, "-exclude", exclude}, io.Discard, &errOut); code != exitError {
				t.Fatalf("code = %d", code)
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("stderr = %s", errOut.String())
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		var errOut strings.Builder
		code := run([]string{"-profile", profile, "-exclude", filepath.Join(t.TempDir(), "nope.txt")}, io.Discard, &errOut)
		if code != exitError || !strings.Contains(errOut.String(), "read exclusions") {
			t.Fatalf("code = %d, stderr = %s", code, errOut.String())
		}
	})
}

func TestAnalyzeAggregatesAndValidates(t *testing.T) {
	if _, err := analyze(strings.NewReader(""), nil, nil, 100); err == nil {
		t.Fatal("an empty package list must be rejected")
	}
	if _, err := analyze(failingReader{}, []string{"agentmem/internal/a"}, nil, 100); err == nil {
		t.Fatal("a profile read error must surface")
	}

	profile := "mode: atomic\n" +
		"agentmem/internal/a/a.go:1.1,2.2 2 1\n" +
		"agentmem/internal/a/a.go:3.3,4.4 3 0\n"
	result, err := analyze(strings.NewReader(profile), []string{"agentmem/internal/a"}, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.aggHit != 2 || result.aggTotal != 5 {
		t.Fatalf("aggregate = %d/%d", result.aggHit, result.aggTotal)
	}
	if len(result.problems) != 1 {
		t.Fatalf("problems = %v", result.problems)
	}
}

func TestAnalyzeMatchesNestedPackages(t *testing.T) {
	packages := []string{"agentmem/internal/plugins", "agentmem/internal/plugins/graphrag"}
	profile := "mode: atomic\nagentmem/internal/plugins/graphrag/x.go:1.1,2.2 4 1\nagentmem/internal/plugins/y.go:1.1,2.2 2 1\n"
	result, err := analyze(strings.NewReader(profile), packages, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	byPkg := map[string]row{}
	for _, r := range result.rows {
		byPkg[r.pkg] = r
	}
	if byPkg["agentmem/internal/plugins/graphrag"].total != 4 || byPkg["agentmem/internal/plugins"].total != 2 {
		t.Fatalf("rows = %+v", result.rows)
	}
}

func TestParseLine(t *testing.T) {
	name, statements, count, err := parseLine("agentmem/internal/a/a.go:1.1,2.2 3 2")
	if err != nil || name != "agentmem/internal/a/a.go" || statements != 3 || count != 2 {
		t.Fatalf("parse = %q %d %d %v", name, statements, count, err)
	}
	// -covermode=atomic emits zero-statement blocks at boundary positions.
	if _, statements, _, err := parseLine("agentmem/a.go:3.3,3.3 0 9"); err != nil || statements != 0 {
		t.Fatalf("zero-statement block = %d, %v", statements, err)
	}
	for _, line := range []string{
		"a b",
		"a b c d",
		"no-colon 1 1",
		":1.1,2.2 1 1",
		"agentmem/a.go:1.1,2.2 zero 1",
		"agentmem/a.go:1.1,2.2 -1 1",
		"agentmem/a.go:1.1,2.2 1 many",
		"agentmem/a.go:1.1,2.2 1 -1",
	} {
		if _, _, _, err := parseLine(line); err == nil {
			t.Fatalf("%q must be rejected", line)
		}
	}
}

func TestMatchPackageAndExclusions(t *testing.T) {
	ordered := []string{"agentmem/internal/plugins/graphrag", "agentmem/internal/plugins"}
	if got := matchPackage(ordered, "agentmem/internal/plugins/graphrag/x.go"); got != ordered[0] {
		t.Fatalf("nested match = %q", got)
	}
	if got := matchPackage(ordered, "agentmem/internal/plugins/y.go"); got != ordered[1] {
		t.Fatalf("parent match = %q", got)
	}
	if got := matchPackage(ordered, "agentmem/other/x.go"); got != "" {
		t.Fatalf("unexpected match = %q", got)
	}
	if got := matchPackage([]string{"agentmem/internal/a"}, "agentmem/internal/a"); got != "agentmem/internal/a" {
		t.Fatalf("exact match = %q", got)
	}

	excludes := []exclusion{{prefix: "agentmem/internal/a", reason: "reason"}}
	if got := excludedReason("agentmem/internal/a", excludes); got != "reason" {
		t.Fatalf("exact exclusion = %q", got)
	}
	if got := excludedReason("agentmem/internal/a/sub", excludes); got != "reason" {
		t.Fatalf("nested exclusion = %q", got)
	}
	if got := excludedReason("agentmem/internal/b", excludes); got != "" {
		t.Fatalf("unexpected exclusion = %q", got)
	}
}

func TestBelow(t *testing.T) {
	if !below(0, 0, 100) {
		t.Fatal("a package without data is below any threshold")
	}
	if below(3, 3, 100) {
		t.Fatal("100% must pass")
	}
	if !below(2, 3, 100) {
		t.Fatal("66% must fail a 100% gate")
	}
	if below(1, 2, 50) {
		t.Fatal("50% must pass a 50% gate")
	}
}

func TestGoListPackages(t *testing.T) {
	previous := goCommand
	t.Cleanup(func() { goCommand = previous })

	goCommand = func(...string) ([]byte, error) {
		return []byte("agentmem/internal/a\n\nagentmem/internal/b\n"), nil
	}
	packages, err := goListPackages()
	if err != nil || len(packages) != 2 || packages[1] != "agentmem/internal/b" {
		t.Fatalf("packages = %v, %v", packages, err)
	}

	goCommand = func(...string) ([]byte, error) { return nil, errors.New("go unavailable") }
	if _, err := goListPackages(); err == nil || !strings.Contains(err.Error(), "go unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func TestSeamDefaultsReachTheRealProcess(t *testing.T) {
	// The seams must default to the real implementation, never to a no-op that
	// would let a broken gate pass.
	if _, err := goCommand("version"); err != nil {
		t.Fatalf("default goCommand: %v", err)
	}
	if len(os.Args) == 0 {
		t.Fatal("os.Args is unexpectedly empty")
	}
	if args := osArgs(); len(args) != len(os.Args)-1 {
		t.Fatalf("default osArgs = %v", args)
	}
	if euid := geteuid(); euid < 0 {
		t.Fatalf("default geteuid = %d", euid)
	}
}

func TestMainSeam(t *testing.T) {
	stubPackages(t, []string{"agentmem/internal/a"}, nil)
	profile := writeTemp(t, "cover.out", passProfile)

	previousExit, previousArgs := osExit, osArgs
	previousOut, previousErr := stdoutWriter, stderrWriter
	t.Cleanup(func() {
		osExit, osArgs = previousExit, previousArgs
		stdoutWriter, stderrWriter = previousOut, previousErr
	})

	var stdout strings.Builder
	exitCode := -1
	osExit = func(code int) { exitCode = code }
	osArgs = func() []string { return []string{"-profile", profile} }
	stdoutWriter = &stdout
	stderrWriter = io.Discard

	main()
	if exitCode != exitPass {
		t.Fatalf("exit = %d", exitCode)
	}
	if !strings.Contains(stdout.String(), "100.0%") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}
