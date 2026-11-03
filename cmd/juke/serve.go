package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"semiotic.systems/juke/internal/jmap"
	"semiotic.systems/juke/internal/store"
	"semiotic.systems/juke/internal/tailnet"
	"semiotic.systems/juke/internal/web"
)

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "", "the Juke database")
	library := fs.String("library", "", "the library tree that track paths are relative to")
	listen := fs.String("listen", "localhost:8737", "the address to listen on in plain HTTP, with no authentication; empty for none")
	baseURL := fs.String("base-url", "", "the URL clients reach the server at, if not the request's own")
	username := fs.String("username", "juke", "the username the session reports")
	maxGet := fs.Int("max-objects-in-get", 0, "the most objects one /get may return (default 5000)")
	webDir := fs.String("web-dir", "", "serve the review page from this directory, not the built-in copy")
	tailnetName := fs.String("tailnet", "", "join the tailnet as a node of this name, such as juke")
	tailnetState := fs.String("tailnet-state", "", "the directory the tailnet node keeps its state in; never copy it to another machine")
	tailnetHTTP := fs.Bool("tailnet-http", false, "serve the tailnet in plain HTTP on port 80, not HTTPS on 443")
	tailnetVerbose := fs.Bool("tailnet-verbose", false, "log everything tsnet says")
	var allow listFlag
	fs.Var(&allow, "allow", "a tailnet login allowed in, like rjbs@github; repeatable, and needed with --tailnet")
	parse(fs, args, "db", "library")

	if *tailnetName != "" && (*tailnetState == "" || len(allow) == 0) {
		return errors.New("--tailnet needs --tailnet-state and at least one --allow")
	}
	if *tailnetName == "" && *listen == "" {
		return errors.New("with neither --listen nor --tailnet there is nothing to serve on")
	}

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
	mux := http.NewServeMux()
	mux.Handle("/", srv.Handler())
	page := web.Handler(*webDir)
	mux.Handle("GET /{$}", page)
	for _, name := range web.Files {
		mux.Handle("GET /"+name, page)
	}

	// Each listener runs until it fails, and the first failure ends the
	// server. -- claude, 2026-11-01
	failed := make(chan error, 2)

	if *listen != "" {
		logger.Printf("serving %s on http://%s/", *dbPath, *listen)
		go func() { failed <- http.ListenAndServe(*listen, mux) }()
	}

	if *tailnetName != "" {
		node, err := tailnet.Start(ctx, *tailnetName, *tailnetState, logger, *tailnetVerbose)
		if err != nil {
			return fmt.Errorf("joining the tailnet: %w", err)
		}
		defer node.Close()
		ln, err := node.Listen(!*tailnetHTTP)
		if err != nil {
			return fmt.Errorf("listening on the tailnet: %w", err)
		}
		logger.Printf("serving %s on %s/ to %s", *dbPath, node.URL(!*tailnetHTTP), allow.String())
		guarded := tailnet.RequireLogin(node.WhoIs, allow, logger, mux)
		go func() { failed <- http.Serve(ln, guarded) }()
	}

	return <-failed
}
