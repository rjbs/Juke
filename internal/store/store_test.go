package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Create(filepath.Join(t.TempDir(), "juke.sqlite"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func checkParse(t *testing.T, epoch, state string, want int64, wantErr error) {
	t.Helper()
	got, err := ParseState(epoch, state)
	if !errors.Is(err, wantErr) {
		t.Errorf("ParseState(%q, %q): error %v, want %v", epoch, state, err, wantErr)
		return
	}
	if err == nil && got != want {
		t.Errorf("ParseState(%q, %q) = %d, want %d", epoch, state, got, want)
	}
}

func TestParseState(t *testing.T) {
	checkParse(t, "abc", "abc-12", 12, nil)
	checkParse(t, "abc", "abc-0", 0, nil)
	checkParse(t, "abc", "xyz-12", 0, ErrForeignState)
	checkParse(t, "abc", "abc", 0, ErrForeignState)
	checkParse(t, "abc", "abc-twelve", 0, ErrForeignState)
	checkParse(t, "abc", "abc--1", 0, ErrForeignState)
}

func TestTypeModseqFollowsWrites(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	checkTypeModseq := func(typ string, want int64) {
		t.Helper()
		got, err := TypeModseq(ctx, s.DB, typ)
		if err != nil {
			t.Fatalf("TypeModseq(%s): %v", typ, err)
		}
		if got != want {
			t.Errorf("TypeModseq(%s) = %d, want %d", typ, got, want)
		}
	}

	checkTypeModseq(TypeTrack, 0)
	if _, err := InsertAlbum(ctx, s.DB, NewAlbum{Title: "A", DiscTitles: []*string{nil}}); err != nil {
		t.Fatal(err)
	}
	if _, err := InsertTrack(ctx, s.DB, NewTrack{Title: "T", DateAdded: "2014-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	checkTypeModseq(TypeAlbum, 1)
	checkTypeModseq(TypeTrack, 2)
	checkTypeModseq(TypePlaylist, 0)
}

func TestEpochsDiffer(t *testing.T) {
	ctx := context.Background()
	a, _ := newStore(t).Epoch(ctx)
	b, _ := newStore(t).Epoch(ctx)
	if a == "" || a == b {
		t.Errorf("epochs %q and %q should be distinct and non-empty", a, b)
	}
}

func TestCreateRefusesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "juke.sqlite")
	s, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Create(path); err == nil {
		t.Errorf("Create on an existing database should fail")
	}
}
