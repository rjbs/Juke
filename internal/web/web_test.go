package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkFile requests a path from a handler and checks its content type
// and that it contains want.
func checkFile(t *testing.T, h http.Handler, path, wantType, want string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), wantType) {
		t.Errorf("%s: status %d, type %q", path, rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(string(body), want) {
		t.Errorf("%s lacks %q", path, want)
	}
}

func TestEmbeddedPage(t *testing.T) {
	checkFile(t, Handler(""), "/", "text/html", "<title>Juke</title>")
	checkFile(t, Handler(""), "/juke.css", "text/css", ":root")
	checkFile(t, Handler(""), "/juke.js", "text/javascript", "function route()")
	checkFile(t, Handler(""), "/review.js", "text/javascript", "")
	checkFile(t, Handler(""), "/smart.js", "text/javascript", "const smart")
}

func TestPageFromDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><p>edited</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkFile(t, Handler(dir), "/", "text/html", "edited")
}
