package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// NewAlbum describes an album to insert.  Each entry in DiscTitles is
// one disc, so its length is the number of discs; a nil entry is a disc
// with no title.  DiscTrackCounts gives the same discs' declared numbers
// of tracks; it may be shorter, and a nil entry or a missing one is a
// disc with no declared total.
type NewAlbum struct {
	Title           string
	Artist          *string
	DiscTitles      []*string
	DiscTrackCounts []*int
}

// InsertAlbum inserts an album and its discs, and returns its new id.
func InsertAlbum(ctx context.Context, q Queryer, a NewAlbum) (string, error) {
	id, err := NewID("A")
	if err != nil {
		return "", err
	}
	modseq, err := NextModseq(ctx, q)
	if err != nil {
		return "", err
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO album (id, title, artist, created_modseq, modseq) VALUES (?, ?, ?, ?, ?)`,
		id, a.Title, a.Artist, modseq, modseq,
	); err != nil {
		return "", err
	}
	for i, title := range a.DiscTitles {
		var count *int
		if i < len(a.DiscTrackCounts) {
			count = a.DiscTrackCounts[i]
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO disc (album_id, disc_index, title, track_count) VALUES (?, ?, ?, ?)`, id, i, title, count,
		); err != nil {
			return "", err
		}
	}
	return id, nil
}

// NewTrack describes a track to insert.  A track with a nil Path has no
// audio: it stands for a gap on a damaged album, and gets no blobId.
type NewTrack struct {
	Title        string
	Artist       *string
	Genre        *string
	Duration     *int64
	AlbumID      *string
	DiscIndex    *int
	Position     *int
	Rating       *float64
	PlayCount    int64
	LastPlayed   *string
	DateAdded    string
	Path         *string
	Size         *int64
	ContentType  *string
	TagFormat    *string
	OriginalTags *string
}

// InsertTrack inserts a track and returns its new id.
func InsertTrack(ctx context.Context, q Queryer, t NewTrack) (string, error) {
	id, err := NewID("T")
	if err != nil {
		return "", err
	}
	modseq, err := NextModseq(ctx, q)
	if err != nil {
		return "", err
	}
	var blobID *string
	if t.Path != nil {
		b := BlobID(id, 1, "original")
		blobID = &b
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO track (
		  id, title, artist, genre, duration, album_id, disc_index, position,
		  rating, play_count, last_played, date_added,
		  blob_id, path, size, content_type, tag_format, original_tags,
		  created_modseq, modseq
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, t.Title, t.Artist, t.Genre, t.Duration, t.AlbumID, t.DiscIndex, t.Position,
		t.Rating, t.PlayCount, t.LastPlayed, t.DateAdded,
		blobID, t.Path, t.Size, t.ContentType, t.TagFormat, t.OriginalTags,
		modseq, modseq,
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

// InsertPlaylist inserts a plain playlist and returns its new id.
func InsertPlaylist(ctx context.Context, q Queryer, title string, trackIDs []string) (string, error) {
	id, err := NewID("P")
	if err != nil {
		return "", err
	}
	modseq, err := NextModseq(ctx, q)
	if err != nil {
		return "", err
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO playlist (id, title, created_modseq, modseq) VALUES (?, ?, ?, ?)`,
		id, title, modseq, modseq,
	); err != nil {
		return "", err
	}
	if err := SetPlaylistTracks(ctx, q, id, trackIDs); err != nil {
		return "", err
	}
	return id, nil
}

// SetPlaylistTracks replaces a playlist's tracks.  It doesn't touch the
// playlist's modseq; the caller does that along with whatever else it
// changes.
func SetPlaylistTracks(ctx context.Context, q Queryer, playlistID string, trackIDs []string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM playlist_track WHERE playlist_id = ?`, playlistID); err != nil {
		return err
	}
	for i, trackID := range trackIDs {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO playlist_track (playlist_id, position, track_id) VALUES (?, ?, ?)`,
			playlistID, i, trackID,
		); err != nil {
			return err
		}
	}
	return nil
}

// Destroy deletes an object's row and leaves a tombstone, so /changes
// can report it destroyed.  Rows that refer to the object must be dealt
// with first.
func Destroy(ctx context.Context, q Queryer, typ, id string) error {
	table, ok := tableFor[typ]
	if !ok {
		return fmt.Errorf("no such type %q", typ)
	}
	modseq, err := NextModseq(ctx, q)
	if err != nil {
		return err
	}
	var created int64
	if err := q.QueryRowContext(ctx,
		`DELETE FROM `+table+` WHERE id = ? RETURNING created_modseq`, id,
	).Scan(&created); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx,
		`INSERT INTO tombstone (type, id, created_modseq, modseq) VALUES (?, ?, ?, ?)`,
		typ, id, created, modseq,
	)
	return err
}

// Disc is one disc of an album, as SetDiscs writes it.
type Disc struct {
	Title    *string
	TrackIDs []string
}

// SetDiscs replaces an album's discs and the tracks on them.  A track
// that joins the album leaves the album it was on; a track that leaves
// it is left on no album.  Each track whose album changes, and each
// other album that loses a track, gets a new modseq, so /changes reports
// them.  The album's own modseq is the caller's to change.
//
// The track ids must exist, and appear only once in discs.
//
// Positions are track numbers, and discs carries no gaps, so each disc's
// tracks are numbered from 1 in the order given, closing any gap the
// album had; each disc keeps the declared track count of the disc that
// had its index.  Until Album/set can express gaps (#40), that's the most
// it can say.  -- claude, 2026-11-01
func SetDiscs(ctx context.Context, q Queryer, albumID string, discs []Disc) error {
	var joining []string
	for _, d := range discs {
		joining = append(joining, d.TrackIDs...)
	}
	in := "0"
	args := []any{albumID}
	if len(joining) > 0 {
		in = "id IN (?" + strings.Repeat(", ?", len(joining)-1) + ")"
		for _, id := range joining {
			args = append(args, id)
		}
	}
	affected := `album_id = ? OR ` + in

	// Where each affected track was, keyed by track id; "" is no album.
	before := map[string]string{}
	rows, err := q.QueryContext(ctx, `SELECT id, coalesce(album_id, '') FROM track WHERE `+affected, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, was string
		if err := rows.Scan(&id, &was); err != nil {
			rows.Close()
			return err
		}
		before[id] = was
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Every affected track is taken off its album before any is placed,
	// so a new position can't collide with an old one under the unique
	// constraint on (album_id, disc_index, position).  -- claude, 2026-10-25
	if _, err := q.ExecContext(ctx,
		`UPDATE track SET album_id = NULL, disc_index = NULL, position = NULL WHERE `+affected, args...,
	); err != nil {
		return err
	}
	counts := map[int]*int{}
	crows, err := q.QueryContext(ctx, `SELECT disc_index, track_count FROM disc WHERE album_id = ?`, albumID)
	if err != nil {
		return err
	}
	for crows.Next() {
		var di int
		var count *int
		if err := crows.Scan(&di, &count); err != nil {
			crows.Close()
			return err
		}
		counts[di] = count
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM disc WHERE album_id = ?`, albumID); err != nil {
		return err
	}
	after := map[string]string{}
	for di, d := range discs {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO disc (album_id, disc_index, title, track_count) VALUES (?, ?, ?, ?)`, albumID, di, d.Title, counts[di],
		); err != nil {
			return err
		}
		for pos, id := range d.TrackIDs {
			if _, err := q.ExecContext(ctx,
				`UPDATE track SET album_id = ?, disc_index = ?, position = ? WHERE id = ?`, albumID, di, pos+1, id,
			); err != nil {
				return err
			}
			after[id] = albumID
		}
	}

	losers := map[string]bool{}
	for _, id := range sortedKeys(before) {
		was := before[id]
		if was == after[id] {
			continue
		}
		if err := touch(ctx, q, "track", id); err != nil {
			return err
		}
		if was != "" && was != albumID {
			losers[was] = true
		}
	}
	for _, id := range sortedKeys(losers) {
		if err := touch(ctx, q, "album", id); err != nil {
			return err
		}
	}
	return nil
}

// touch gives a row a new modseq.
func touch(ctx context.Context, q Queryer, table, id string) error {
	modseq, err := NextModseq(ctx, q)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE `+table+` SET modseq = ? WHERE id = ?`, modseq, id)
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SmartQuery is the Track/query a smart playlist stores, as the JSON the
// client sent: Filter is nil for none, Sort is always an array.
type SmartQuery struct {
	Filter       *string
	Sort         string
	Limit        *int64
	LimitBytes   *int64
	LimitSeconds *int64
}

// InsertSmartPlaylist inserts a smart playlist and returns its new id.
func InsertSmartPlaylist(ctx context.Context, q Queryer, title string, sq SmartQuery) (string, error) {
	id, err := NewID("S")
	if err != nil {
		return "", err
	}
	modseq, err := NextModseq(ctx, q)
	if err != nil {
		return "", err
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO smart_playlist (id, title, filter, sort, "limit", limit_bytes, limit_seconds, created_modseq, modseq)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, title, sq.Filter, sq.Sort, sq.Limit, sq.LimitBytes, sq.LimitSeconds, modseq, modseq,
	)
	return id, err
}

// WriteSmartPlaylist replaces a smart playlist's title and query, and
// gives it a new modseq.
func WriteSmartPlaylist(ctx context.Context, q Queryer, id, title string, sq SmartQuery) error {
	modseq, err := NextModseq(ctx, q)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
		UPDATE smart_playlist SET title = ?, filter = ?, sort = ?, "limit" = ?, limit_bytes = ?, limit_seconds = ?, modseq = ?
		WHERE id = ?`,
		title, sq.Filter, sq.Sort, sq.Limit, sq.LimitBytes, sq.LimitSeconds, modseq, id,
	)
	return err
}

// LoadSmartPlaylist reads a smart playlist's title and query, or returns
// sql.ErrNoRows.
func LoadSmartPlaylist(ctx context.Context, q Queryer, id string) (string, SmartQuery, error) {
	var title string
	var sq SmartQuery
	err := q.QueryRowContext(ctx,
		`SELECT title, filter, sort, "limit", limit_bytes, limit_seconds FROM smart_playlist WHERE id = ?`, id,
	).Scan(&title, &sq.Filter, &sq.Sort, &sq.Limit, &sq.LimitBytes, &sq.LimitSeconds)
	return title, sq, err
}

// Ref is a playlist of either kind that a smart playlist's rule names.
type Ref struct {
	Type string // TypePlaylist or TypeSmartPlaylist
	ID   string
}

// SetSmartPlaylistRefs records what a smart playlist's rule names,
// replacing what was recorded before.
func SetSmartPlaylistRefs(ctx context.Context, q Queryer, id string, refs []Ref) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM smart_playlist_ref WHERE smart_playlist_id = ?`, id); err != nil {
		return err
	}
	for _, r := range refs {
		if _, err := q.ExecContext(ctx,
			`INSERT OR IGNORE INTO smart_playlist_ref (smart_playlist_id, ref_type, ref_id) VALUES (?, ?, ?)`, id, r.Type, r.ID,
		); err != nil {
			return err
		}
	}
	return nil
}

// SmartPlaylistRefs returns the smart playlists whose rules name the
// given smart playlist id, directly.
func SmartPlaylistRefs(ctx context.Context, q Queryer, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT ref_id FROM smart_playlist_ref WHERE smart_playlist_id = ? AND ref_type = ?`, id, TypeSmartPlaylist)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// Referenced reports whether any smart playlist's rule names the object.
func Referenced(ctx context.Context, q Queryer, typ, id string) (bool, error) {
	var found bool
	err := q.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM smart_playlist_ref WHERE ref_type = ? AND ref_id = ?)`, typ, id,
	).Scan(&found)
	return found, err
}
