package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	"semiotic.systems/juke/internal/jmap"
	"semiotic.systems/juke/internal/store"
)

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "", "the Juke database")
	library := fs.String("library", "", "the library tree that track paths are relative to")
	listen := fs.String("listen", "localhost:8737", "the address to listen on")
	baseURL := fs.String("base-url", "", "the URL clients reach the server at, if not the request's own")
	username := fs.String("username", "juke", "the username the session reports")
	maxGet := fs.Int("max-objects-in-get", 0, "the most objects one /get may return (default 5000)")
	parse(fs, args, "db", "library")

	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	logger := log.New(os.Stderr, "juke: ", log.LstdFlags)
	srv := jmap.New(jmap.Config{
		Store:           s,
		Library:         *library,
		Username:        *username,
		BaseURL:         *baseURL,
		MaxObjectsInGet: *maxGet,
		Log:             logger,
	})
	logger.Printf("serving %s on http://%s/", *dbPath, *listen)
	return http.ListenAndServe(*listen, srv.Handler())
}
