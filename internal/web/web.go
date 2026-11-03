// Package web serves Juke's review page: a page that browses, plays and
// rates the library through the server's own JMAP API, and a review tab
// for putting albums right.
package web

import (
	"embed"
	"net/http"
	"os"
)

//go:embed index.html juke.css juke.js review.js
var files embed.FS

// Files are the page's files besides the page itself, which the server
// routes to the handler by name.
var Files = []string{"juke.css", "juke.js", "review.js"}

// Handler serves the page and its files.  With a non-empty dir, they are
// read from dir on every request instead, so they can be edited without
// a rebuild.
func Handler(dir string) http.Handler {
	fsys := http.FS(files)
	if dir != "" {
		fsys = http.FS(os.DirFS(dir))
	}
	serve := http.FileServer(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		serve.ServeHTTP(w, r)
	})
}
