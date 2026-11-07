package okf

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Bundle is a directory tree of OKF concept documents.
type Bundle struct {
	Root string
	// LogWrites controls whether Put/Delete append to log.md.
	LogWrites bool
}

// Open creates the bundle root if needed.
func Open(root string) (*Bundle, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("okf: bundle root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("okf: create bundle root: %w", err)
	}
	return &Bundle{Root: abs, LogWrites: true}, nil
}

var reserved = map[string]bool{"index": true, "log": true}

// ValidateConceptID rejects traversal, absolute paths and reserved names.
func ValidateConceptID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("okf: empty concept id")
	}
	if strings.ContainsRune(id, 0) || strings.Contains(id, "\\") {
		return "", fmt.Errorf("okf: illegal concept id %q", id)
	}
	id = strings.TrimSuffix(id, ".md")
	clean := filepath.ToSlash(filepath.Clean(id))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("okf: concept id escapes bundle: %q", id)
	}
	base := filepath.Base(clean)
	if reserved[strings.TrimSuffix(base, ".md")] {
		return "", fmt.Errorf("okf: reserved concept id %q", id)
	}
	return clean, nil
}

// Path resolves a concept id to an absolute file path inside the bundle.
func (b *Bundle) Path(id string) (string, error) {
	clean, err := ValidateConceptID(id)
	if err != nil {
		return "", err
	}
	p := filepath.Join(b.Root, filepath.FromSlash(clean)+".md")
	rel, err := filepath.Rel(b.Root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("okf: path escapes bundle: %q", id)
	}
	return p, nil
}

// Hash is the canonical content hash used to relate Git bodies with metadata.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// maxResourceBytes bounds sanctioned computation resources read from a bundle.
const maxResourceBytes = 1 << 20

// ReadResource reads a non-Markdown bundle resource (for example a sanctioned
// .sql file) referenced by an Attested Computation. It applies its own path
// safety checks because Bundle.Path always appends .md and is reserved for
// concept documents. Traversal, absolute paths and symlink escapes are refused.
func (b *Bundle) ReadResource(rel string) ([]byte, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return nil, errors.New("okf: empty resource path")
	}
	if strings.ContainsRune(rel, 0) || strings.Contains(rel, "\\") {
		return nil, fmt.Errorf("okf: illegal resource path %q", rel)
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return nil, fmt.Errorf("okf: resource path escapes bundle: %q", rel)
	}
	p := filepath.Join(b.Root, filepath.FromSlash(clean))
	if _, err := filepath.Rel(b.Root, p); err != nil {
		return nil, fmt.Errorf("okf: resource path escapes bundle: %q", rel)
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return nil, fmt.Errorf("okf: resource directory %q: %w", rel, err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(b.Root)
	if err != nil {
		return nil, fmt.Errorf("okf: bundle root: %w", err)
	}
	check, err := filepath.Rel(resolvedRoot, filepath.Join(resolvedParent, filepath.Base(p)))
	if err != nil || check == ".." || strings.HasPrefix(check, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("okf: resource symlink escapes bundle: %q", rel)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("okf: read resource %q: %w", rel, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxResourceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("okf: read resource %q: %w", rel, err)
	}
	if len(data) > maxResourceBytes {
		return nil, fmt.Errorf("okf: resource %q exceeds %d bytes", rel, maxResourceBytes)
	}
	return data, nil
}

// Put writes a concept atomically, then incrementally refreshes only the
// affected directory's index.md (no full-tree regeneration).
func (b *Bundle) Put(id string, d *Document) (string, error) {
	if err := d.Validate(); err != nil {
		return "", err
	}
	p, err := b.Path(id)
	if err != nil {
		return "", err
	}
	data, err := d.Marshal()
	if err != nil {
		return "", err
	}
	if err := atomicWrite(p, data); err != nil {
		return "", err
	}
	dir := filepath.Dir(p)
	if err := b.RebuildIndex(dir); err != nil {
		return "", err
	}
	if b.LogWrites {
		if err := b.AppendLog(dir, "Update", cleanID(id)); err != nil {
			return "", err
		}
	}
	return Hash(data), nil
}

// Get reads a concept.
func (b *Bundle) Get(id string) (*Document, error) {
	p, err := b.Path(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("okf: read %s: %w", id, err)
	}
	return ParseDocument(data)
}

// Delete removes a single concept (even from an error-state bundle).
func (b *Bundle) Delete(id string) error {
	p, err := b.Path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		return fmt.Errorf("okf: delete %s: %w", id, err)
	}
	dir := filepath.Dir(p)
	if err := b.RebuildIndex(dir); err != nil {
		return err
	}
	if b.LogWrites {
		return b.AppendLog(dir, "Deprecation", cleanID(id))
	}
	return nil
}

// List returns all concept ids, sorted, excluding reserved files.
func (b *Bundle) List() ([]string, error) {
	var out []string
	err := filepath.WalkDir(b.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		base := strings.TrimSuffix(d.Name(), ".md")
		if reserved[base] {
			return nil
		}
		rel, err := filepath.Rel(b.Root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(strings.TrimSuffix(rel, ".md")))
		return nil
	})
	sort.Strings(out)
	return out, err
}

var linkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// Links extracts concept ids referenced by markdown links. Broken links are
// legal in OKF and are returned as-is without existence checks.
func (b *Bundle) Links(id string) ([]string, error) {
	d, err := b.Get(id)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range linkRe.FindAllStringSubmatch(d.Body, -1) {
		target := strings.TrimSpace(strings.SplitN(m[1], " ", 2)[0])
		if target == "" || strings.Contains(target, "://") {
			continue
		}
		target = strings.SplitN(target, "#", 2)[0]
		if !strings.HasSuffix(target, ".md") {
			continue
		}
		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(target, "/")
		} else {
			target = filepath.ToSlash(filepath.Join(filepath.Dir(cleanID(id)), target))
		}
		clean, err := ValidateConceptID(target)
		if err != nil || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	sort.Strings(out)
	return out, nil
}

// RebuildIndex regenerates index.md for one directory (progressive disclosure).
func (b *Bundle) RebuildIndex(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var subdirs, concepts []string
	var descriptions = map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			subdirs = append(subdirs, name)
			continue
		}
		if !strings.HasSuffix(name, ".md") || reserved[strings.TrimSuffix(name, ".md")] {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		concepts = append(concepts, id)
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			if d, err := ParseDocument(data); err == nil {
				descriptions[id] = d.Description
			}
		}
	}
	sort.Strings(subdirs)
	sort.Strings(concepts)
	title := filepath.Base(dir)
	rel, _ := filepath.Rel(b.Root, dir)
	if rel != "." {
		title = filepath.ToSlash(rel)
	}
	var sb strings.Builder
	sb.WriteString("# " + title + "\n\n")
	if len(subdirs) > 0 {
		sb.WriteString("## Subdirectories\n\n")
		for _, s := range subdirs {
			fmt.Fprintf(&sb, "* [%s](%s/index.md)\n", s, s)
		}
		sb.WriteString("\n")
	}
	if len(concepts) > 0 {
		sb.WriteString("## Concepts\n\n")
		for _, c := range concepts {
			desc := descriptions[c]
			if desc == "" {
				desc = "OKF concept."
			}
			fmt.Fprintf(&sb, "* [%s](%s.md) - %s\n", c, c, desc)
		}
	}
	return atomicWrite(filepath.Join(dir, "index.md"), []byte(sb.String()))
}

// AppendLog prepends a dated entry to log.md (newest first).
func (b *Bundle) AppendLog(dir, action, detail string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, "log.md")
	existing, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	date := time.Now().UTC().Format("2006-01-02")
	entry := fmt.Sprintf("* **%s**: %s", action, detail)
	body := strings.TrimSuffix(string(existing), "\n")
	if body == "" {
		body = "# Directory Update Log"
	}
	prefix := "## " + date
	if idx := strings.Index(body, "\n"+prefix+"\n"); idx >= 0 {
		insert := idx + 1 + len(prefix) + 1
		body = body[:insert] + entry + "\n" + body[insert:]
	} else {
		firstHeading := strings.Index(body, "\n## ")
		if firstHeading < 0 {
			body += "\n\n" + prefix + "\n" + entry
		} else {
			body = body[:firstHeading] + "\n\n" + prefix + "\n" + entry + body[firstHeading:]
		}
	}
	return atomicWrite(p, []byte(strings.TrimSuffix(body, "\n")+"\n"))
}

func cleanID(id string) string {
	c, err := ValidateConceptID(id)
	if err != nil {
		return strings.TrimSpace(id)
	}
	return c
}

func atomicWrite(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
