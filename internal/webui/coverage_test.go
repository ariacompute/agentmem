package webui

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func withNewFS(t *testing.T, replacement func() (fs.FS, error)) {
	t.Helper()
	previous := newFS
	newFS = replacement
	t.Cleanup(func() { newFS = previous })
}

func TestNewFailsClosedOnBrokenBundle(t *testing.T) {
	withNewFS(t, func() (fs.FS, error) { return nil, errors.New("no bundle") })
	if _, err := New(); err == nil {
		t.Fatal("a missing console bundle must fail startup")
	}

	withNewFS(t, func() (fs.FS, error) { return fstest.MapFS{}, nil })
	if _, err := New(); err == nil {
		t.Fatal("a bundle without index.html must fail startup")
	}
}

func TestNewRequiresExactlyOneInlineScript(t *testing.T) {
	cases := map[string]string{
		"none": `<html><body><script src="/assets/app.js"></script></body></html>`,
		"two":  `<html><body><script>a()</script><script>b()</script></body></html>`,
	}
	for name, page := range cases {
		withNewFS(t, func() (fs.FS, error) {
			return fstest.MapFS{indexName: &fstest.MapFile{Data: []byte(page)}}, nil
		})
		if _, err := New(); err == nil {
			t.Fatalf("%s inline script count must fail startup", name)
		}
	}
}

func TestContentTypePinsConsoleTypes(t *testing.T) {
	files := fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte("<p>ok</p>")},
		"assets/app.json":   &fstest.MapFile{Data: []byte("{}")},
		"assets/app.map":    &fstest.MapFile{Data: []byte("{}")},
		"assets/logo.svg":   &fstest.MapFile{Data: []byte("<svg/>")},
		"assets/logo.png":   &fstest.MapFile{Data: []byte("png")},
		"assets/logo.ico":   &fstest.MapFile{Data: []byte("ico")},
		"assets/font.woff2": &fstest.MapFile{Data: []byte("woff")},
		"assets/style.css":  &fstest.MapFile{Data: []byte("body{}")},
		"assets/app.mjs":    &fstest.MapFile{Data: []byte("export {}")},
		"assets/notes.txt":  &fstest.MapFile{Data: []byte("hi")},
		"assets/blob":       &fstest.MapFile{Data: []byte{0}},
	}
	h := &Handler{files: files, csp: "default-src 'none'"}
	want := map[string]string{
		"/assets/app.json":   "application/json; charset=utf-8",
		"/assets/app.map":    "application/json; charset=utf-8",
		"/assets/logo.svg":   "image/svg+xml",
		"/assets/logo.png":   "image/png",
		"/assets/logo.ico":   "image/x-icon",
		"/assets/font.woff2": "font/woff2",
		"/assets/style.css":  "text/css; charset=utf-8",
		"/assets/app.mjs":    "text/javascript; charset=utf-8",
		"/assets/notes.txt":  "text/plain; charset=utf-8",
	}
	for requestPath, contentType := range want {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", requestPath, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != contentType {
			t.Fatalf("%s content type = %q", requestPath, got)
		}
	}
	// An unknown extension falls back to the platform table or octet-stream.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/blob", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("extensionless asset status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("an unknown extension must fall back to octet-stream, got %q", got)
	}
}
