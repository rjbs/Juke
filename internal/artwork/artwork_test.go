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
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"semiotic.systems/juke/internal/store"
)

// A library is described in a little language.  Each album is a line of
// tracks separated by spaces; each track is the letters of the pictures
// its file carries, in order, or "-" for none, and a lowercase letter is
// a front cover while an uppercase one is a back cover.  "folder:x"
// before the tracks puts an image file x beside them.  Albumless tracks
// go on a line starting "loose:".
type library struct {
	albums []string // "a a b", "folder:f a a", "loose: a -"
}

// The outcome is rendered one album per line, in the same order, as the
// album's image, then "|", then each track's override or "-".
type want []string

// pngFor makes a distinct little image for each letter.
func pngFor(letter byte) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.RGBA{letter, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// mp3With makes an MP3 file that is all ID3v2.3 tag: one APIC frame per
// picture, front cover for a lowercase letter and back cover for an
// uppercase one.  It also returns the stubs the import would store.
func mp3With(letters string) ([]byte, string) {
	var frames bytes.Buffer
	stubs := map[string]any{"TIT2": "Song"}
	for i, c := range []byte(letters) {
		data := pngFor(c)
		picType := byte(3)
		typeName := "Cover (front)"
		if c >= 'A' && c <= 'Z' {
			picType, typeName = 4, "Cover (back)"
		}
		var body bytes.Buffer
		body.WriteByte(0)
		body.WriteString("image/png\x00")
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
			"binary": true, "mime": "image/png", "pictureType": typeName,
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
	letters := map[string]string{}
	for c := byte('a'); c <= 'z'; c++ {
		sum := sha256.Sum256(pngFor(c))
		letters[store.ArtworkID(hex.EncodeToString(sum[:]))] = string(c)
		sum = sha256.Sum256(pngFor(c - 32))
		letters[store.ArtworkID(hex.EncodeToString(sum[:]))] = string(c - 32)
	}
	name := func(art sql.NullString) string {
		if !art.Valid {
			return "-"
		}
		if l, ok := letters[art.String]; ok {
			return l
		}
		return "?" + art.String
	}
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
		if mime != "image/png" || width != 3 || height != 2 {
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
