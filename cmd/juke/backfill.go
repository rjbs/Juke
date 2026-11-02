package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"

	"semiotic.systems/juke/internal/backfill"
	"semiotic.systems/juke/internal/store"
)

// runBackfill fills in properties that older imports didn't record,
// from the tags the import stored and, given the catalog, from it.
// Today that is genre.  The server notices the change on its next poll.
func runBackfill(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("backfill", flag.ExitOnError)
	dbPath := fs.String("db", "", "the Juke database")
	catalogPath := fs.String("catalog", "", "the recovery's catalog database, for what the tags don't say")
	parse(fs, args, "db")

	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	var catalog *sql.DB
	if *catalogPath != "" {
		catalog, err = sql.Open("sqlite", "file:"+*catalogPath+"?mode=ro")
		if err != nil {
			return err
		}
		defer catalog.Close()
	}

	r, err := backfill.Genre(ctx, s.DB, catalog)
	if err != nil {
		return err
	}
	fmt.Printf("genre from tags:    %d\n", r.FromTags)
	fmt.Printf("genre from catalog: %d\n", r.FromCatalog)
	fmt.Printf("still without one:  %d\n", r.Without)
	return nil
}
