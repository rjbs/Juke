package store

import (
	"context"
	"errors"
	"fmt"
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

// checkOpenMigrates makes a database, sets its schema version to from (0
// for a database made before versioning), and opens it with the extra
// migrations appended to the real ones.  It checks the version Open leaves
// it at, or that Open fails.
func checkOpenMigrates(t *testing.T, from int, extra []string, want int, wantErr bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "juke.sqlite")
	s, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.DB.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, from)); err != nil {
		t.Fatal(err)
	}
	s.Close()

	saved := migrations
	migrations = append(append([]string{}, saved...), extra...)
	defer func() { migrations = saved }()

	s, err = Open(path)
	if wantErr {
		if err == nil {
			s.Close()
			t.Errorf("Open from version %d with %d more: no error", from, len(extra))
		}
		return
	}
	if err != nil {
		t.Fatalf("Open from version %d with %d more: %v", from, len(extra), err)
	}
	defer s.Close()
	got, err := SchemaVersion(context.Background(), s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Open from version %d with %d more: version %d, want %d", from, len(extra), got, want)
	}
}

func TestMigrations(t *testing.T) {
	newTable := []string{`CREATE TABLE later (x INTEGER)`}
	broken := []string{`CREATE TABLE later (x INTEGER)`, `NOT SQL`}

	checkOpenMigrates(t, 1, nil, 1, false)
	checkOpenMigrates(t, 0, nil, 1, false)
	checkOpenMigrates(t, 1, newTable, 2, false)
	checkOpenMigrates(t, 0, newTable, 2, false)
	checkOpenMigrates(t, 1, broken, 0, true)
	checkOpenMigrates(t, 2, nil, 0, true)
}

func TestCreateIsCurrent(t *testing.T) {
	s := newStore(t)
	got, err := SchemaVersion(context.Background(), s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if got != len(migrations) {
		t.Errorf("a new database is at version %d, want %d", got, len(migrations))
	}
}
