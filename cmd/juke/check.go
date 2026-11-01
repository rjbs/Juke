package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/text/unicode/norm"

	"semiotic.systems/juke/internal/store"
)

// runCheck confirms that every track with audio has its file under the
// library, at the size the import recorded.  A file that isn't at its
// path is looked for under the path's other Unicode normalizations,
// because macOS can report a name in a different form than the bytes a
// copy wrote on another filesystem, and Linux compares bytes exactly.
// -- claude, 2026-11-01
func runCheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	dbPath := fs.String("db", "", "the Juke database")
	library := fs.String("library", "", "the library tree that track paths are relative to")
	show := fs.Int("show", 20, "how many problems of each kind to list")
	parse(fs, args, "db", "library")

	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	rows, err := s.DB.QueryContext(ctx, `SELECT id, path, size FROM track WHERE path IS NOT NULL ORDER BY path`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var checked int
	problems := map[string][]string{}
	var kinds []string
	report := func(kind, line string) {
		if _, ok := problems[kind]; !ok {
			kinds = append(kinds, kind)
		}
		problems[kind] = append(problems[kind], line)
	}
	for rows.Next() {
		var id, rel string
		var size int64
		if err := rows.Scan(&id, &rel, &size); err != nil {
			return err
		}
		checked++
		info, err := os.Stat(filepath.Join(*library, filepath.FromSlash(rel)))
		switch {
		case err == nil && info.Size() != size:
			report("wrong size", fmt.Sprintf("%s %s: %d bytes, want %d", id, rel, info.Size(), size))
		case err == nil:
		case !errors.Is(err, os.ErrNotExist):
			report("unreadable", fmt.Sprintf("%s %s: %v", id, rel, err))
		case otherForm(*library, rel) != "":
			report("other Unicode form", fmt.Sprintf("%s %s: found as %s", id, rel, otherForm(*library, rel)))
		default:
			report("missing", fmt.Sprintf("%s %s", id, rel))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	total := 0
	for _, kind := range kinds {
		lines := problems[kind]
		total += len(lines)
		fmt.Printf("%s: %d\n", kind, len(lines))
		for _, line := range lines[:min(len(lines), *show)] {
			fmt.Printf("  %s\n", line)
		}
	}
	fmt.Printf("%d tracks with audio checked, %d problems\n", checked, total)
	if total > 0 {
		return fmt.Errorf("%d tracks' files aren't where the database says", total)
	}
	return nil
}

// otherForm returns rel in another Unicode normalization form, if the
// file exists under that name, or "".
func otherForm(library, rel string) string {
	for _, form := range []norm.Form{norm.NFC, norm.NFD} {
		alt := form.String(rel)
		if alt == rel {
			continue
		}
		if _, err := os.Stat(filepath.Join(library, filepath.FromSlash(alt))); err == nil {
			return alt
		}
	}
	return ""
}
