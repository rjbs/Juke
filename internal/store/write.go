package store

import (
	"context"
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
	for i, trackID := range trackIDs {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO playlist_track (playlist_id, position, track_id) VALUES (?, ?, ?)`,
			id, i, trackID,
		); err != nil {
			return "", err
		}
	}
	return id, nil
}
