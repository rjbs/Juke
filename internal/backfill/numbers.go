package backfill

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"semiotic.systems/juke/internal/store"
	"semiotic.systems/juke/internal/tags"
)

// NumbersReport counts what a track-number backfill did.
type NumbersReport struct {
	Placed  int // tracks moved to the number their filename gives
	Taken   int // tracks whose filename number another track holds
	Unnamed int // tracks with no number anywhere, filename included
	Albums  int // albums whose order changed
	Sourced int // tracks told where their number came from
}

// Numbers places tracks that were numbered from nowhere.  The import
// put a track with no number in its tags or the catalog after its
// disc's highest, in filename order, though the filename usually
// starts with the number, "NN Title", as iTunes wrote it (#6).  This
// moves such a track to its filename's number when that spot is free,
// and leaves it where it is when another track holds the spot.  Disc
// numbers in filenames are left alone; a track keeps its disc.
//
// A track whose file has been moved to the originals store has lost its
// name from its path; the move's journal, if given, still has it.  Every
// track moved gets the same new modseq, and so does each album changed,
// since its track order is the album's.
//
// It also records where each album track's number came from, for the
// review page, where an older import didn't: the tags, the catalog or
// the filename, whichever gives the number the track has, or "none".
// A gap track's number is the catalog's, if the catalog says so; with no
// catalog, nothing can be said of one.  No modseq: clients don't see it.
func Numbers(ctx context.Context, db *sql.DB, catalog *sql.DB, journal string) (*NumbersReport, error) {
	r := &NumbersReport{}
	names, err := originalNames(journal)
	if err != nil {
		return nil, err
	}
	var catalogNumbers map[catalogKey]int
	if catalog != nil {
		if catalogNumbers, err = numbersFromCatalog(ctx, catalog); err != nil {
			return nil, err
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	albums, err := loadAlbumTracks(ctx, tx)
	if err != nil {
		return nil, err
	}

	var modseq int64
	albumIDs := make([]string, 0, len(albums))
	for id := range albums {
		albumIDs = append(albumIDs, id)
	}
	sort.Strings(albumIDs)
	for _, albumID := range albumIDs {
		ts := albums[albumID]
		var movers []placement
		for _, t := range ts {
			if t.source == "" {
				if source := t.numberSource(catalogNumbers, names); source != "" {
					if _, err := tx.ExecContext(ctx, `UPDATE track SET number_source = ? WHERE id = ?`, source, t.id); err != nil {
						return nil, err
					}
					r.Sourced++
				}
			}
			if t.path == "" || t.numbered(catalogNumbers) {
				continue
			}
			_, n := tags.NumbersFromName(t.name(names))
			if n <= 0 {
				r.Unnamed++
				continue
			}
			if n != t.position {
				movers = append(movers, placement{t, n})
			}
		}
		placed := settle(ts, movers)
		r.Taken += len(movers) - len(placed)
		if len(placed) == 0 {
			continue
		}
		if modseq == 0 {
			if modseq, err = store.NextModseq(ctx, tx); err != nil {
				return nil, err
			}
		}
		// Movers may trade spots, so each leaves its own first.  A
		// negative position is unique and brief: the second update
		// follows in the same transaction.
		for _, p := range placed {
			if _, err := tx.ExecContext(ctx, `UPDATE track SET position = ? WHERE id = ?`, -p.track.position, p.track.id); err != nil {
				return nil, err
			}
		}
		for _, p := range placed {
			if _, err := tx.ExecContext(ctx, `UPDATE track SET position = ?, number_source = 'file', modseq = ? WHERE id = ?`, p.number, modseq, p.track.id); err != nil {
				return nil, err
			}
			r.Placed++
		}
		if _, err := tx.ExecContext(ctx, `UPDATE album SET modseq = ? WHERE id = ?`, modseq, albumID); err != nil {
			return nil, err
		}
		r.Albums++
	}
	return r, tx.Commit()
}

type slot struct{ disc, position int }

type placement struct {
	track  *albumTrack
	number int
}

// settle decides which movers get the spot they want.  A spot held by a
// track staying put is not to be had; a spot two movers want goes to
// the first by current position; and a mover refused keeps its spot, so
// a mover wanting that one is refused in turn.  Movers may trade spots
// among themselves.
func settle(all []*albumTrack, movers []placement) []placement {
	moving := map[*albumTrack]bool{}
	for _, p := range movers {
		moving[p.track] = true
	}
	refused := map[*albumTrack]bool{}
	for changed := true; changed; {
		changed = false
		held := map[slot]bool{}
		for _, t := range all {
			if !moving[t] || refused[t] {
				held[slot{t.disc, t.position}] = true
			}
		}
		claimed := map[slot]bool{}
		for _, p := range movers {
			if refused[p.track] {
				continue
			}
			s := slot{p.track.disc, p.number}
			if held[s] || claimed[s] {
				refused[p.track] = true
				changed = true
				continue
			}
			claimed[s] = true
		}
	}
	var placed []placement
	for _, p := range movers {
		if !refused[p.track] {
			placed = append(placed, p)
		}
	}
	return placed
}

// An albumTrack is one track on an album, with what's needed to say
// whether anything numbered it.
type albumTrack struct {
	id, title, album, path string
	disc, position         int
	source                 string         // number_source as stored, "" for NULL
	raw                    map[string]any // the stored tags, or nil
}

// name is the filename the import saw: the journal's, for a file the
// originals move renamed, else the path's.
func (t *albumTrack) name(names map[string]string) string {
	if name := names[t.id]; name != "" {
		return name
	}
	return path.Base(t.path)
}

// numberSource says what gave the track the number it has, or "" when
// that can't be told: a gap track the catalog doesn't number, or any
// track the tags and filename don't when there is no catalog to ask,
// since the catalog may have numbered it at import.
func (t *albumTrack) numberSource(catalogNumbers map[catalogKey]int, names map[string]string) string {
	catalog := catalogNumbers[catalogKey{title: t.title, album: t.album}]
	if t.path == "" {
		if catalog == t.position {
			return "catalog"
		}
		return ""
	}
	if t.raw != nil && tags.TrackNumber(t.raw) == t.position {
		return "tags"
	}
	if catalog == t.position {
		return "catalog"
	}
	if _, n := tags.NumbersFromName(t.name(names)); n == t.position {
		return "file"
	}
	if catalogNumbers == nil {
		return ""
	}
	return "none"
}

// numbered says whether the track's tags or the catalog give it a
// number.  The catalog match is by title and album, which is what
// remains once the originals move has rewritten the path the import
// matched by.
func (t *albumTrack) numbered(catalogNumbers map[catalogKey]int) bool {
	if t.raw != nil && tags.TrackNumber(t.raw) > 0 {
		return true
	}
	return catalogNumbers[catalogKey{title: t.title, album: t.album}] > 0
}

// loadAlbumTracks reads every album's tracks, gaps included, since a gap
// holds a spot too.
func loadAlbumTracks(ctx context.Context, q store.Queryer) (map[string][]*albumTrack, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT t.id, t.title, a.title, t.album_id, t.path, t.disc_index, t.position, t.number_source, t.original_tags
		FROM track t JOIN album a ON a.id = t.album_id
		WHERE t.position IS NOT NULL ORDER BY t.album_id, t.disc_index, t.position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]*albumTrack{}
	for rows.Next() {
		var t albumTrack
		var albumID string
		var p, source, raw sql.NullString
		if err := rows.Scan(&t.id, &t.title, &t.album, &albumID, &p, &t.disc, &t.position, &source, &raw); err != nil {
			return nil, err
		}
		t.path, t.source = p.String, source.String
		if raw.Valid {
			if err := json.Unmarshal([]byte(raw.String), &t.raw); err != nil {
				return nil, fmt.Errorf("track %s: original tags: %w", t.id, err)
			}
		}
		out[albumID] = append(out[albumID], &t)
	}
	return out, rows.Err()
}

// numbersFromCatalog reads the catalog's track numbers by title and
// album, keeping only the keys with one number, so an ambiguous match
// counts as none.
func numbersFromCatalog(ctx context.Context, catalog *sql.DB) (map[catalogKey]int, error) {
	rows, err := catalog.QueryContext(ctx, `
		SELECT name, album, track_number FROM itunes_audio_track
		WHERE track_number > 0 AND name IS NOT NULL AND album IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[catalogKey]int{}
	for rows.Next() {
		var k catalogKey
		var n int
		if err := rows.Scan(&k.title, &k.album, &n); err != nil {
			return nil, err
		}
		switch out[k] {
		case 0:
			out[k] = n
		case n:
		default:
			out[k] = -1
		}
	}
	return out, rows.Err()
}

// originalNames reads the originals move's journal, if there is one,
// for the filename each moved track had before: the first move recorded
// for a track is from the name the import saw.
func originalNames(journal string) (map[string]string, error) {
	out := map[string]string{}
	if journal == "" {
		return out, nil
	}
	f, err := os.Open(journal)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 3 {
			continue
		}
		if _, seen := out[fields[0]]; !seen {
			out[fields[0]] = path.Base(fields[1])
		}
	}
	return out, scanner.Err()
}
