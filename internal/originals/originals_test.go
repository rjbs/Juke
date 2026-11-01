package originals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"semiotic.systems/juke/internal/store"
)

// library is a test library: a database whose tracks name files under
// dir, with each track's id kept by title.
type library struct {
	s   *store.Store
	dir string
	ids map[string]string
}

// newLibrary makes a library from title → file contents; "" contents is
// a track with no audio.
func newLibrary(t *testing.T, files map[string]string) *library {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.Create(filepath.Join(dir, "juke.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	l := &library{s: s, dir: filepath.Join(dir, "lib"), ids: map[string]string{}}
	for title, content := range files {
		nt := store.NewTrack{Title: title, DateAdded: "2014-01-01T00:00:00Z"}
		if content != "" {
			rel := "Undamaged/Band/Album/" + title + ".MP3"
			p := filepath.Join(l.dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			nt.Path = &rel
		}
		id, err := store.InsertTrack(ctx, s.DB, nt)
		if err != nil {
			t.Fatal(err)
		}
		l.ids[title] = id
	}
	return l
}

func (l *library) run(t *testing.T, dryRun bool) *Report {
	t.Helper()
	r, err := Run(context.Background(), Options{
		DB: l.s.DB, Library: l.dir, Journal: filepath.Join(l.dir, "..", "journal"), DryRun: dryRun,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return r
}

// checkStored checks that a track's path is the opaque name for content,
// and that the file there holds it; content "" means the track still
// has the path it was given.
func checkStored(t *testing.T, l *library, title, content string) {
	t.Helper()
	var got string
	if err := l.s.DB.QueryRow(`SELECT coalesce(path, '') FROM track WHERE id = ?`, l.ids[title]).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if content == "" {
		if got != "" && got != "Undamaged/Band/Album/"+title+".MP3" {
			t.Errorf("%s: path %q, want it unmoved", title, got)
		}
		return
	}
	sum := sha256.Sum256([]byte(content))
	want := Name(hex.EncodeToString(sum[:]), title+".MP3")
	if got != want {
		t.Errorf("%s: path %q, want %q", title, got, want)
	}
	b, err := os.ReadFile(filepath.Join(l.dir, filepath.FromSlash(got)))
	if err != nil || string(b) != content {
		t.Errorf("%s: file at %s holds %q (%v), want %q", title, got, b, err, content)
	}
}

func checkReport(t *testing.T, r *Report, moved, already, recovered, duplicates int) {
	t.Helper()
	if r.Moved != moved || r.Already != already || r.Recovered != recovered || len(r.Duplicates) != duplicates {
		t.Errorf("moved %d, already %d, recovered %d, duplicates %d; want %d, %d, %d, %d",
			r.Moved, r.Already, r.Recovered, len(r.Duplicates), moved, already, recovered, duplicates)
	}
}

func TestName(t *testing.T) {
	if got := Name("ab12cd", "Some/Dir/01 Song.MP3"); got != "originals/ab/ab12cd.mp3" {
		t.Errorf("Name = %q", got)
	}
}

func TestDryRunMovesNothing(t *testing.T) {
	l := newLibrary(t, map[string]string{"One": "one", "Two": "two"})
	checkReport(t, l.run(t, true), 2, 0, 0, 0)
	checkStored(t, l, "One", "")
	checkStored(t, l, "Two", "")
}

func TestMoves(t *testing.T) {
	l := newLibrary(t, map[string]string{"One": "one", "Two": "two", "Copy": "one", "Gap": ""})
	r := l.run(t, false)
	checkReport(t, r, 2, 0, 0, 1)
	checkStored(t, l, "Two", "two")
	checkStored(t, l, "Gap", "")

	// Of One and Copy, whichever sorts first by path moves, and the other
	// stays put as its duplicate.
	if d := r.Duplicates[0]; d.TrackID != l.ids["One"] || d.SameAs != l.ids["Copy"] {
		t.Errorf("duplicate %+v, want One as the same as Copy", d)
	}
	checkStored(t, l, "Copy", "one")
	checkStored(t, l, "One", "")

	// The emptied directories are gone; the one still holding One stays.
	if _, err := os.Stat(filepath.Join(l.dir, "Undamaged/Band/Album/One.MP3")); err != nil {
		t.Errorf("the duplicate's file is gone: %v", err)
	}

	// A second run finds everything stored but the duplicate.
	checkReport(t, l.run(t, false), 0, 2, 0, 1)
}

func TestFinishesInterruptedMove(t *testing.T) {
	l := newLibrary(t, map[string]string{"One": "one", "Two": "two"})

	// Make One's move as far as the rename, as if the run stopped before
	// updating the database.
	sum := sha256.Sum256([]byte("one"))
	name := Name(hex.EncodeToString(sum[:]), "One.MP3")
	from := "Undamaged/Band/Album/One.MP3"
	dest := filepath.Join(l.dir, filepath.FromSlash(name))
	os.MkdirAll(filepath.Dir(dest), 0o755)
	if err := os.Rename(filepath.Join(l.dir, filepath.FromSlash(from)), dest); err != nil {
		t.Fatal(err)
	}
	journal := l.ids["One"] + "\t" + from + "\t" + name + "\n"
	if err := os.WriteFile(filepath.Join(l.dir, "..", "journal"), []byte(journal), 0o644); err != nil {
		t.Fatal(err)
	}

	checkReport(t, l.run(t, false), 1, 1, 1, 0)
	checkStored(t, l, "One", "one")
	checkStored(t, l, "Two", "two")
}
