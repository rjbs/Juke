// Package originals moves a library's audio files from the human-readable
// tree the recovery left, Artist/Album/NN Title.mp3, to the opaque store
// doc/storage.md calls for: originals/ab/<sha256>.mp3, named for the
// SHA-256 of the whole file.  Browsing happens through Juke, not the file
// tree, and an opaque name never needs to change when metadata does.
//
// Files are renamed, never copied, so the library must be one filesystem;
// nothing is deleted but the directories left empty.  A track's blobId
// doesn't depend on its path, so clients see no change.
//
// Each move is written to a journal before the rename, and the track's
// path is updated after it.  A run interrupted between the two leaves a
// file at its new name with the database still giving the old one; the
// next run reads the journal and finishes those first.  -- claude, 2026-11-01
package originals

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Dir is the directory under the library that holds the opaque store.
const Dir = "originals"

// Options control a run.
type Options struct {
	DB      *sql.DB
	Library string

	// Journal is the file each move is recorded in before it's made.
	Journal string

	// DryRun hashes every file and reports, but moves nothing.
	DryRun bool

	// Progress, if set, gets a line now and then.
	Progress io.Writer
}

// Report says what a run did, or with DryRun, would do.
type Report struct {
	Moved     int
	Recovered int
	Already   int
	Bytes     int64

	// Duplicates are tracks whose file is byte-identical to another
	// track's.  Each is left where it is, for a person to decide about.
	Duplicates []Duplicate
}

// Duplicate is a track whose file has the same bytes as another's.
type Duplicate struct {
	TrackID, Path  string
	SameAs, SameAt string
}

// Name is the opaque path, relative to the library, for a file with the
// given SHA-256 and original path; the original's extension is kept, in
// lower case, so the stored file still says what it is.
func Name(sum, original string) string {
	return path.Join(Dir, sum[:2], sum+strings.ToLower(path.Ext(original)))
}

// Run moves every track's file into the opaque store.
func Run(ctx context.Context, opts Options) (*Report, error) {
	r := &Report{}
	if !opts.DryRun {
		n, err := finishJournal(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("finishing the journal's moves: %w", err)
		}
		r.Recovered = n
	}

	type track struct{ id, path string }
	var tracks []track
	rows, err := opts.DB.QueryContext(ctx, `SELECT id, path FROM track WHERE path IS NOT NULL ORDER BY path`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t track
		if err := rows.Scan(&t.id, &t.path); err != nil {
			rows.Close()
			return nil, err
		}
		tracks = append(tracks, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var journal *os.File
	if !opts.DryRun {
		journal, err = os.OpenFile(opts.Journal, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		defer journal.Close()
	}

	type owner struct{ id, path string }
	owners := map[string]owner{}
	for _, t := range tracks {
		if strings.HasPrefix(t.path, Dir+"/") {
			r.Already++
			owners[t.path] = owner{t.id, t.path}
		}
	}
	for i, t := range tracks {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if strings.HasPrefix(t.path, Dir+"/") {
			continue
		}
		src := filepath.Join(opts.Library, filepath.FromSlash(t.path))
		sum, size, err := hashFile(src)
		if err != nil {
			return r, fmt.Errorf("track %s: %w", t.id, err)
		}
		name := Name(sum, t.path)
		if o, ok := owners[name]; ok {
			r.Duplicates = append(r.Duplicates, Duplicate{TrackID: t.id, Path: t.path, SameAs: o.id, SameAt: o.path})
			continue
		}
		owners[name] = owner{t.id, t.path}
		r.Bytes += size

		if !opts.DryRun {
			dest := filepath.Join(opts.Library, filepath.FromSlash(name))
			if _, err := os.Lstat(dest); err == nil {
				return r, fmt.Errorf("track %s: %s already exists, and no track names it", t.id, name)
			}
			if err := record(journal, t.id, t.path, name); err != nil {
				return r, err
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return r, err
			}
			if err := os.Rename(src, dest); err != nil {
				return r, fmt.Errorf("track %s: %w", t.id, err)
			}
			if _, err := opts.DB.ExecContext(ctx, `UPDATE track SET path = ? WHERE id = ?`, name, t.id); err != nil {
				return r, fmt.Errorf("track %s moved to %s, but: %w", t.id, name, err)
			}
		}
		r.Moved++
		if opts.Progress != nil && (i+1)%1000 == 0 {
			fmt.Fprintf(opts.Progress, "%d of %d tracks\n", i+1, len(tracks))
		}
	}

	if !opts.DryRun {
		entries, err := os.ReadDir(opts.Library)
		if err != nil {
			return r, err
		}
		for _, e := range entries {
			if e.IsDir() && e.Name() != Dir {
				removeEmptyDirs(filepath.Join(opts.Library, e.Name()))
			}
		}
	}
	return r, nil
}

// record writes a move to the journal and syncs it, so it's on disk
// before the rename it describes.
func record(journal *os.File, id, from, to string) error {
	if strings.ContainsAny(from, "\t\n") {
		return fmt.Errorf("track %s: path %q has a tab or newline, which the journal can't hold", id, from)
	}
	if _, err := fmt.Fprintf(journal, "%s\t%s\t%s\n", id, from, to); err != nil {
		return err
	}
	return journal.Sync()
}

// finishJournal finishes the moves the journal records whose rename happened
// but whose track still gives the old path, and returns how many.
func finishJournal(ctx context.Context, opts Options) (int, error) {
	f, err := os.Open(opts.Journal)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()

	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 3 {
			continue
		}
		id, from, to := fields[0], fields[1], fields[2]
		var current string
		err := opts.DB.QueryRowContext(ctx, `SELECT path FROM track WHERE id = ?`, id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && current != from) {
			continue
		}
		if err != nil {
			return n, err
		}
		_, fromErr := os.Lstat(filepath.Join(opts.Library, filepath.FromSlash(from)))
		_, toErr := os.Lstat(filepath.Join(opts.Library, filepath.FromSlash(to)))
		if !errors.Is(fromErr, os.ErrNotExist) || toErr != nil {
			continue
		}
		if _, err := opts.DB.ExecContext(ctx, `UPDATE track SET path = ? WHERE id = ?`, to, id); err != nil {
			return n, err
		}
		n++
	}
	return n, scanner.Err()
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// removeEmptyDirs removes directories under dir that hold nothing,
// deepest first, and dir itself if it ends up empty.  Files are never
// touched; a directory with even a .DS_Store in it stays.
func removeEmptyDirs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			removeEmptyDirs(filepath.Join(dir, e.Name()))
		}
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		os.Remove(dir)
	}
}
