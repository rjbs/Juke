package backfill

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"semiotic.systems/juke/internal/store"
	"semiotic.systems/juke/internal/tags"
)

// ReleasedReport counts what a release date backfill did.
type ReleasedReport struct {
	FromTags    int // albums dated from their tracks' stored tags
	FromCatalog int // albums dated from the catalog's year
	Without     int // albums still without a date
}

// Released sets the release date of every album that has none: the
// date most of its tracks' stored tags give, or else, when a catalog is
// given, the year most of its tracks have there.  Every album changed
// gets the same new modseq, so clients refetch them once.
func Released(ctx context.Context, db *sql.DB, catalog *sql.DB) (*ReleasedReport, error) {
	r := &ReleasedReport{}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	modseq, err := store.NextModseq(ctx, tx)
	if err != nil {
		return nil, err
	}
	set := func(albumID, released string) error {
		_, err := tx.ExecContext(ctx, `UPDATE album SET released = ?, modseq = ? WHERE id = ?`, released, modseq, albumID)
		return err
	}

	fromTags, err := releasesFromTags(ctx, tx)
	if err != nil {
		return nil, err
	}
	for album, values := range fromTags {
		if err := set(album, tags.CommonReleased(values)); err != nil {
			return nil, err
		}
		r.FromTags++
	}
	if catalog != nil {
		fromCatalog, err := releasesFromCatalog(ctx, tx, catalog)
		if err != nil {
			return nil, err
		}
		for album, values := range fromCatalog {
			if err := set(album, tags.CommonReleased(values)); err != nil {
				return nil, err
			}
			r.FromCatalog++
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM album WHERE released IS NULL`).Scan(&r.Without)
	return r, err
}

// releasesFromTags gives, for each undated album, the dates its tracks'
// stored tags hold.
func releasesFromTags(ctx context.Context, q store.Queryer) (map[string][]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT t.album_id, t.original_tags FROM track t JOIN album a ON a.id = t.album_id
		WHERE a.released IS NULL AND t.original_tags IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var album, raw string
		if err := rows.Scan(&album, &raw); err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("album %s: a track's original tags: %w", album, err)
		}
		if d := tags.Released(m); d != "" {
			out[album] = append(out[album], d)
		}
	}
	return out, rows.Err()
}

// releasesFromCatalog gives, for each album still undated, the years
// the catalog has for its tracks, matched as matchCatalog matches them.
func releasesFromCatalog(ctx context.Context, q store.Queryer, catalog *sql.DB) (map[string][]string, error) {
	byKey, err := catalogColumn(ctx, catalog, "year")
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT t.album_id, t.title, a.title, t.position FROM track t JOIN album a ON a.id = t.album_id
		WHERE a.released IS NULL AND t.position IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var album string
		var k catalogKey
		if err := rows.Scan(&album, &k.title, &k.album, &k.number); err != nil {
			return nil, err
		}
		if values := byKey[k]; len(values) == 1 && tags.ValidReleased(values[0]+"-00-00") {
			out[album] = append(out[album], values[0]+"-00-00")
		}
	}
	return out, rows.Err()
}
