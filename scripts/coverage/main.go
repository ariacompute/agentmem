// Command coverage enforces per-package statement coverage for the module.
//
// `go test ./...` instruments only the package under test, so a profile entry
// means "this package's own tests executed it". That is the property this gate
// pins: every package must be exercised by tests inside that package, not
// merely reached from another package's tests.
//
// Exit codes: 0 the gate passes, 1 a package is below the threshold or has no
// tests, 2 the invocation or a file is unusable.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	exitPass  = 0
	exitFail  = 1
	exitError = 2
)

// The seams below default to the real process so `go run ./scripts/coverage`
// behaves normally, while tests can drive main() without spawning binaries.
var (
	listPackages = goListPackages
	goCommand    = func(args ...string) ([]byte, error) {
		return exec.Command("go", args...).Output()
	}
	osExit                 = os.Exit
	osArgs                 = func() []string { return os.Args[1:] }
	geteuid                = os.Geteuid
	stdoutWriter io.Writer = os.Stdout
	stderrWriter io.Writer = os.Stderr
)

func main() {
	osExit(run(osArgs(), stdoutWriter, stderrWriter))
}

// goListPackages returns the import path of every package in the module. A
// package missing from the profile is a gate failure, so the list — not the
// profile — defines the expected set.
func goListPackages() ([]string, error) {
	out, err := goCommand("list", "-f", "{{.ImportPath}}", "./...")
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	var packages []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			packages = append(packages, line)
		}
	}
	return packages, nil
}

type exclusion struct {
	prefix string
	reason string
}

type row struct {
	pkg      string
	hit      int
	total    int
	excluded bool
}

type report struct {
	rows     []row
	problems []string
	aggHit   int
	aggTotal int
	excluded int
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("coverage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", "coverage.out", "go test coverage profile")
	min := fs.Float64("min", 100, "minimum statement coverage per package, in percent")
	excludePath := fs.String("exclude", "", "optional exclusions, one '<package-prefix>:<reason>' per line")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	root := geteuid() == 0
	if root {
		fmt.Fprintln(stderr, "coverage: running as root; DAC-dependent assertions use injected seams, so permission skips should not affect this result (see docs/testing.md)")
	}
	excludes, err := readExclusions(*excludePath)
	if err != nil {
		fmt.Fprintf(stderr, "coverage: %v\n", err)
		return exitError
	}
	packages, err := listPackages()
	if err != nil {
		fmt.Fprintf(stderr, "coverage: %v\n", err)
		return exitError
	}
	file, err := os.Open(*profile)
	if err != nil {
		fmt.Fprintf(stderr, "coverage: %v\n", err)
		return exitError
	}
	defer file.Close()

	result, err := analyze(file, packages, excludes, *min)
	if err != nil {
		fmt.Fprintf(stderr, "coverage: %v\n", err)
		return exitError
	}
	if err := result.write(stdout); err != nil {
		fmt.Fprintf(stderr, "coverage: %v\n", err)
		return exitError
	}
	if len(result.problems) > 0 {
		if root {
			fmt.Fprintln(stderr, "coverage: a below-threshold package under root is unexpected; inspect the uncovered statements in the profile before attributing it to missing tests")
		}
		return exitFail
	}
	return exitPass
}

// readExclusions parses an optional exclusion file. Every entry must carry a
// reason: an exclusion without one is rejected, and a file with no entries is a
// mistake rather than a quiet no-op.
func readExclusions(name string) ([]exclusion, error) {
	if name == "" {
		return nil, nil
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read exclusions: %w", err)
	}
	var out []exclusion
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.LastIndex(line, ":")
		if idx <= 0 || strings.TrimSpace(line[idx+1:]) == "" {
			return nil, fmt.Errorf("exclusion %d must be '<package-prefix>:<reason>': %q", i+1, line)
		}
		out = append(out, exclusion{
			prefix: strings.TrimSpace(line[:idx]),
			reason: strings.TrimSpace(line[idx+1:]),
		})
	}
	if len(out) == 0 {
		return nil, errors.New("exclusion file has no entries")
	}
	return out, nil
}

func analyze(profile io.Reader, packages []string, excludes []exclusion, min float64) (*report, error) {
	if len(packages) == 0 {
		return nil, errors.New("no packages listed")
	}
	// Longest prefix first so agentmem/internal/plugins/graphrag wins over
	// agentmem/internal/plugins when both are part of the module.
	ordered := append([]string(nil), packages...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	byPkg := make(map[string]*row, len(packages))
	for _, pkg := range packages {
		byPkg[pkg] = &row{pkg: pkg}
	}

	scanner := bufio.NewScanner(profile)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	entries := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		name, statements, count, err := parseLine(line)
		if err != nil {
			return nil, err
		}
		pkg := matchPackage(ordered, name)
		if pkg == "" {
			return nil, fmt.Errorf("coverage entry outside the package set: %s", name)
		}
		entries++
		byPkg[pkg].total += statements
		if count > 0 {
			byPkg[pkg].hit += statements
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	if entries == 0 {
		return nil, errors.New("coverage profile is empty")
	}

	result := &report{}
	for _, pkg := range packages {
		row := byPkg[pkg]
		if reason := excludedReason(pkg, excludes); reason != "" {
			row.excluded = true
			result.excluded++
			result.rows = append(result.rows, *row)
			continue
		}
		result.aggHit += row.hit
		result.aggTotal += row.total
		switch {
		case row.total == 0:
			result.problems = append(result.problems, row.pkg+": no coverage data (no tests)")
		case below(row.hit, row.total, min):
			result.problems = append(result.problems, fmt.Sprintf("%s: %.1f%% < %.1f%%",
				row.pkg, 100*float64(row.hit)/float64(row.total), min))
		}
		result.rows = append(result.rows, *row)
	}
	sort.Slice(result.rows, func(i, j int) bool { return result.rows[i].pkg < result.rows[j].pkg })

	for _, e := range excludes {
		if !anyExcluded(result.rows, e.prefix) {
			return nil, fmt.Errorf("stale exclusion: %s", e.prefix)
		}
	}
	return result, nil
}

// parseLine reads one profile block: <file>:<start>,<end> <statements> <count>.
func parseLine(line string) (name string, statements, count int, err error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return "", 0, 0, fmt.Errorf("malformed coverage line: %q", line)
	}
	idx := strings.LastIndex(fields[0], ":")
	if idx <= 0 {
		return "", 0, 0, fmt.Errorf("malformed coverage line: %q", line)
	}
	name = fields[0][:idx]
	statements, err = strconv.Atoi(fields[1])
	if err != nil || statements < 0 {
		// -covermode=atomic emits zero-statement blocks for some boundary
		// positions; they carry no weight and are accepted.
		return "", 0, 0, fmt.Errorf("malformed coverage line: %q", line)
	}
	count, err = strconv.Atoi(fields[2])
	if err != nil || count < 0 {
		return "", 0, 0, fmt.Errorf("malformed coverage line: %q", line)
	}
	return name, statements, count, nil
}

func matchPackage(ordered []string, name string) string {
	clean := filepath.ToSlash(name)
	for _, pkg := range ordered {
		if clean == pkg || strings.HasPrefix(clean, pkg+"/") {
			return pkg
		}
	}
	return ""
}

func excludedReason(pkg string, excludes []exclusion) string {
	for _, e := range excludes {
		if pkg == e.prefix || strings.HasPrefix(pkg, e.prefix+"/") {
			return e.reason
		}
	}
	return ""
}

func anyExcluded(rows []row, prefix string) bool {
	for _, row := range rows {
		if row.excluded && (row.pkg == prefix || strings.HasPrefix(row.pkg, prefix+"/")) {
			return true
		}
	}
	return false
}

func below(hit, total int, min float64) bool {
	if total == 0 {
		return true
	}
	return 100*float64(hit)/float64(total)+1e-9 < min
}

func (r *report) write(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%-56s %7s %7s %8s\n", "package", "hit", "total", "cover")
	for _, row := range r.rows {
		label, percentage := row.pkg, "n/a"
		if row.excluded {
			label += " (excluded)"
		}
		if row.total > 0 {
			percentage = fmt.Sprintf("%.1f%%", 100*float64(row.hit)/float64(row.total))
		}
		fmt.Fprintf(&b, "%-56s %7d %7d %8s\n", label, row.hit, row.total, percentage)
	}
	aggregate := "n/a"
	if r.aggTotal > 0 {
		aggregate = fmt.Sprintf("%.1f%%", 100*float64(r.aggHit)/float64(r.aggTotal))
	}
	fmt.Fprintf(&b, "%-56s %7d %7d %8s\n", "total", r.aggHit, r.aggTotal, aggregate)
	if r.excluded > 0 {
		fmt.Fprintf(&b, "excluded: %d package(s) (see docs/testing.md)\n", r.excluded)
	}
	for _, problem := range r.problems {
		fmt.Fprintf(&b, "FAIL %s\n", problem)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
