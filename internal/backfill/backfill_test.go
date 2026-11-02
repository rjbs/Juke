package backfill

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"semiotic.systems/juke/internal/store"
)

type track struct {
	title    string
	position int    // 0 for a track on no album
	genre    string // "" for none
	tags     string // "" for a gap track
}

// setup makes a database with one album, "Record", holding the given
// tracks, and a catalog whose only table is the view the backfill reads.
func setup(t *testing.T, tracks []track, catalogRows string) (*sql.DB, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.Create(filepath.Join(dir, "juke.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	albumID, err := store.InsertAlbum(ctx, s.DB, store.NewAlbum{Title: "Record"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range tracks {
		nt := store.NewTrack{Title: tr.title, DateAdded: "2014-01-01T00:00:00Z"}
		if tr.position > 0 {
			pos := tr.position
			nt.AlbumID, nt.Position = &albumID, &pos
		}
		if tr.genre != "" {
			nt.Genre = &tr.genre
		}
		if tr.tags != "" {
			p, f := tr.title+".mp3", "ID3v2.3"
			nt.Path, nt.TagFormat, nt.OriginalTags = &p, &f, &tr.tags
		}
		if _, err := store.InsertTrack(ctx, s.DB, nt); err != nil {
			t.Fatal(err)
		}
	}

	catalog, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { catalog.Close() })
	if _, err := catalog.Exec(`
		CREATE TABLE itunes_audio_track (name TEXT, album TEXT, track_number INTEGER, genre TEXT);
		INSERT INTO itunes_audio_track VALUES ` + catalogRows); err != nil {
		t.Fatal(err)
	}
	return s.DB, catalog
}

func checkGenre(t *testing.T, db *sql.DB, title string, want string, wantModseq int64) {
	t.Helper()
	var got sql.NullString
	var modseq int64
	if err := db.QueryRow(`SELECT genre, modseq FROM track WHERE title = ?`, title).Scan(&got, &modseq); err != nil {
		t.Fatal(err)
	}
	if got.String != want || modseq != wantModseq {
		t.Errorf("%s: genre %q at modseq %d, want %q at %d", title, got.String, modseq, want, wantModseq)
	}
}

func TestGenre(t *testing.T) {
	db, catalog := setup(t, []track{
		{title: "Tagged", position: 1, tags: `{"TIT2":"Tagged","TCON":"(17)"}`},
		{title: "Already", position: 2, genre: "Kept", tags: `{"TCON":"Ignored"}`},
		{title: "Silent", position: 3, tags: `{"TIT2":"Silent"}`},
		{title: "Gap", position: 4},
		{title: "Twice", position: 5},
		{title: "Nowhere", position: 6, tags: `{}`},
		{title: "Loose", tags: `{"TCON":"Folk"}`},
	}, `
		('Silent', 'Record', 3, 'Jazz'),
		('Gap', 'Record', 4, 'Blues'),
		('Gap', 'Other', 4, 'Wrong album'),
		('Twice', 'Record', 5, 'One'),
		('Twice', 'Record', 5, 'Another'),
		('Nowhere', 'Record', 6, '')`)

	ctx := context.Background()
	before, err := store.TypeModseq(ctx, db, store.TypeTrack)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Genre(ctx, db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if r.FromTags != 2 || r.FromCatalog != 2 || r.Without != 2 {
		t.Errorf("report = %+v, want 2 from tags, 2 from the catalog, 2 without", r)
	}

	after := before + 1
	checkGenre(t, db, "Tagged", "Rock", after)
	checkGenre(t, db, "Loose", "Folk", after)
	checkGenre(t, db, "Silent", "Jazz", after)
	checkGenre(t, db, "Gap", "Blues", after)
	checkGenre(t, db, "Already", "Kept", before-5)
	checkGenre(t, db, "Twice", "", before-2)
	checkGenre(t, db, "Nowhere", "", before-1)

	// Running again changes nothing.
	r, err = Genre(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.FromTags != 0 || r.FromCatalog != 0 || r.Without != 2 {
		t.Errorf("second report = %+v, want nothing filled", r)
	}
	checkGenre(t, db, "Tagged", "Rock", after)
}
