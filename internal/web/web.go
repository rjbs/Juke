// Package web serves Juke's review page: a single HTML file that browses,
// plays and rates the library through the server's own JMAP API.
package web

import (
	_ "embed"
	"net/http"
	"path/filepath"
)

//go:embed index.html
var index []byte

// Handler serves the page.  With a non-empty dir, the page is read from
// dir/index.html on every request instead, so it can be edited without a
// rebuild.
func Handler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		if dir != "" {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(index)
	})
}
