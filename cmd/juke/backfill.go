package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"

	"semiotic.systems/juke/internal/backfill"
	"semiotic.systems/juke/internal/store"
)

// runBackfill fills in properties that older imports didn't record:
// genre from the tags the import stored, duration from the audio under
// the library, either from the catalog for what those don't say, and
// track numbers from filenames where nothing else gave one.  The server
// notices the change on its next poll.
func runBackfill(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("backfill", flag.ExitOnError)
	dbPath := fs.String("db", "", "the Juke database")
	library := fs.String("library", "", "the library tree that track paths are relative to")
	catalogPath := fs.String("catalog", "", "the recovery's catalog database, for what the files don't say")
	journal := fs.String("journal", "", "the originals move's journal, for the names moved files had (default: the database's name plus .originals-journal)")
	parse(fs, args, "db", "library")
	if *journal == "" {
		*journal = *dbPath + ".originals-journal"
	}

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

	g, err := backfill.Genre(ctx, s.DB, catalog)
	if err != nil {
		return err
	}
	fmt.Printf("genre from tags:       %d\n", g.FromTags)
	fmt.Printf("genre from catalog:    %d\n", g.FromCatalog)
	fmt.Printf("still without one:     %d\n", g.Without)

	d, err := backfill.Duration(ctx, s.DB, *library, catalog)
	if err != nil {
		return err
	}
	fmt.Printf("duration from files:   %d\n", d.FromFiles)
	fmt.Printf("  unmeasured:          %d\n", d.Unmeasured)
	fmt.Printf("duration from catalog: %d\n", d.FromCatalog)
	fmt.Printf("still without one:     %d\n", d.Without)
	if catalog != nil {
		fmt.Printf("catalog disagrees:     %d\n", d.CatalogDisagrees)
	}

	n, err := backfill.Numbers(ctx, s.DB, catalog, *journal)
	if err != nil {
		return err
	}
	fmt.Printf("numbered from files:   %d\n", n.Placed)
	fmt.Printf("  on albums:           %d\n", n.Albums)
	fmt.Printf("  spot taken:          %d\n", n.Taken)
	fmt.Printf("  no number anywhere:  %d\n", n.Unnamed)
	return nil
}
