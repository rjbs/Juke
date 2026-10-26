package store

import (
	"context"
	"fmt"
)

// NewAlbum describes an album to insert.  Each entry in DiscTitles is
// one disc, so its length is the number of discs; a nil entry is a disc
// with no title.
type NewAlbum struct {
	Title      string
	Artist     *string
	DiscTitles []*string
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
		if _, err := q.ExecContext(ctx,
			`INSERT INTO disc (album_id, disc_index, title) VALUES (?, ?, ?)`, id, i, title,
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
		  id, title, artist, album_id, disc_index, position,
		  rating, play_count, last_played, date_added,
		  blob_id, path, size, content_type, tag_format, original_tags,
		  created_modseq, modseq
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, t.Title, t.Artist, t.AlbumID, t.DiscIndex, t.Position,
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
