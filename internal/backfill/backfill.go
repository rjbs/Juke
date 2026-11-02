// Package backfill fills in track properties that an import made before
// the property existed left empty.  The import stores every file's tags
// as found, so a property derived from tags can be filled from the
// database alone; the catalog fills what the tags don't say, as it does
// at import.  See doc/import.md.
package backfill

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"semiotic.systems/juke/internal/store"
	"semiotic.systems/juke/internal/tags"
)

// Report counts what a backfill did.
type Report struct {
	FromTags    int // tracks given a genre from their stored tags
	FromCatalog int // tracks given a genre from the catalog
	Without     int // tracks still without one
}

// Genre sets the genre of every track that has none, from its stored
// tags first and then, when a catalog is given, from the catalog track
// with the same title, album and track number, when exactly one has a
// genre.  Every track changed gets the same new modseq, so clients
// refetch them once.
func Genre(ctx context.Context, db *sql.DB, catalog *sql.DB) (*Report, error) {
	r := &Report{}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	modseq, err := store.NextModseq(ctx, tx)
	if err != nil {
		return nil, err
	}
	set, err := tx.PrepareContext(ctx, `UPDATE track SET genre = ?, modseq = ? WHERE id = ?`)
	if err != nil {
		return nil, err
	}
	defer set.Close()

	fromTags, err := genresFromTags(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, g := range fromTags {
		if _, err := set.ExecContext(ctx, g.genre, modseq, g.id); err != nil {
			return nil, err
		}
		r.FromTags++
	}

	if catalog != nil {
		fromCatalog, err := genresFromCatalog(ctx, tx, catalog)
		if err != nil {
			return nil, err
		}
		for _, g := range fromCatalog {
			if _, err := set.ExecContext(ctx, g.genre, modseq, g.id); err != nil {
				return nil, err
			}
			r.FromCatalog++
		}
	}

	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM track WHERE genre IS NULL`).Scan(&r.Without); err != nil {
		return nil, err
	}
	return r, tx.Commit()
}

type trackGenre struct{ id, genre string }

func genresFromTags(ctx context.Context, q store.Queryer) ([]trackGenre, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, original_tags FROM track WHERE genre IS NULL AND original_tags IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []trackGenre
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
			out = append(out, trackGenre{id, g})
		}
	}
	return out, rows.Err()
}

// genresFromCatalog matches each genre-less track on an album to the 2014
// catalog by title, album title and track number.  At import the match
// went through the file's path, which the originals move has since
// rewritten, so this is the match that remains; it is exact, and takes
// only a catalog track that is the sole one with a genre.  The catalog is
// read once into a map: itunes_audio_track is a view that dedupes with a
// correlated subquery, far too slow to consult once per track.
// -- claude, 2026-11-01
func genresFromCatalog(ctx context.Context, q store.Queryer, catalog *sql.DB) ([]trackGenre, error) {
	type key struct {
		title, album string
		number       int
	}
	byKey := map[key][]string{}
	found, err := catalog.QueryContext(ctx, `
		SELECT name, album, track_number, genre FROM itunes_audio_track
		WHERE genre IS NOT NULL AND genre != '' AND name IS NOT NULL AND album IS NOT NULL AND track_number IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	for found.Next() {
		var k key
		var genre string
		if err := found.Scan(&k.title, &k.album, &k.number, &genre); err != nil {
			found.Close()
			return nil, err
		}
		byKey[k] = append(byKey[k], genre)
	}
	found.Close()
	if err := found.Err(); err != nil {
		return nil, err
	}

	rows, err := q.QueryContext(ctx, `
		SELECT t.id, t.title, a.title, t.position FROM track t JOIN album a ON a.id = t.album_id
		WHERE t.genre IS NULL AND t.position IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []trackGenre
	for rows.Next() {
		var id string
		var k key
		if err := rows.Scan(&id, &k.title, &k.album, &k.number); err != nil {
			return nil, err
		}
		if genres := byKey[k]; len(genres) == 1 {
			out = append(out, trackGenre{id, genres[0]})
		}
	}
	return out, rows.Err()
}
