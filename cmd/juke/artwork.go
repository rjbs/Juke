package main

import (
	"context"
	"flag"
	"fmt"

	"semiotic.systems/juke/internal/artwork"
	"semiotic.systems/juke/internal/store"
)

// runArtwork finds album art in the library's files and beside them,
// stores each distinct image once, and sets each album's and track's
// art; see doc/artwork.md.  The server notices the change on its next
// poll.
func runArtwork(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("artwork", flag.ExitOnError)
	dbPath := fs.String("db", "", "the Juke database")
	library := fs.String("library", "", "the library tree that track paths are relative to; images are stored under it")
	parse(fs, args, "db", "library")

	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	r, err := artwork.Run(ctx, s.DB, *library)
	if err != nil {
		return err
	}
	fmt.Printf("images known:          %d\n", r.Images)
	fmt.Printf("  read from files:     %d\n", r.Extracted)
	fmt.Printf("  unreadable:          %d\n", r.Unreadable)
	fmt.Printf("albums with art:       %d\n", r.Albums)
	fmt.Printf("  from a folder image: %d\n", r.Folder)
	fmt.Printf("  settled by a tie:    %d\n", r.Ties)
	fmt.Printf("tracks with their own: %d\n", r.Overrides)
	fmt.Printf("  one picture, encoded several ways: %d\n", r.Collapsed)
	fmt.Printf("images hashed:         %d\n", r.Hashed)
	fmt.Printf("  unhashable:          %d\n", r.Unhashable)
	fmt.Printf("albums+tracks changed: %d\n", r.Changed)
	fmt.Printf("  chosen by hand, left alone: %d\n", r.Kept)
	return nil
}
