package store

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"testing"

	"github.com/rjbs/gizmo/sqlitekit"
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
	// Each type counts for itself.
	checkTypeModseq(TypeAlbum, 1)
	checkTypeModseq(TypeTrack, 1)
	checkTypeModseq(TypePlaylist, 0)
}

// A database from before Gizmo's state kit kept one modseq counter for
// every type, an epoch, and its own tombstones.  Opening it carries them
// over, so that the states clients hold still mean what they did.
func TestOpenAdoptsOldState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "juke.sqlite")
	db, err := sqlitekit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations)),
		`INSERT INTO meta (name, value) VALUES ('epoch', 'oldepoch'), ('modseq', '9')`,
		`INSERT INTO album (id, title, created_modseq, modseq) VALUES ('Aone', 'One', 1, 6)`,
		`INSERT INTO track (id, title, date_added, created_modseq, modseq) VALUES ('Tone', 'One', '2014-01-01T00:00:00Z', 2, 4)`,
		`INSERT INTO playlist (id, title, created_modseq, modseq) VALUES ('Pone', 'One', 3, 3)`,
		`INSERT INTO tombstone (type, id, created_modseq, modseq) VALUES ('Track', 'Tgone', 5, 9), ('Playlist', 'Pgone', 7, 8)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	// Twice, since the second opening finds the work already done.
	for range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		states, err := Kit.States(ctx, s.DB)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"Album": "oldepoch-6", "Track": "oldepoch-9", "Playlist": "oldepoch-8", "SmartPlaylist": "oldepoch-0"}
		if !maps.Equal(states, want) {
			t.Errorf("states are %v, want %v", states, want)
		}
		tx, err := Kit.Read(ctx, s.DB)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := tx.Changes(ctx, TypeTrack, "oldepoch-3", 0)
		if err != nil || len(ch.Updated) != 1 || ch.Updated[0] != "Tone" || len(ch.Destroyed) != 0 || len(ch.Created) != 0 {
			t.Errorf("changes to Track since 3: %+v, error %v; want Tone updated, and no word of the track made and unmade since", ch, err)
		}
		ch, err = tx.Changes(ctx, TypePlaylist, "oldepoch-7", 0)
		if err != nil || len(ch.Destroyed) != 1 || ch.Destroyed[0] != "Pgone" {
			t.Errorf("changes to Playlist since 7: %+v, error %v; want Pgone destroyed", ch, err)
		}
		tx.Rollback()

		var left int
		if err := s.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('meta', 'tombstone')`).Scan(&left); err != nil || left != 0 {
			t.Errorf("%d of the old tables are left, error %v", left, err)
		}
		s.Close()
	}

	// The one counter stood at 9, and each type goes on from its own state.
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := NextModseq(ctx, s.DB, TypeAlbum); err != nil || n != 7 {
		t.Errorf("the next Album modseq is %d, error %v; want 7", n, err)
	}
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

// checkOpenMigrates makes a database as it stood after the first from
// migrations (0: made before versioning, with the first migration's
// schema and no version), and opens it with the extra migrations
// appended to the real ones.  It checks the version Open leaves it at, or
// that Open fails.
func checkOpenMigrates(t *testing.T, from int, extra []string, want int, wantErr bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "juke.sqlite")
	db, err := sqlitekit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:min(max(from, 1), len(migrations))] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO meta (name, value) VALUES ('epoch', 'e'), ('modseq', '0')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, from)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	saved := migrations
	migrations = append(append([]string{}, saved...), extra...)
	defer func() { migrations = saved }()

	s, err := Open(path)
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
	now := len(migrations)
	newTable := []string{`CREATE TABLE later (x INTEGER)`}
	broken := []string{`CREATE TABLE later (x INTEGER)`, `NOT SQL`}

	checkOpenMigrates(t, now, nil, now, false)
	checkOpenMigrates(t, 0, nil, now, false)
	checkOpenMigrates(t, 1, nil, now, false)
	checkOpenMigrates(t, now, newTable, now+1, false)
	checkOpenMigrates(t, 0, newTable, now+1, false)
	checkOpenMigrates(t, now, broken, 0, true)
	checkOpenMigrates(t, now+1, nil, 0, true)
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

// checkSetDiscs makes an album of one disc declared to have count tracks,
// sets its disc to the given number of new tracks, and checks the tracks'
// positions and the count the disc keeps.
func checkSetDiscs(t *testing.T, count, tracks int, wantPositions []int) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	albumID, err := InsertAlbum(ctx, s.DB, NewAlbum{Title: "A", DiscTitles: []*string{nil}, DiscTrackCounts: []*int{&count}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for range tracks {
		id, err := InsertTrack(ctx, s.DB, NewTrack{Title: "T", DateAdded: "2014-01-01T00:00:00Z"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := SetDiscs(ctx, s.DB, albumID, []Disc{{TrackIDs: ids}}); err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, id := range ids {
		var pos int
		if err := s.DB.QueryRow(`SELECT position FROM track WHERE id = ?`, id).Scan(&pos); err != nil {
			t.Fatal(err)
		}
		got = append(got, pos)
	}
	if fmt.Sprint(got) != fmt.Sprint(wantPositions) {
		t.Errorf("positions %v, want %v", got, wantPositions)
	}
	var kept int
	if err := s.DB.QueryRow(`SELECT track_count FROM disc WHERE album_id = ?`, albumID).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != count {
		t.Errorf("disc keeps %d tracks declared, want %d", kept, count)
	}
}

func TestSetDiscsNumbersFromOne(t *testing.T) {
	checkSetDiscs(t, 12, 3, []int{1, 2, 3})
	checkSetDiscs(t, 2, 2, []int{1, 2})
}
