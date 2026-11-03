package artwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/bits"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"semiotic.systems/juke/internal/store"
)

// A library is described in a little language.  Each album is a line of
// tracks separated by spaces; each track is the letters of the pictures
// its file carries, in order, or "-" for none, and a lowercase letter is
// a front cover while an uppercase one is a back cover.  A letter
// followed by an apostrophe, "a'", is the same picture as "a" encoded
// another way: other bytes, another id, the same look.  "folder:x"
// before the tracks puts an image file x beside them.  Albumless tracks
// go on a line starting "loose:".
type library struct {
	albums []string // "a a b", "folder:f a a", "loose: a -"
}

// The outcome is rendered one album per line, in the same order, as the
// album's image, then "|", then each track's override or "-".
type want []string

// imageFor makes a distinct picture for each letter: nine by eight
// blocks of grey, shades drawn from the letter, so its difference hash
// is a pattern of its own and a re-encoding of it hashes the same.
func imageFor(letter byte) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, 72, 64))
	seed := uint32(letter) * 2654435761
	for by := 0; by < 8; by++ {
		for bx := 0; bx < 9; bx++ {
			seed = seed*1103515245 + 12345
			shade := color.Gray{Y: uint8(seed >> 24)}
			for y := by * 8; y < by*8+8; y++ {
				for x := bx * 8; x < bx*8+8; x++ {
					img.SetGray(x, y, shade)
				}
			}
		}
	}
	return img
}

// pngFor is the letter's picture as a PNG; jpegFor the same picture as
// a JPEG, which is other bytes.
func pngFor(letter byte) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, imageFor(letter)); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func jpegFor(letter byte) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, imageFor(letter), &jpeg.Options{Quality: 95}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// picturesOf parses a track's letters into its pictures' bytes and
// media types.
func picturesOf(letters string) ([][]byte, []string) {
	var data [][]byte
	var mimes []string
	for i := 0; i < len(letters); i++ {
		if i+1 < len(letters) && letters[i+1] == '\'' {
			data, mimes = append(data, jpegFor(letters[i])), append(mimes, "image/jpeg")
			i++
		} else {
			data, mimes = append(data, pngFor(letters[i])), append(mimes, "image/png")
		}
	}
	return data, mimes
}

// mp3With makes an MP3 file that is all ID3v2.3 tag: one APIC frame per
// picture, front cover for a lowercase letter and back cover for an
// uppercase one.  It also returns the stubs the import would store.
func mp3With(letters string) ([]byte, string) {
	var frames bytes.Buffer
	stubs := map[string]any{"TIT2": "Song"}
	pictures, mimes := picturesOf(letters)
	for i, data := range pictures {
		c := strings.ReplaceAll(letters, "'", "")[i]
		picType := byte(3)
		typeName := "Cover (front)"
		if c >= 'A' && c <= 'Z' {
			picType, typeName = 4, "Cover (back)"
		}
		var body bytes.Buffer
		body.WriteByte(0)
		body.WriteString(mimes[i] + "\x00")
		body.WriteByte(picType)
		body.WriteString("\x00")
		body.Write(data)
		frames.WriteString("APIC")
		binary.Write(&frames, binary.BigEndian, uint32(body.Len()))
		frames.Write([]byte{0, 0})
		frames.Write(body.Bytes())
		key := "APIC"
		if i > 0 {
			key = fmt.Sprintf("APIC_%d", i-1)
		}
		sum := sha256.Sum256(data)
		stubs[key] = map[string]any{
			"binary": true, "mime": mimes[i], "pictureType": typeName,
			"size": len(data), "sha256": hex.EncodeToString(sum[:]),
		}
	}
	size := frames.Len()
	header := []byte{'I', 'D', '3', 3, 0, 0,
		byte(size >> 21 & 0x7f), byte(size >> 14 & 0x7f), byte(size >> 7 & 0x7f), byte(size & 0x7f)}
	raw, _ := json.Marshal(stubs)
	return append(header, frames.Bytes()...), string(raw)
}

type fixture struct {
	db      *sql.DB
	library string
	albums  []string // ids, "" for the loose tracks
	tracks  [][]string
}

// setup builds the database and files for a library.
func setup(t *testing.T, lib library) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	f := &fixture{library: filepath.Join(dir, "library")}
	s, err := store.Create(filepath.Join(dir, "juke.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	f.db = s.DB

	for n, line := range lib.albums {
		fields := strings.Fields(line)
		albumID, albumDir := "", fmt.Sprintf("Artist/Album %d", n)
		if fields[0] == "loose:" {
			fields, albumDir = fields[1:], "Artist/Unknown Album"
		} else {
			albumID, err = store.InsertAlbum(ctx, f.db, store.NewAlbum{Title: fmt.Sprintf("Album %d", n)})
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(f.library, albumDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(fields[0], "folder:") {
			letter := strings.TrimPrefix(fields[0], "folder:")
			if err := os.WriteFile(filepath.Join(f.library, albumDir, "cover.png"), pngFor(letter[0]), 0o644); err != nil {
				t.Fatal(err)
			}
			fields = fields[1:]
		}
		var ids []string
		for i, letters := range fields {
			if letters == "-" {
				letters = ""
			}
			content, raw := mp3With(letters)
			rel := fmt.Sprintf("%s/%02d Song.mp3", albumDir, i+1)
			if err := os.WriteFile(filepath.Join(f.library, rel), content, 0o644); err != nil {
				t.Fatal(err)
			}
			size, ct := int64(len(content)), "audio/mpeg"
			nt := store.NewTrack{Title: "Song", DateAdded: "2014-01-01T00:00:00Z", Path: &rel, Size: &size, ContentType: &ct, OriginalTags: &raw}
			if albumID != "" {
				disc, pos := 0, i+1
				nt.AlbumID, nt.DiscIndex, nt.Position = &albumID, &disc, &pos
			}
			id, err := store.InsertTrack(ctx, f.db, nt)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		f.albums = append(f.albums, albumID)
		f.tracks = append(f.tracks, ids)
	}
	return f
}

// render reads the outcome back in the little language, naming each
// image by the letter it was made from.
func (f *fixture) render(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()
	name := f.letterOf
	var out []string
	for n, albumID := range f.albums {
		var line strings.Builder
		var art sql.NullString
		if albumID != "" {
			if err := f.db.QueryRowContext(ctx, `SELECT artwork_id FROM album WHERE id = ?`, albumID).Scan(&art); err != nil {
				t.Fatal(err)
			}
		}
		line.WriteString(name(art) + " |")
		for _, id := range f.tracks[n] {
			if err := f.db.QueryRowContext(ctx, `SELECT artwork_id FROM track WHERE id = ?`, id).Scan(&art); err != nil {
				t.Fatal(err)
			}
			line.WriteString(" " + name(art))
		}
		out = append(out, line.String())
	}
	return out
}

// checkArt runs the artwork pass over a library and checks what each
// album and track ends up with, and that a second run changes nothing.
func checkArt(t *testing.T, name string, lib library, expected want) {
	t.Helper()
	f := setup(t, lib)
	ctx := context.Background()
	if _, err := Run(ctx, f.db, f.library); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	got := f.render(t)
	if strings.Join(got, "\n") != strings.Join(expected, "\n") {
		t.Errorf("%s:\n got: %q\nwant: %q", name, got, []string(expected))
	}
	again, err := Run(ctx, f.db, f.library)
	if err != nil {
		t.Fatalf("%s, again: %v", name, err)
	}
	if again.Changed != 0 || again.Extracted != 0 {
		t.Errorf("%s: a second run changed %d and extracted %d", name, again.Changed, again.Extracted)
	}
}

func TestAssignment(t *testing.T) {
	checkArt(t, "one picture throughout", library{[]string{"a a a"}}, want{"a | - - -"})
	checkArt(t, "a bare track inherits", library{[]string{"a - a"}}, want{"a | - - -"})
	checkArt(t, "the majority wins, the rest override", library{[]string{"a b a"}}, want{"a | - b -"})
	checkArt(t, "a tie goes to the first track", library{[]string{"b a"}}, want{"b | - a"})
	checkArt(t, "every track its own: the first is the album's", library{[]string{"c b a"}}, want{"c | - b a"})
	checkArt(t, "folder art outranks the vote", library{[]string{"folder:f a a"}}, want{"f | a a"})
	checkArt(t, "a back cover is not a candidate", library{[]string{"A A a"}}, want{"a | - - -"})
	checkArt(t, "a track's first front cover is its own", library{[]string{"Ab b ab"}}, want{"b | - - a"})
	checkArt(t, "no pictures at all", library{[]string{"- -"}}, want{"- | - -"})
	checkArt(t, "loose tracks keep their own", library{[]string{"loose: a -"}}, want{"- | a -"})
	checkArt(t, "two albums share nothing", library{[]string{"a a", "b", "loose: a"}}, want{"a | - -", "b | -", "- | a"})
}

func TestStoredImages(t *testing.T) {
	f := setup(t, library{[]string{"a a b", "loose: a"}})
	r, err := Run(context.Background(), f.db, f.library)
	if err != nil {
		t.Fatal(err)
	}
	if r.Images != 2 || r.Extracted != 2 || r.Overrides != 2 || r.Albums != 1 {
		t.Errorf("report: %+v", *r)
	}
	rows, err := f.db.Query(`SELECT id, mime, size, width, height FROM artwork ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, mime string
		var size, width, height int
		if err := rows.Scan(&id, &mime, &size, &width, &height); err != nil {
			t.Fatal(err)
		}
		n++
		if mime != "image/png" || width != 72 || height != 64 {
			t.Errorf("image %s: %s %dx%d", id, mime, width, height)
		}
		info, err := os.Stat(Path(f.library, id))
		if err != nil {
			t.Errorf("image %s: %v", id, err)
		} else if info.Size() != int64(size) {
			t.Errorf("image %s: file is %d bytes, row says %d", id, info.Size(), size)
		}
	}
	if n != 2 {
		t.Errorf("%d images stored, want 2", n)
	}
}

// An album or track whose art changes gets a new modseq.
func TestModseqs(t *testing.T) {
	f := setup(t, library{[]string{"a b"}})
	ctx := context.Background()
	if _, err := Run(ctx, f.db, f.library); err != nil {
		t.Fatal(err)
	}
	modseq := func(table, id string) int64 {
		var m int64
		if err := f.db.QueryRowContext(ctx, `SELECT modseq FROM `+table+` WHERE id = ?`, id).Scan(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	album, first, second := modseq("album", f.albums[0]), modseq("track", f.tracks[0][0]), modseq("track", f.tracks[0][1])
	if err := os.WriteFile(filepath.Join(f.library, "Artist/Album 0/cover.png"), pngFor('b'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, f.db, f.library); err != nil {
		t.Fatal(err)
	}
	if got := f.render(t); got[0] != "b | a -" {
		t.Errorf("after adding folder art: %q", got)
	}
	if modseq("album", f.albums[0]) <= album || modseq("track", f.tracks[0][0]) <= first || modseq("track", f.tracks[0][1]) <= second {
		t.Error("changed rows kept their modseqs")
	}
}

// letterOf names a stored image by the letter it was made from.
func (f *fixture) letterOf(art sql.NullString) string {
	if !art.Valid {
		return "-"
	}
	for c := byte('a'); c <= 'z'; c++ {
		for _, l := range []byte{c, c - 32} {
			sum := sha256.Sum256(pngFor(l))
			if store.ArtworkID(hex.EncodeToString(sum[:])) == art.String {
				return string(l)
			}
			sum = sha256.Sum256(jpegFor(l))
			if store.ArtworkID(hex.EncodeToString(sum[:])) == art.String {
				return string(l) + "'"
			}
		}
	}
	return "?" + art.String
}

// idOf is the id the image made from a letter is stored under.
func idOf(letter byte) string {
	sum := sha256.Sum256(pngFor(letter))
	return store.ArtworkID(hex.EncodeToString(sum[:]))
}

// Art chosen by hand survives a run: the album keeps its image and its
// tracks keep theirs, overrides cleared included.
func TestHandChoicesKept(t *testing.T) {
	f := setup(t, library{[]string{"a b a", "a b a"}})
	ctx := context.Background()
	if _, err := Run(ctx, f.db, f.library); err != nil {
		t.Fatal(err)
	}
	// The first album: chosen as "b", overrides cleared.  The second: the
	// rule's album art, but the first track given "b" by hand.
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE album SET artwork_id = ?, artwork_source = 'user' WHERE id = ?`, []any{idOf('b'), f.albums[0]}},
		{`UPDATE track SET artwork_id = NULL, artwork_source = 'user' WHERE album_id = ?`, []any{f.albums[0]}},
		{`UPDATE track SET artwork_id = ?, artwork_source = 'user' WHERE id = ?`, []any{idOf('b'), f.tracks[1][0]}},
	} {
		if _, err := f.db.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Run(ctx, f.db, f.library)
	if err != nil {
		t.Fatal(err)
	}
	got := f.render(t)
	if want := []string{"b | - - -", "a | b b -"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("after a run:\n got: %q\nwant: %q", got, want)
	}
	if r.Changed != 0 || r.Kept != 5 || r.Overrides != 2 {
		t.Errorf("report: %+v, want nothing changed, 5 kept, 2 overrides", *r)
	}
}

// The run records what the review page shows: each track's own picture,
// the image file beside an album, and whether the choice was a tie.
func TestReviewColumns(t *testing.T) {
	f := setup(t, library{[]string{"b a", "folder:f a b", "a a"}})
	ctx := context.Background()
	if _, err := Run(ctx, f.db, f.library); err != nil {
		t.Fatal(err)
	}
	for n, want := range []struct {
		folder   string
		tie      bool
		pictures string
	}{{"-", true, "b a"}, {"f", false, "a b"}, {"-", false, "a a"}} {
		var folder sql.NullString
		var tie bool
		if err := f.db.QueryRowContext(ctx, `SELECT folder_artwork_id, art_tie FROM album WHERE id = ?`, f.albums[n]).Scan(&folder, &tie); err != nil {
			t.Fatal(err)
		}
		var pictures []string
		for _, id := range f.tracks[n] {
			var p sql.NullString
			if err := f.db.QueryRowContext(ctx, `SELECT picture_id FROM track WHERE id = ?`, id).Scan(&p); err != nil {
				t.Fatal(err)
			}
			pictures = append(pictures, f.letterOf(p))
		}
		if got := f.letterOf(folder); got != want.folder || tie != want.tie || strings.Join(pictures, " ") != want.pictures {
			t.Errorf("album %d: folder %s, tie %v, pictures %q; want %s, %v, %q", n, got, tie, pictures, want.folder, want.tie, want.pictures)
		}
	}
}

// The collector removes images nothing names, keeping a recent upload
// for the client that may be about to set it.
func TestCollect(t *testing.T) {
	f := setup(t, library{[]string{"a b"}})
	ctx := context.Background()
	if _, err := Run(ctx, f.db, f.library); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	fresh, err := Store(ctx, f.db, f.library, pngFor('x'), &now)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := Store(ctx, f.db, f.library, pngFor('y'), &old)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := Store(ctx, f.db, f.library, pngFor('z'), &old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE album SET artwork_id = ? WHERE id = ?`, kept.ID, f.albums[0]); err != nil {
		t.Fatal(err)
	}
	// Track 2's own picture "b" is named by its picture_id, so it stays
	// though it is also the track's art; make it unnamed by both.
	if _, err := f.db.ExecContext(ctx, `UPDATE track SET artwork_id = NULL, picture_id = NULL WHERE id = ?`, f.tracks[0][1]); err != nil {
		t.Fatal(err)
	}

	n, err := Collect(ctx, f.db, f.library, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("removed %d, want 2: the stale upload and the picture nothing names", n)
	}
	for _, c := range []struct {
		id   string
		want bool
	}{{fresh.ID, true}, {stale.ID, false}, {kept.ID, true}, {idOf('a'), true}, {idOf('b'), false}} {
		var found bool
		if err := f.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM artwork WHERE id = ?)`, c.id).Scan(&found); err != nil {
			t.Fatal(err)
		}
		_, statErr := os.Stat(Path(f.library, c.id))
		if found != c.want || (statErr == nil) != c.want {
			t.Errorf("%s: row %v, file %v, want %v", f.letterOf(sql.NullString{String: c.id, Valid: true}), found, statErr == nil, c.want)
		}
	}
}

// One picture encoded several ways is one picture: it votes together,
// and a track carrying another encoding of the album's shows the album's.
func TestNearIdentical(t *testing.T) {
	checkArt(t, "encodings vote together", library{[]string{"a a' b"}}, want{"a | - - b"})
	checkArt(t, "the first carried is the exemplar", library{[]string{"a' a b"}}, want{"a' | - - b"})
	checkArt(t, "a tie between pictures is still a tie", library{[]string{"a b'"}}, want{"a | - b'"})
	checkArt(t, "an album carried two ways", library{[]string{"a a'"}}, want{"a | - -"})
	checkArt(t, "folder art the files carry another way", library{[]string{"folder:f f' f'"}}, want{"f | - -"})
	checkArt(t, "loose tracks are not grouped", library{[]string{"loose: a a'"}}, want{"- | a a'"})
	checkArt(t, "grouping is per album", library{[]string{"a a'", "a' b"}}, want{"a | - -", "a' | - b"})

	f := setup(t, library{[]string{"a a' b"}})
	ctx := context.Background()
	r, err := Run(ctx, f.db, f.library)
	if err != nil {
		t.Fatal(err)
	}
	if r.Collapsed != 1 || r.Hashed != 0 {
		t.Errorf("report: %+v, want 1 collapsed, none hashed after the fact", *r)
	}
	// Images stored before hashing began are hashed on the next run, from
	// their files, and nothing else changes.
	if _, err := f.db.ExecContext(ctx, `UPDATE artwork SET dhash = NULL`); err != nil {
		t.Fatal(err)
	}
	r, err = Run(ctx, f.db, f.library)
	if err != nil {
		t.Fatal(err)
	}
	if r.Hashed != 3 || r.Unhashable != 0 || r.Changed != 0 || r.Collapsed != 1 {
		t.Errorf("after unhashing: %+v, want 3 hashed, nothing changed, 1 collapsed", *r)
	}
	// An image no decoder reads is counted, not hidden.
	if _, err := f.db.ExecContext(ctx, `UPDATE artwork SET dhash = NULL`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(f.library, idOf('b')), []byte("not a picture"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = Run(ctx, f.db, f.library)
	if err != nil {
		t.Fatal(err)
	}
	if r.Hashed != 2 || r.Unhashable != 1 {
		t.Errorf("with a junk file: %+v, want 2 hashed, 1 unhashable", *r)
	}
}

// The hashes of the fixture's pictures: distinct per letter, alike per
// encoding, which is what the cases above rest on.
func TestHashes(t *testing.T) {
	a, _ := hashOf(pngFor('a'))
	a2, _ := hashOf(jpegFor('a'))
	b, _ := hashOf(pngFor('b'))
	if !near(a, a2) {
		t.Errorf("a and its JPEG differ in %d bits", bits.OnesCount64(a^a2))
	}
	if near(a, b) {
		t.Errorf("a and b differ in only %d bits", bits.OnesCount64(a^b))
	}
}
