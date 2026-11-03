package backfill

import (
	"context"
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"semiotic.systems/juke/internal/store"
)

type track struct {
	title    string
	position int    // 0 for a track on no album
	genre    string // "" for none
	duration int64  // 0 for none
	tags     string // "" for a gap track
	audio    []byte // the file's bytes, for a track with audio
	file     string // the file's path under the library; "" for the title
}

// mp3 makes n frames of 128 kbps MPEG-1 Layer III audio behind a Xing
// header that counts them: each frame is 1152 samples at 44.1 kHz, so
// 1000 frames play for 26 seconds.
func mp3(n int) []byte {
	frame := make([]byte, 417)
	copy(frame, []byte{0xFF, 0xFB, 0x90, 0x00})
	first := append([]byte{}, frame...)
	copy(first[36:], "Xing")
	binary.BigEndian.PutUint32(first[40:], 1)
	binary.BigEndian.PutUint32(first[44:], uint32(n))
	out := first
	for i := 0; i < n; i++ {
		out = append(out, frame...)
	}
	return out
}

// setup makes a database with one album, "Record", holding the given
// tracks, a library holding their audio, and a catalog whose only table
// stands in for the view the backfill reads.
func setup(t *testing.T, tracks []track, catalogRows string) (*sql.DB, string, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	library := filepath.Join(dir, "library")
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatal(err)
	}
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
			pos, disc := tr.position, 0
			nt.AlbumID, nt.DiscIndex, nt.Position = &albumID, &disc, &pos
		}
		if tr.genre != "" {
			nt.Genre = &tr.genre
		}
		if tr.duration != 0 {
			nt.Duration = &tr.duration
		}
		if tr.tags != "" {
			p, f, ct := tr.title+".mp3", "ID3v2.3", "audio/mpeg"
			if tr.file != "" {
				p = tr.file
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(library, p)), 0o755); err != nil {
				t.Fatal(err)
			}
			nt.Path, nt.TagFormat, nt.OriginalTags, nt.ContentType = &p, &f, &tr.tags, &ct
			if err := os.WriteFile(filepath.Join(library, p), tr.audio, 0o644); err != nil {
				t.Fatal(err)
			}
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
		CREATE TABLE itunes_audio_track (name TEXT, album TEXT, track_number INTEGER, genre TEXT, total_time INTEGER);
		INSERT INTO itunes_audio_track VALUES ` + catalogRows); err != nil {
		t.Fatal(err)
	}
	return s.DB, library, catalog
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

func checkDuration(t *testing.T, db *sql.DB, title string, want int64, wantModseq int64) {
	t.Helper()
	var got sql.NullInt64
	var modseq int64
	if err := db.QueryRow(`SELECT duration, modseq FROM track WHERE title = ?`, title).Scan(&got, &modseq); err != nil {
		t.Fatal(err)
	}
	if got.Int64 != want || (want != 0 && !got.Valid) || modseq != wantModseq {
		t.Errorf("%s: duration %v at modseq %d, want %d at %d", title, got, modseq, want, wantModseq)
	}
}

func TestGenre(t *testing.T) {
	db, _, catalog := setup(t, []track{
		{title: "Tagged", position: 1, tags: `{"TIT2":"Tagged","TCON":"(17)"}`},
		{title: "Already", position: 2, genre: "Kept", tags: `{"TCON":"Ignored"}`},
		{title: "Silent", position: 3, tags: `{"TIT2":"Silent"}`},
		{title: "Gap", position: 4},
		{title: "Twice", position: 5},
		{title: "Nowhere", position: 6, tags: `{}`},
		{title: "Loose", tags: `{"TCON":"Folk"}`},
	}, `
		('Silent', 'Record', 3, 'Jazz', NULL),
		('Gap', 'Record', 4, 'Blues', NULL),
		('Gap', 'Other', 4, 'Wrong album', NULL),
		('Twice', 'Record', 5, 'One', NULL),
		('Twice', 'Record', 5, 'Another', NULL),
		('Nowhere', 'Record', 6, '', NULL)`)

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

func TestDuration(t *testing.T) {
	db, library, catalog := setup(t, []track{
		{title: "Measured", position: 1, tags: `{}`, audio: mp3(1000)},
		{title: "Agrees", position: 2, tags: `{}`, audio: mp3(1000)},
		{title: "Disagrees", position: 3, tags: `{}`, audio: mp3(1000)},
		{title: "Already", position: 4, duration: 99, tags: `{}`, audio: mp3(1000)},
		{title: "Garbage", position: 5, tags: `{}`, audio: []byte("this is not audio, not even slightly")},
		{title: "Gap", position: 6},
		{title: "Unknown gap", position: 7},
	}, `
		('Agrees', 'Record', 2, NULL, 26500),
		('Disagrees', 'Record', 3, NULL, 90000),
		('Garbage', 'Record', 5, NULL, 45000),
		('Gap', 'Record', 6, NULL, 123456)`)

	ctx := context.Background()
	before, err := store.TypeModseq(ctx, db, store.TypeTrack)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Duration(ctx, db, library, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if r.FromFiles != 3 || r.Unmeasured != 1 || r.FromCatalog != 1 || r.Without != 2 || r.CatalogDisagrees != 1 {
		t.Errorf("report = %+v, want 3 from files, 1 unmeasured, 1 from the catalog, 2 without, 1 disagreeing", r)
	}

	after := before + 1
	checkDuration(t, db, "Measured", 26, after)
	checkDuration(t, db, "Agrees", 26, after)
	checkDuration(t, db, "Disagrees", 26, after)
	checkDuration(t, db, "Gap", 123, after)
	checkDuration(t, db, "Already", 99, before-3)
	checkDuration(t, db, "Garbage", 0, before-2)
	checkDuration(t, db, "Unknown gap", 0, before)

	// A file with audio the catalog knows but the measuring can't read
	// stays empty: the file is the truth for a track that has one.
	r, err = Duration(ctx, db, library, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if r.FromFiles != 0 || r.Unmeasured != 1 || r.FromCatalog != 0 || r.Without != 2 {
		t.Errorf("second report = %+v, want nothing filled", r)
	}

	// A file that can't be read at all fails the run, and changes nothing.
	if err := os.Remove(filepath.Join(library, "Garbage.mp3")); err != nil {
		t.Fatal(err)
	}
	if _, err := Duration(ctx, db, library, catalog); err == nil {
		t.Error("missing file: no error")
	}
	checkDuration(t, db, "Garbage", 0, before-2)
}
