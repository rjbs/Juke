// Package backfill fills in track properties that an import made before
// the property existed left empty.  The import stores every file's tags
// as found, so a property derived from tags can be filled from the
// database alone; a property read from the audio means opening each
// file again; and the catalog fills what neither says, as it does at
// import.  See doc/import.md.
package backfill

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"semiotic.systems/juke/internal/audio"
	"semiotic.systems/juke/internal/store"
	"semiotic.systems/juke/internal/tags"
)

// GenreReport counts what a genre backfill did.
type GenreReport struct {
	FromTags    int // tracks given a genre from their stored tags
	FromCatalog int // tracks given a genre from the catalog
	Without     int // tracks still without one
}

// Genre sets the genre of every track that has none, from its stored
// tags first and then, when a catalog is given, from the catalog track
// with the same title, album and track number, when exactly one has a
// genre.  Every track changed gets the same new modseq, so clients
// refetch them once.
func Genre(ctx context.Context, db *sql.DB, catalog *sql.DB) (*GenreReport, error) {
	r := &GenreReport{}
	err := fill(ctx, db, "genre", func(ctx context.Context, tx *sql.Tx, set func(id string, value any) error) error {
		fromTags, err := genresFromTags(ctx, tx)
		if err != nil {
			return err
		}
		for _, g := range fromTags {
			if err := set(g.id, g.value); err != nil {
				return err
			}
			r.FromTags++
		}
		if catalog == nil {
			return nil
		}
		fromCatalog, err := matchCatalog(ctx, tx, catalog, `t.genre IS NULL`, `genre`)
		if err != nil {
			return err
		}
		for _, g := range fromCatalog {
			if err := set(g.id, g.value); err != nil {
				return err
			}
			r.FromCatalog++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM track WHERE genre IS NULL`).Scan(&r.Without)
	return r, err
}

// DurationReport counts what a duration backfill did.
type DurationReport struct {
	FromFiles        int // tracks measured from their audio
	Unreadable       int // tracks with audio that couldn't be measured
	FromCatalog      int // tracks given the catalog's figure, having no audio
	Without          int // tracks still without one
	CatalogDisagrees int // measured tracks whose catalog entry says something else
}

// Duration sets the duration of every track that has none: measured
// from its file under the library for a track with audio, and taken
// from the catalog for a track without.  Where a measured track has a
// catalog entry, the two are compared, and a difference of more than
// two seconds is counted, as a check on the measuring.
func Duration(ctx context.Context, db *sql.DB, library string, catalog *sql.DB) (*DurationReport, error) {
	r := &DurationReport{}
	err := fill(ctx, db, "duration", func(ctx context.Context, tx *sql.Tx, set func(id string, value any) error) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, path, content_type FROM track WHERE duration IS NULL AND path IS NOT NULL`)
		if err != nil {
			return err
		}
		type file struct{ id, path, contentType string }
		var files []file
		for rows.Next() {
			var f file
			if err := rows.Scan(&f.id, &f.path, &f.contentType); err != nil {
				rows.Close()
				return err
			}
			files = append(files, f)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, f := range files {
			seconds, err := measure(filepath.Join(library, filepath.FromSlash(f.path)), f.contentType)
			if err != nil {
				r.Unreadable++
				continue
			}
			if err := set(f.id, seconds); err != nil {
				return err
			}
			r.FromFiles++
		}
		if catalog == nil {
			return nil
		}
		fromCatalog, err := matchCatalog(ctx, tx, catalog, `t.duration IS NULL AND t.path IS NULL`, `total_time`)
		if err != nil {
			return err
		}
		for _, g := range fromCatalog {
			ms, err := strconv.ParseFloat(g.value, 64)
			if err != nil {
				continue
			}
			if err := set(g.id, int64(math.Round(ms/1000))); err != nil {
				return err
			}
			r.FromCatalog++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM track WHERE duration IS NULL`).Scan(&r.Without); err != nil {
		return nil, err
	}
	if catalog != nil {
		r.CatalogDisagrees, err = disagreements(ctx, db, catalog)
	}
	return r, err
}

// fill runs one backfill in a transaction, giving every track it sets
// the same new modseq.
func fill(ctx context.Context, db *sql.DB, column string, run func(context.Context, *sql.Tx, func(string, any) error) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	modseq, err := store.NextModseq(ctx, tx)
	if err != nil {
		return err
	}
	update, err := tx.PrepareContext(ctx, `UPDATE track SET `+column+` = ?, modseq = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer update.Close()
	set := func(id string, value any) error {
		_, err := update.ExecContext(ctx, value, modseq, id)
		return err
	}
	if err := run(ctx, tx, set); err != nil {
		return err
	}
	return tx.Commit()
}

// measure returns a file's length in whole seconds.
func measure(path, contentType string) (int64, error) {
	fh, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer fh.Close()
	d, err := audio.Duration(fh, contentType)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(d.Seconds())), nil
}

type trackValue struct{ id, value string }

func genresFromTags(ctx context.Context, q store.Queryer) ([]trackValue, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, original_tags FROM track WHERE genre IS NULL AND original_tags IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []trackValue
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("track %s: original tags: %w", id, err)
		}
		if g := tags.Genre(m); g != "" {
			out = append(out, trackValue{id, g})
		}
	}
	return out, rows.Err()
}

// A catalog track is matched by title, album title and track number.
type catalogKey struct {
	title, album string
	number       int
}

// catalogColumn reads one column of the catalog's audio tracks into a
// map by key, keeping every value found for a key so an ambiguous match
// can be refused.  The catalog is read once: itunes_audio_track is a
// view that dedupes with a correlated subquery, far too slow to consult
// once per track. -- claude, 2026-11-01
func catalogColumn(ctx context.Context, catalog *sql.DB, column string) (map[catalogKey][]string, error) {
	byKey := map[catalogKey][]string{}
	rows, err := catalog.QueryContext(ctx, `
		SELECT name, album, track_number, `+column+` FROM itunes_audio_track
		WHERE `+column+` IS NOT NULL AND `+column+` != '' AND name IS NOT NULL AND album IS NOT NULL AND track_number IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k catalogKey
		var value string
		if err := rows.Scan(&k.title, &k.album, &k.number, &value); err != nil {
			return nil, err
		}
		byKey[k] = append(byKey[k], value)
	}
	return byKey, rows.Err()
}

// matchCatalog matches each track on an album that satisfies the SQL
// condition to the 2014 catalog, and returns the catalog column's value
// for those with exactly one.  At import the match went through the
// file's path, which the originals move has since rewritten, so this is
// the match that remains; it is exact.
func matchCatalog(ctx context.Context, q store.Queryer, catalog *sql.DB, where, column string) ([]trackValue, error) {
	byKey, err := catalogColumn(ctx, catalog, column)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT t.id, t.title, a.title, t.position FROM track t JOIN album a ON a.id = t.album_id
		WHERE t.position IS NOT NULL AND (`+where+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []trackValue
	for rows.Next() {
		var id string
		var k catalogKey
		if err := rows.Scan(&id, &k.title, &k.album, &k.number); err != nil {
			return nil, err
		}
		if values := byKey[k]; len(values) == 1 {
			out = append(out, trackValue{id, values[0]})
		}
	}
	return out, rows.Err()
}

// disagreements counts the measured tracks whose catalog entry puts
// their length more than two seconds away.
func disagreements(ctx context.Context, db *sql.DB, catalog *sql.DB) (int, error) {
	matches, err := matchCatalog(ctx, db, catalog, `t.duration IS NOT NULL AND t.path IS NOT NULL`, `total_time`)
	if err != nil {
		return 0, err
	}
	catalogSeconds := map[string]float64{}
	for _, m := range matches {
		if ms, err := strconv.ParseFloat(m.value, 64); err == nil {
			catalogSeconds[m.id] = ms / 1000
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT id, duration FROM track WHERE duration IS NOT NULL AND path IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id string
		var seconds float64
		if err := rows.Scan(&id, &seconds); err != nil {
			return 0, err
		}
		if want, ok := catalogSeconds[id]; ok && math.Abs(want-seconds) > 2 {
			n++
		}
	}
	return n, rows.Err()
}
