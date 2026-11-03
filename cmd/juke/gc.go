package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"semiotic.systems/juke/internal/artwork"
	"semiotic.systems/juke/internal/store"
)

// runGC removes the images nothing names any more: uploads nobody set,
// and art no album or track has.  An upload younger than the grace
// period is kept, since the client that sent it may be about to set it.
func runGC(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("gc", flag.ExitOnError)
	dbPath := fs.String("db", "", "the Juke database")
	library := fs.String("library", "", "the library tree, where images are stored")
	grace := fs.Duration("grace", 24*time.Hour, "how long an upload nobody has set is kept")
	parse(fs, args, "db", "library")

	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	n, err := artwork.Collect(ctx, s.DB, *library, time.Now().Add(-*grace))
	if err != nil {
		return err
	}
	fmt.Printf("images removed:        %d\n", n)
	return nil
}
