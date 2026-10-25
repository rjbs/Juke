package importer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// id3 returns a minimal ID3v2.3 tag holding the given text frames,
// followed by some bytes standing in for audio.
func id3(frames map[string]string) []byte {
	var body bytes.Buffer
	for _, id := range []string{"TIT2", "TPE1", "TPE2", "TALB", "TRCK", "TPOS"} {
		text, ok := frames[id]
		if !ok {
			continue
		}
		body.WriteString(id)
		binary.Write(&body, binary.BigEndian, uint32(len(text)+1))
		body.Write([]byte{0, 0, 0}) // flags, then ISO-8859-1 encoding
		body.WriteString(text)
	}
	n := body.Len()
	header := []byte{'I', 'D', '3', 3, 0, 0,
		byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
	return append(append(header, body.Bytes()...), []byte("\xff\xfbAUDIO-FRAMES")...)
}

const syntheticCatalogSchema = `
CREATE TABLE move_log (file_id INTEGER PRIMARY KEY, dest TEXT);
CREATE TABLE move_plan (file_id INTEGER PRIMARY KEY, displaced_by INTEGER);
CREATE TABLE itunes_track (
  track_id INTEGER PRIMARY KEY, name TEXT, artist TEXT, album_artist TEXT, album TEXT,
  track_number INTEGER, disc_number INTEGER, rating INTEGER, play_count INTEGER,
  play_date TEXT, date_added TEXT);
CREATE VIEW itunes_audio_track AS SELECT * FROM itunes_track;
CREATE TABLE itunes_track_file (track_id INTEGER, library TEXT, file_id INTEGER);
CREATE TABLE itunes_track_album (track_id INTEGER PRIMARY KEY, album_id INTEGER);
CREATE TABLE itunes_playlist (
  playlist_id INTEGER PRIMARY KEY, name TEXT, master INTEGER DEFAULT 0, folder INTEGER DEFAULT 0,
  all_items INTEGER DEFAULT 0, music INTEGER DEFAULT 0, movies INTEGER DEFAULT 0,
  tv_shows INTEGER DEFAULT 0, podcasts INTEGER DEFAULT 0, audiobooks INTEGER DEFAULT 0,
  distinguished_kind INTEGER, smart_criteria BLOB);
CREATE TABLE itunes_playlist_item (playlist_id INTEGER, position INTEGER, track_id INTEGER);

-- Album 1, "Album" by Band: track 1 survives, track 2 is lost, and
-- track 3's own file lost its slot to a better copy, file 30.
INSERT INTO itunes_track VALUES
  (1, 'One',   'Band', NULL, 'Album', 1, 1, 80, 5, '2013-01-01T00:00:00Z', '2005-01-01T00:00:00Z'),
  (2, 'Two',   'Band', NULL, 'Album', 2, 1, 70, 3, NULL,                   '2005-01-01T00:00:00Z'),
  (3, 'Three', 'Band', NULL, 'Album', 3, 1, 60, 9, NULL,                   '2005-01-02T00:00:00Z');
INSERT INTO itunes_track_album VALUES (1, 1), (2, 1), (3, 1);
INSERT INTO move_log VALUES
  (10, '/old/Undamaged/Band/Album/01 One.mp3'),
  (30, '/old/Undamaged/Band/Album/03 Three.mp3');
INSERT INTO itunes_track_file VALUES (1, 'mine', 10), (3, 'jcap', 31);
INSERT INTO move_plan VALUES (31, 30);

INSERT INTO itunes_playlist (playlist_id, name) VALUES (1, 'Complete'), (2, 'Partial');
INSERT INTO itunes_playlist_item VALUES (1, 0, 2), (1, 1, 1), (2, 0, 1), (2, 1, 99);
`

// syntheticImport builds a small library and catalog, imports them, and
// returns the new database and the report.
func syntheticImport(t *testing.T) (*sql.DB, *Report) {
	t.Helper()
	dir := t.TempDir()
	library := filepath.Join(dir, "library")

	write := func(rel string, content []byte) {
		p := filepath.Join(library, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Undamaged/Band/Album/01 One.mp3", id3(map[string]string{"TIT2": "One", "TPE1": "Band", "TALB": "Album", "TRCK": "1"}))
	write("Undamaged/Band/Album/03 Three.mp3", id3(map[string]string{"TIT2": "Three", "TPE1": "Band", "TALB": "Album", "TRCK": "3/3"}))
	write("Undamaged/Band/Album/cover.jpg", []byte("JPEG"))
	write("Undamaged/Band/Unknown Album/Loose.mp3", id3(map[string]string{"TIT2": "Loose", "TPE1": "Band"}))
	write("Undamaged/Compilations/Mix/01 A.mp3", id3(map[string]string{"TIT2": "A", "TPE1": "X", "TALB": "Mix", "TRCK": "1"}))
	write("Undamaged/Compilations/Mix/02 B.mp3", id3(map[string]string{"TIT2": "B", "TPE1": "Y", "TALB": "Mix", "TRCK": "2"}))
	write("Undamaged/Other/Thing/10 Static.mp3", []byte("\xff\xfbNO-TAGS-AT-ALL"))
	write("Undamaged/Other/Thing/9 Hiss.mp3", []byte("\xff\xfbNO-TAGS-AT-ALL"))
	write("Undamaged/Other/Thing/1999 Prince.mp3", []byte("\xff\xfbNO-TAGS-AT-ALL"))
	write("Undamaged/Other/Thing/2-01 Hum.mp3", []byte("\xff\xfbNO-TAGS-AT-ALL"))
	write("Undamaged/Solo/Record/01 Song.mp3", id3(map[string]string{"TIT2": "Song", "TALB": "Record", "TRCK": "1"}))
	write("Undamaged/Unknown Artist/Mystery/01 Riddle.mp3", id3(map[string]string{"TIT2": "Riddle", "TPE1": "Unknown Artist", "TALB": "Mystery", "TRCK": "1"}))
	write("Damaged/Ignored/Album/01 X.mp3", id3(map[string]string{"TIT2": "X"}))

	loose := filepath.Join(library, "Undamaged/Band/Unknown Album/Loose.mp3")
	mtime := time.Date(2019, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(loose, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	catalogPath := filepath.Join(dir, "catalog.sqlite")
	catalog, err := sql.Open("sqlite", "file:"+catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Exec(syntheticCatalogSchema); err != nil {
		t.Fatal(err)
	}
	catalog.Close()

	out := filepath.Join(dir, "juke.sqlite")
	r, err := Run(context.Background(), Options{
		Library:     library,
		Include:     []string{"Undamaged"},
		Catalog:     catalogPath,
		CatalogRoot: "/old",
		Out:         out,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+out+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, r
}

// checkTrack checks one imported track, found by title.  album is ""
// for a track on no album; rating is 0 for none.
func checkTrack(t *testing.T, db *sql.DB, title, album string, pos int, rating float64, plays int64, dateAdded string, hasAudio bool) {
	t.Helper()
	var gotAlbum sql.NullString
	var gotPos sql.NullInt64
	var gotRating sql.NullFloat64
	var gotPlays int64
	var gotAdded string
	var gotAudio bool
	err := db.QueryRow(`
		SELECT a.title, t.position, t.rating, t.play_count, t.date_added, t.blob_id IS NOT NULL
		FROM track t LEFT JOIN album a ON a.id = t.album_id WHERE t.title = ?`, title,
	).Scan(&gotAlbum, &gotPos, &gotRating, &gotPlays, &gotAdded, &gotAudio)
	if err != nil {
		t.Errorf("track %q: %v", title, err)
		return
	}
	if gotAlbum.String != album || (album != "" && gotPos.Int64 != int64(pos)) {
		t.Errorf("track %q: on %q at %d, want %q at %d", title, gotAlbum.String, gotPos.Int64, album, pos)
	}
	if gotRating.Float64 != rating || gotPlays != plays || gotAdded != dateAdded || gotAudio != hasAudio {
		t.Errorf("track %q: rating %v, plays %d, added %s, audio %v; want %v, %d, %s, %v",
			title, gotRating.Float64, gotPlays, gotAdded, gotAudio, rating, plays, dateAdded, hasAudio)
	}
}

func TestSyntheticImport(t *testing.T) {
	db, r := syntheticImport(t)

	if len(r.TitledFromName) != 4 {
		t.Errorf("titled from filename %q, want the four untagged files", r.TitledFromName)
	}
	if r.Tracks != 11 || r.GapTracks != 1 || r.Albums != 5 || r.AlbumlessTrack != 1 || r.SkippedFiles != 1 || r.Playlists != 1 {
		t.Errorf("report = %+v", r)
	}

	checkTrack(t, db, "One", "Album", 0, 4, 5, "2005-01-01T00:00:00Z", true)
	checkTrack(t, db, "Two", "Album", 1, 3.5, 3, "2005-01-01T00:00:00Z", false)
	checkTrack(t, db, "Three", "Album", 2, 3, 9, "2005-01-02T00:00:00Z", true)
	checkTrack(t, db, "Loose", "", 0, 0, 0, "2019-03-04T05:06:07Z", true)
	checkTrack(t, db, "A", "Mix", 0, 0, 0, mtimeOf(t, db, "A"), true)

	// Untagged files the catalog doesn't know take their titles and
	// numbers from their filenames: "9 Hiss" before "10 Static", whatever
	// the alphabet says, and "2-01 Hum" first on a second disc.
	checkTrack(t, db, "Hiss", "Thing", 0, 0, 0, mtimeOf(t, db, "Hiss"), true)
	checkTrack(t, db, "Static", "Thing", 1, 0, 0, mtimeOf(t, db, "Static"), true)
	checkTrack(t, db, "1999 Prince", "Thing", 2, 0, 0, mtimeOf(t, db, "1999 Prince"), true)
	checkTrack(t, db, "Hum", "Thing", 0, 0, 0, mtimeOf(t, db, "Hum"), true)
	checkTrackArtist(t, db, "Hiss", "Other")

	// A track with no artist tag takes its artist directory's name, but
	// "Unknown Artist" is no artist, in a tag or a directory.
	checkTrackArtist(t, db, "Song", "Solo")
	checkTrackArtist(t, db, "Riddle", "")
	checkAlbumArtist(t, db, "Mystery", "")

	var compilationArtist sql.NullString
	db.QueryRow(`SELECT artist FROM album WHERE title = 'Mix'`).Scan(&compilationArtist)
	if compilationArtist.Valid {
		t.Errorf("compilation artist = %q, want none", compilationArtist.String)
	}

	var playlistTitles []string
	rows, _ := db.Query(`
		SELECT t.title FROM playlist p JOIN playlist_track pt ON pt.playlist_id = p.id
		JOIN track t ON t.id = pt.track_id WHERE p.title = 'Complete' ORDER BY pt.position`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		playlistTitles = append(playlistTitles, s)
	}
	rows.Close()
	if len(playlistTitles) != 2 || playlistTitles[0] != "Two" || playlistTitles[1] != "One" {
		t.Errorf("playlist Complete = %q, want [Two One]", playlistTitles)
	}

	var tags string
	db.QueryRow(`SELECT original_tags FROM track WHERE title = 'One'`).Scan(&tags)
	if !bytes.Contains([]byte(tags), []byte(`"TIT2":"One"`)) {
		t.Errorf("original tags = %s, want the TIT2 frame", tags)
	}
}

// mtimeOf returns the date added the import gave a track, which for a
// file the catalog lacks is the file's mtime, set when the test wrote it.
func mtimeOf(t *testing.T, db *sql.DB, title string) string {
	t.Helper()
	var s string
	db.QueryRow(`SELECT date_added FROM track WHERE title = ?`, title).Scan(&s)
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		t.Errorf("track %q date added %q is not a date", title, s)
	}
	return s
}

// checkTrackArtist checks the artist of one imported track, found by title.
func checkTrackArtist(t *testing.T, db *sql.DB, title, artist string) {
	t.Helper()
	var got sql.NullString
	if err := db.QueryRow(`SELECT artist FROM track WHERE title = ?`, title).Scan(&got); err != nil {
		t.Errorf("track %q: %v", title, err)
		return
	}
	if got.String != artist {
		t.Errorf("track %q: artist %q, want %q", title, got.String, artist)
	}
}

// checkAlbumArtist checks the artist of one imported album, found by
// title; "" means none.
func checkAlbumArtist(t *testing.T, db *sql.DB, title, artist string) {
	t.Helper()
	var got sql.NullString
	if err := db.QueryRow(`SELECT artist FROM album WHERE title = ?`, title).Scan(&got); err != nil {
		t.Errorf("album %q: %v", title, err)
		return
	}
	if got.String != artist {
		t.Errorf("album %q: artist %q, want %q", title, got.String, artist)
	}
}
