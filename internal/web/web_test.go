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

// checkPage requests the page from a handler and checks that it is HTML
// containing want.
func checkPage(t *testing.T, h http.Handler, want string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(string(body), want) {
		t.Errorf("page lacks %q", want)
	}
}

func TestEmbeddedPage(t *testing.T) {
	checkPage(t, Handler(""), "<title>Juke</title>")
}

func TestPageFromDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><p>edited</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkPage(t, Handler(dir), "edited")
}
