package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"semiotic.systems/juke/internal/importer"
)

func runImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	var opts importer.Options
	var include listFlag
	fs.StringVar(&opts.Library, "library", "", "the library tree, holding Undamaged and so on")
	fs.Var(&include, "include", "a top-level directory of the library to import; repeatable (default Undamaged)")
	fs.StringVar(&opts.Catalog, "catalog", "", "the recovery's catalog database")
	fs.StringVar(&opts.CatalogRoot, "catalog-root", "/Volumes/Video/library", "the library root as the catalog's move_log recorded it")
	fs.StringVar(&opts.Playlists, "playlists", "", "a JSON file of extra playlists")
	fs.StringVar(&opts.Out, "out", "", "the new database; must not exist")
	parse(fs, args, "library", "catalog", "out")

	opts.Include = include
	if len(opts.Include) == 0 {
		opts.Include = []string{"Undamaged"}
	}
	opts.Log = os.Stderr

	r, err := importer.Run(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Printf("files seen:       %d\n", r.Files)
	fmt.Printf("  not audio:      %d\n", r.SkippedFiles)
	fmt.Printf("  refused:        %d\n", len(r.Refused))
	fmt.Printf("tracks:           %d\n", r.Tracks)
	fmt.Printf("  with no album:  %d\n", r.AlbumlessTrack)
	fmt.Printf("gap tracks:       %d\n", r.GapTracks)
	fmt.Printf("albums:           %d\n", r.Albums)
	fmt.Printf("playlists:        %d\n", r.Playlists)
	return nil
}
