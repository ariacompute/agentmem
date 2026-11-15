package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// indexName is the only page the console serves; every other URL is an asset.
const indexName = "index.html"

// consolePolicy is the page policy, identical in intent to the CSP meta tag in
// web/index.html. Only the inline theme bootstrap needs a digest, and it is
// computed from the embedded file instead of being copied by hand, so the Go
// header cannot drift from the HTML that is actually served.
const consolePolicy = "default-src 'none'; script-src 'self' %s; " +
	"style-src 'self'; img-src 'self'; connect-src 'self'; " +
	"base-uri 'none'; form-action 'none'; object-src 'none'; frame-ancestors 'none'"

// inlineScript matches a classic <script> block without attributes: the theme
// resolver in index.html. A module script is always an external `src=`, so it
// never matches here.
var inlineScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// Handler serves the embedded console read-only. It implements the console port
// that internal/handlers consumes.
type Handler struct {
	files fs.FS
	csp   string
}

// New builds the console handler. A missing index.html or an unexpected number
// of inline scripts is a hard error: the binary must never serve a page whose
// policy it cannot state, so startup fails closed instead of degrading.
func New() (*Handler, error) {
	files, err := FS()
	if err != nil {
		return nil, fmt.Errorf("webui: embedded console: %w", err)
	}
	index, err := fs.ReadFile(files, indexName)
	if err != nil {
		return nil, fmt.Errorf("webui: embedded console index: %w", err)
	}
	digest, err := inlineDigest(index)
	if err != nil {
		return nil, err
	}
	return &Handler{files: files, csp: fmt.Sprintf(consolePolicy, digest)}, nil
}

// inlineDigest returns the CSP source expression of the single inline script.
func inlineDigest(index []byte) (string, error) {
	matches := inlineScript.FindAllSubmatch(index, -1)
	if len(matches) != 1 {
		return "", errors.New("webui: console index must contain exactly one inline script")
	}
	sum := sha256.Sum256(matches[0][1])
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'", nil
}

// CSP is the page-level policy the console needs. The API policy
// (default-src 'none') stays in place for every other path; both are enforced
// when set, so serving a page under the API policy would render nothing.
func (h *Handler) CSP() string { return h.csp }

// Public reports whether a request path may be read anonymously. The console is
// a public page, exactly like GET /healthz; the API calls it makes still need a
// bearer token. Anything not matched here is not public (fail closed).
func (h *Handler) Public(requestPath string) bool {
	_, ok := h.resolve(requestPath)
	return ok
}

// resolve maps a request path to an embedded file name.
func (h *Handler) resolve(requestPath string) (string, bool) {
	if requestPath == "" {
		return "", false
	}
	// Only already-clean paths are served: "..", "//" and "/assets/" are
	// rejected instead of normalised, so a traversal attempt can never be
	// rewritten into a hit.
	if path.Clean(requestPath) != requestPath || strings.Contains(requestPath, "..") {
		return "", false
	}
	switch requestPath {
	case "/", "/" + indexName:
		return indexName, true
	}
	if !strings.HasPrefix(requestPath, "/assets/") {
		return "", false
	}
	name := strings.TrimPrefix(requestPath, "/")
	if name == "" || strings.HasSuffix(name, "/") {
		return "", false
	}
	return name, true
}

// ServeHTTP serves index.html, the hashed assets, and nothing else.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.setHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name, ok := h.resolve(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	body, err := fs.ReadFile(h.files, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", contentType(name))
	if name == indexName {
		// The page must be revalidated: it names the current hashed assets.
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		// Assets carry a content hash in the file name, so they never change.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

// setHeaders replaces the API posture with the console posture. The console is
// served from the same origin as the API, so it keeps nosniff/framing/referrer
// protections; only the CSP and the cache policy differ.
func (h *Handler) setHeaders(w http.ResponseWriter) {
	head := w.Header()
	head.Set("Content-Security-Policy", h.csp)
	head.Set("X-Content-Type-Options", "nosniff")
	head.Set("X-Frame-Options", "DENY")
	head.Set("Referrer-Policy", "no-referrer")
}

// contentType pins the types the console actually ships. Relying on the host
// mime table would make a module script depend on /etc/mime.types.
func contentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	}
	if fallback := mime.TypeByExtension(path.Ext(name)); fallback != "" {
		return fallback
	}
	return "application/octet-stream"
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
