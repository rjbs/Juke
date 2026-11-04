package backfill

import (
	"context"
	"database/sql"
	"testing"
)

func checkReleased(t *testing.T, db *sql.DB, album string, want string, wantModseq int64) {
	t.Helper()
	var got sql.NullString
	var modseq int64
	if err := db.QueryRow(`SELECT released, modseq FROM album WHERE title = ?`, album).Scan(&got, &modseq); err != nil {
		t.Fatal(err)
	}
	if got.String != want || (want != "" && !got.Valid) || modseq != wantModseq {
		t.Errorf("%s: released %v at modseq %d, want %q at %d", album, got, modseq, want, wantModseq)
	}
}

// The album takes the date most of its tracks' tags give; a lone full
// date loses to the year the rest share.
func TestReleasedFromTags(t *testing.T) {
	db, _, _ := setup(t, []track{
		{title: "One", position: 1, tags: `{"TYER":"1997","TDAT":"2105"}`},
		{title: "Two", position: 2, tags: `{"TYER":"1997"}`},
		{title: "Three", position: 3, tags: `{"TYER":"1997"}`},
		{title: "Four", position: 4, tags: `{"TIT2":"undated"}`},
	}, `('One','Record',1,NULL,NULL)`)
	r, err := Released(context.Background(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.FromTags != 1 || r.Without != 0 {
		t.Errorf("report %+v, want 1 from tags, 0 without", r)
	}
	checkReleased(t, db, "Record", "1997-00-00", 6)
}

// With no dates in the tags, the catalog's year dates the album, as a
// year alone; a second run has nothing to do.
func TestReleasedFromCatalog(t *testing.T) {
	db, _, catalog := setup(t, []track{
		{title: "One", position: 1, tags: `{"TIT2":"One"}`},
		{title: "Two", position: 2, tags: `{"TIT2":"Two"}`},
	}, `('One','Record',1,NULL,NULL), ('Two','Record',2,NULL,NULL)`)
	if _, err := catalog.Exec(`ALTER TABLE itunes_audio_track ADD COLUMN year INTEGER; UPDATE itunes_audio_track SET year = 2002`); err != nil {
		t.Fatal(err)
	}
	r, err := Released(context.Background(), db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if r.FromTags != 0 || r.FromCatalog != 1 || r.Without != 0 {
		t.Errorf("report %+v, want 1 from catalog, 0 without", r)
	}
	checkReleased(t, db, "Record", "2002-00-00", 4)

	again, err := Released(context.Background(), db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if again.FromTags != 0 || again.FromCatalog != 0 {
		t.Errorf("second run %+v, want nothing done", again)
	}
	checkReleased(t, db, "Record", "2002-00-00", 4)
}
