package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"semiotic.systems/juke/internal/originals"
	"semiotic.systems/juke/internal/store"
)

func runOriginals(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("originals", flag.ExitOnError)
	dbPath := fs.String("db", "", "the Juke database")
	library := fs.String("library", "", "the library tree that track paths are relative to")
	journal := fs.String("journal", "", "the journal of moves (default: the database's name plus .originals-journal)")
	dryRun := fs.Bool("dry-run", false, "hash every file and report, but move nothing")
	parse(fs, args, "db", "library")
	if *journal == "" {
		*journal = *dbPath + ".originals-journal"
	}

	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	r, err := originals.Run(ctx, originals.Options{
		DB:       s.DB,
		Library:  *library,
		Journal:  *journal,
		DryRun:   *dryRun,
		Progress: os.Stderr,
	})
	if r != nil {
		verb := "moved"
		if *dryRun {
			verb = "would move"
		}
		fmt.Printf("%s:        %d files, %.1f GB\n", verb, r.Moved, float64(r.Bytes)/1e9)
		fmt.Printf("already stored:    %d\n", r.Already)
		fmt.Printf("finished from the journal: %d\n", r.Recovered)
		fmt.Printf("duplicates left in place: %d\n", len(r.Duplicates))
		for _, d := range r.Duplicates {
			fmt.Printf("  %s %s\n    same bytes as %s %s\n", d.TrackID, d.Path, d.SameAs, d.SameAt)
		}
	}
	return err
}
