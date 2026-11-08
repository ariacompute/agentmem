// Package safepath contains the single, shared path-safety boundary used by
// the OKF bundle and the Git backend. It is a leaf package with no internal
// dependencies so both consumers cannot drift into subtly different checks.
package safepath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxRelLength bounds a relative path inside a bundle or repository. It keeps
// concept ids and resource names from becoming an unbounded allocation vector.
const MaxRelLength = 512

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// Validate rejects every relative path that could resolve outside its root,
// including platform-specific ambiguities. It is deliberately stricter than
// OKF conformance: this guards filesystem paths, not document metadata.
func Validate(rel string) error {
	if rel == "" {
		return errors.New("safepath: empty path")
	}
	if len(rel) > MaxRelLength {
		return fmt.Errorf("safepath: path exceeds %d bytes", MaxRelLength)
	}
	if strings.ContainsRune(rel, 0) {
		return errors.New("safepath: NUL byte in path")
	}
	if strings.Contains(rel, "\\") {
		return errors.New("safepath: backslash in path")
	}
	if strings.Contains(rel, ":") {
		return errors.New("safepath: colon in path")
	}
	for _, r := range rel {
		if r < 0x20 || r == 0x7f {
			return errors.New("safepath: control character in path")
		}
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || clean == ".." || clean == "/" ||
		strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return fmt.Errorf("safepath: path escapes root: %q", rel)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		if seg != strings.TrimSpace(seg) || strings.HasSuffix(seg, " ") || strings.HasSuffix(seg, ".") {
			return fmt.Errorf("safepath: ambiguous path segment %q", seg)
		}
		stem := seg
		if idx := strings.LastIndexByte(stem, '.'); idx > 0 {
			stem = stem[:idx]
		}
		if windowsReserved[strings.ToUpper(stem)] {
			return fmt.Errorf("safepath: reserved path segment %q", seg)
		}
	}
	return nil
}

// Resolve validates rel and returns the join of root and the cleaned relative
// path, but only after confirming with EvalSymlinks that neither an existing
// parent directory nor the target itself escapes the resolved root. The
// returned path keeps the caller's original root spelling so existing path
// handling (and tests) stay stable.
func Resolve(root, rel string) (string, error) {
	if err := Validate(rel); err != nil {
		return "", err
	}
	if root == "" {
		return "", errors.New("safepath: empty root")
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	target := filepath.Join(root, filepath.FromSlash(clean))
	lexical, err := filepath.Rel(root, target)
	if err != nil || !contained(lexical) {
		return "", fmt.Errorf("safepath: path escapes root: %q", rel)
	}

	// A root that does not exist yet (first write into a fresh bundle) cannot
	// hide a symlink; lexical containment is sufficient in that case.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return target, nil
		}
		return "", fmt.Errorf("safepath: resolve root: %w", err)
	}
	realTarget, err := resolveNearestExisting(target)
	if err != nil {
		return "", err
	}
	inside, err := filepath.Rel(realRoot, realTarget)
	if err != nil || !contained(inside) {
		return "", fmt.Errorf("safepath: symlink escapes root: %q", rel)
	}
	return target, nil
}

func contained(rel string) bool {
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(rel) && rel != ""
}

// resolveNearestExisting resolves symlinks on the deepest existing ancestor of
// p, then re-appends the not-yet-existing suffix. This is what makes the check
// work for both reads (target exists) and writes (target is new).
func resolveNearestExisting(p string) (string, error) {
	cur := p
	var suffix []string
	for {
		_, err := os.Lstat(cur)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("safepath: stat %q: %w", cur, err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", errors.New("safepath: no existing ancestor")
		}
		suffix = append([]string{filepath.Base(cur)}, suffix...)
		cur = parent
	}
	real, err := filepath.EvalSymlinks(cur)
	if err != nil {
		return "", fmt.Errorf("safepath: resolve %q: %w", cur, err)
	}
	parts := append([]string{real}, suffix...)
	return filepath.Join(parts...), nil
}
