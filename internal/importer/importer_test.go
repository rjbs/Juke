package importer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// The sample library and the recovery's catalog live outside the
// repository, so this test runs only where both are present.
const (
	sampleLibrary = "../../sample"
	sampleCatalog = "/Volumes/Video/music/catalog.sqlite"
)

func importSample(t *testing.T) *sql.DB {
	t.Helper()
	for _, p := range []string{sampleLibrary, sampleCatalog} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("%s not present", p)
		}
	}
	out := filepath.Join(t.TempDir(), "juke.sqlite")
	r, err := Run(context.Background(), Options{
		Library:     sampleLibrary,
		Include:     []string{"Undamaged", "Damaged", "Miscellaneous"},
		Catalog:     sampleCatalog,
		CatalogRoot: "/Volumes/Video/library",
		Playlists:   "../../doc/sample-playlists.json",
		Out:         out,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if r.Tracks != 188 || r.GapTracks != 4 || r.Albums != 19 || r.Playlists != 3 {
		t.Errorf("report = %+v", r)
	}
	db, err := sql.Open("sqlite", "file:"+out+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func checkAlbum(t *testing.T, db *sql.DB, title string, wantArtist *string, wantTracks, wantDiscs, wantGaps int) {
	t.Helper()
	var artist sql.NullString
	var tracks, discs, gaps int
	err := db.QueryRow(`
		SELECT a.artist, count(t.id), count(DISTINCT t.disc_index), sum(t.blob_id IS NULL)
		FROM album a JOIN track t ON t.album_id = a.id
		WHERE a.title = ? GROUP BY a.id`, title,
	).Scan(&artist, &tracks, &discs, &gaps)
	if err != nil {
		t.Errorf("album %q: %v", title, err)
		return
	}
	if (wantArtist == nil) != !artist.Valid || (wantArtist != nil && *wantArtist != artist.String) {
		t.Errorf("album %q: artist %v, want %v", title, artist, wantArtist)
	}
	if tracks != wantTracks || discs != wantDiscs || gaps != wantGaps {
		t.Errorf("album %q: %d tracks, %d discs, %d gaps; want %d, %d, %d",
			title, tracks, discs, gaps, wantTracks, wantDiscs, wantGaps)
	}
}

func checkOrder(t *testing.T, db *sql.DB, album string, want ...string) {
	t.Helper()
	rows, err := db.Query(`
		SELECT t.title FROM track t JOIN album a ON a.id = t.album_id
		WHERE a.title = ? ORDER BY t.disc_index, t.position`, album)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		got = append(got, s)
	}
	if len(got) < len(want) {
		t.Errorf("album %q: got %q, want it to start %q", album, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("album %q: got %q, want it to start %q", album, got, want)
			return
		}
	}
}

func ptr(s string) *string { return &s }

func TestImportSample(t *testing.T) {
	db := importSample(t)

	checkAlbum(t, db, "Live Killers", ptr("Queen"), 22, 2, 0)
	checkAlbum(t, db, "We Are Not Devo", nil, 13, 1, 0)
	checkAlbum(t, db, "Invader Zim", nil, 4, 1, 0)
	checkAlbum(t, db, "3 Feet High And Rising", ptr("De La Soul"), 23, 1, 0)
	checkAlbum(t, db, "Getz/Gilberto", ptr("Stan Getz/João Gilberto"), 10, 1, 0)
	checkAlbum(t, db, "Comes A Time", ptr("Neil Young"), 10, 1, 2)
	checkAlbum(t, db, "It Means Everything", ptr("Save Ferris"), 9, 1, 2)

	checkOrder(t, db, "Comes A Time",
		"Goin' Back", "Comes A Time", "Look Out For My Love", "Lotta Love", "Peace of Mind", "Human Highway")
}
