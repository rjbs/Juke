package backfill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// checkNumbers runs the numbers backfill over an album of tracks and
// checks where each ends up, and the report.  A track's file name is
// where its number may come from; a journal line moves a track's file to
// the originals store, so only the journal remembers its name.
func checkNumbers(t *testing.T, name string, tracks []track, catalogRows string, journalFor map[string]string, want map[string]int, wantReport NumbersReport) {
	t.Helper()
	db, library, catalog := setup(t, tracks, catalogRows)
	ctx := context.Background()
	journal := filepath.Join(filepath.Dir(library), "journal")
	var lines string
	for title, from := range journalFor {
		var id string
		if err := db.QueryRow(`SELECT id FROM track WHERE title = ?`, title).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE track SET path = ? WHERE id = ?`, "originals/ab/abcdef.mp3", id); err != nil {
			t.Fatal(err)
		}
		lines += id + "\t" + from + "\toriginals/ab/abcdef.mp3\n"
	}
	if err := os.WriteFile(journal, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := db.QueryRow(`SELECT max(modseq) FROM track`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	r, err := Numbers(ctx, db, catalog, journal)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if *r != wantReport {
		t.Errorf("%s: report %+v, want %+v", name, *r, wantReport)
	}
	for title, pos := range want {
		var got int
		var modseq int64
		if err := db.QueryRow(`SELECT position, modseq FROM track WHERE title = ?`, title).Scan(&got, &modseq); err != nil {
			t.Fatal(err)
		}
		if got != pos {
			t.Errorf("%s: %s at %d, want %d", name, title, got, pos)
		}
		if moved := modseq > before; moved != (pos != positionOf(tracks, title)) {
			t.Errorf("%s: %s modseq %d, before the run was %d", name, title, modseq, before)
		}
	}
	var albumModseq int64
	if err := db.QueryRow(`SELECT modseq FROM album`).Scan(&albumModseq); err != nil {
		t.Fatal(err)
	}
	if (albumModseq > before) != (wantReport.Albums > 0) {
		t.Errorf("%s: album modseq %d, before the run was %d", name, albumModseq, before)
	}

	again, err := Numbers(ctx, db, catalog, journal)
	if err != nil {
		t.Fatalf("%s, again: %v", name, err)
	}
	if again.Placed != 0 || again.Albums != 0 {
		t.Errorf("%s: a second run placed %d on %d albums", name, again.Placed, again.Albums)
	}
}

func positionOf(tracks []track, title string) int {
	for _, tr := range tracks {
		if tr.title == title {
			return tr.position
		}
	}
	return 0
}

func TestNumbers(t *testing.T) {
	numbered := `{"TIT2":"x","TRCK":"1"}`
	unnumbered := `{"TIT2":"x"}`
	none := `('Nobody', 'Nothing', 1, NULL, NULL)`

	checkNumbers(t, "filename numbers fill the holes",
		[]track{
			{title: "One", position: 1, tags: numbered, file: "Record/01 One.mp3"},
			{title: "Three", position: 2, tags: unnumbered, file: "Record/03 Three.mp3"},
			{title: "Five", position: 3, tags: unnumbered, file: "Record/05 Five.mp3"},
		}, none, nil,
		map[string]int{"One": 1, "Three": 3, "Five": 5},
		NumbersReport{Placed: 2, Albums: 1})

	checkNumbers(t, "a taken spot and a bare name stay put",
		[]track{
			{title: "One", position: 1, tags: numbered, file: "Record/01 One.mp3"},
			{title: "Dupe", position: 2, tags: unnumbered, file: "Record/01 Dupe.mp3"},
			{title: "Plain", position: 3, tags: unnumbered, file: "Record/Plain.mp3"},
		}, none, nil,
		map[string]int{"One": 1, "Dupe": 2, "Plain": 3},
		NumbersReport{Taken: 1, Unnamed: 1})

	checkNumbers(t, "the catalog's number is a number",
		[]track{
			{title: "One", position: 1, tags: numbered, file: "Record/01 One.mp3"},
			{title: "Two", position: 2, tags: unnumbered, file: "Record/09 Two.mp3"},
		}, `('Two', 'Record', 2, NULL, NULL)`, nil,
		map[string]int{"One": 1, "Two": 2},
		NumbersReport{})

	checkNumbers(t, "a gap holds its spot",
		[]track{
			{title: "One", position: 1, tags: numbered, file: "Record/01 One.mp3"},
			{title: "Lost", position: 2},
			{title: "Two", position: 3, tags: unnumbered, file: "Record/02 Two.mp3"},
		}, none, nil,
		map[string]int{"One": 1, "Lost": 2, "Two": 3},
		NumbersReport{Taken: 1})

	checkNumbers(t, "a spot being left can be taken in the same run",
		[]track{
			{title: "Two", position: 1, tags: unnumbered, file: "Record/02 Two.mp3"},
			{title: "One", position: 2, tags: unnumbered, file: "Record/01 One.mp3"},
		}, none, nil,
		map[string]int{"One": 1, "Two": 2},
		NumbersReport{Placed: 2, Albums: 1})

	checkNumbers(t, "a moved file's name comes from the journal",
		[]track{
			{title: "One", position: 1, tags: numbered, file: "Record/01 One.mp3"},
			{title: "Four", position: 2, tags: unnumbered, file: "Record/04 Four.mp3"},
		}, none, map[string]string{"Four": "Record/04 Four.mp3"},
		map[string]int{"One": 1, "Four": 4},
		NumbersReport{Placed: 1, Albums: 1})
}
