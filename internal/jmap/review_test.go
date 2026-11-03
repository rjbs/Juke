package jmap

import (
	"context"
	"testing"
)

// reviewFixture is the usual fixture with some trouble in it, and calls
// made under the review capability: two images, "a" and "b"; We Are Not
// Devo's art chosen by the rule on a tie, with Jocko Homo carrying "b"
// over Whip It's "a"; Live Killers with no art but an image file "a"
// beside its tracks, one of which carries "b"; and Trigger Happy
// numbered from nowhere on an album with a gap.
func reviewFixture(t *testing.T) (f *fixture, a, b string) {
	t.Helper()
	f = newFixture(t, Config{})
	f.using = []string{CapCore, CapJuke, CapReview}
	ctx := context.Background()
	a, b = "Aaaaa", "Abbbb"
	exec := func(sql string, args ...any) {
		if _, err := f.store.DB.ExecContext(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO artwork (id, sha256, mime, size) VALUES (?, 'a', 'image/png', 1), (?, 'b', 'image/jpeg', 1)`, a, b)
	exec(`UPDATE album SET artwork_id = ?, artwork_source = 'rule', art_tie = 1 WHERE id = ?`, a, f.albumOf("Whip It"))
	exec(`UPDATE track SET picture_id = ? WHERE id = ?`, a, f.ids["Whip It"])
	exec(`UPDATE track SET picture_id = ?, artwork_id = ?, artwork_source = 'rule' WHERE id = ?`, b, b, f.ids["Jocko Homo"])
	exec(`UPDATE album SET folder_artwork_id = ? WHERE id = ?`, a, f.albumOf("We Will Rock You"))
	exec(`UPDATE track SET picture_id = ? WHERE id = ?`, b, f.ids["Bohemian Rhapsody"])
	exec(`UPDATE track SET number_source = 'none' WHERE id = ?`, f.ids["Trigger Happy"])
	return f, a, b
}

func (f *fixture) albumOf(title string) string {
	f.t.Helper()
	r := f.call("Track/get", map[string]any{"ids": []string{f.ids[title]}, "properties": []string{"albumId"}})
	return r["list"].([]any)[0].(map[string]any)["albumId"].(string)
}

// album fetches one album's properties, under whatever the fixture is using.
func (f *fixture) album(id string, properties ...string) map[string]any {
	f.t.Helper()
	args := map[string]any{"ids": []string{id}}
	if properties != nil {
		args["properties"] = properties
	}
	return f.call("Album/get", args)["list"].([]any)[0].(map[string]any)
}

func TestReviewIssuesAndCandidates(t *testing.T) {
	f, a, b := reviewFixture(t)
	devo, queen, al := f.albumOf("Whip It"), f.albumOf("We Will Rock You"), f.albumOf("Smells Like Nirvana")

	checkEqual(t, "a tie with an override", f.album(devo)["issues"], []any{"artOverrides", "artTie"})
	checkEqual(t, "no art, and folder art the tracks disagree with", f.album(queen)["issues"], []any{"noArt", "folderArt"})
	checkEqual(t, "a gap and a track from nowhere", f.album(al)["issues"], []any{"noArt", "unnumbered", "gaps"})

	checkEqual(t, "candidates by first carrier", f.album(devo)["artworkCandidates"], []any{
		map[string]any{"blobId": a, "type": "image/png", "trackIds": []any{f.ids["Whip It"]}, "folder": false},
		map[string]any{"blobId": b, "type": "image/jpeg", "trackIds": []any{f.ids["Jocko Homo"]}, "folder": false},
	})
	checkEqual(t, "the folder image comes first", f.album(queen)["artworkCandidates"], []any{
		map[string]any{"blobId": a, "type": "image/png", "trackIds": []any{}, "folder": true},
		map[string]any{"blobId": b, "type": "image/jpeg", "trackIds": []any{f.ids["Bohemian Rhapsody"]}, "folder": false},
	})
	checkEqual(t, "no pictures anywhere", f.album(al)["artworkCandidates"], []any{})

	query := func(filter map[string]any) []string {
		r := f.call("Album/query", map[string]any{"filter": filter, "sort": []map[string]any{{"property": "title"}}})
		return idList(r["ids"])
	}
	checkEqual(t, "one issue", query(map[string]any{"issue": "artTie"}), []string{devo})
	checkEqual(t, "any issue", query(map[string]any{"hasIssues": true}), []string{queen, al, devo})
	checkEqual(t, "no issue", query(map[string]any{"hasIssues": false}), []string{})
	r := f.calls("Album/query", map[string]any{"filter": map[string]any{"issue": "badHair"}})[0]
	checkEqual(t, "an unknown issue", r.Args["type"], "unsupportedFilter")
}

func TestReviewChoosesArt(t *testing.T) {
	f, a, b := reviewFixture(t)
	devo := f.albumOf("Whip It")
	before := f.state("Track")

	r := f.call("Album/set", map[string]any{"update": map[string]any{devo: map[string]any{"artworkBlobId": b}}})
	checkEqual(t, "album updated", keys(r["updated"]), []any{devo})
	album := f.album(devo)
	checkEqual(t, "the chosen art", []any{album["artworkBlobId"], album["artworkType"]}, []any{b, "image/jpeg"})
	checkEqual(t, "choosing settles the art issues", album["issues"], []any{})
	track := f.call("Track/get", map[string]any{"ids": []string{f.ids["Jocko Homo"]}, "properties": []string{"artworkBlobId"}})["list"].([]any)[0].(map[string]any)
	checkEqual(t, "the override is cleared", track["artworkBlobId"], nil)
	checkChanges(t, f, "Track", before, 10, nil, []string{"Jocko Homo"}, false)

	f.call("Track/set", map[string]any{"update": map[string]any{f.ids["Whip It"]: map[string]any{"artworkBlobId": a}}})
	track = f.call("Track/get", map[string]any{"ids": []string{f.ids["Whip It"]}, "properties": []string{"artworkBlobId", "artworkType"}})["list"].([]any)[0].(map[string]any)
	checkEqual(t, "a track's own art", []any{track["artworkBlobId"], track["artworkType"]}, []any{a, "image/png"})
	checkEqual(t, "an override chosen by hand is no issue", f.album(devo)["issues"], []any{})

	f.call("Album/set", map[string]any{"update": map[string]any{devo: map[string]any{"artworkBlobId": nil}}})
	checkEqual(t, "no art, by choice", f.album(devo)["artworkBlobId"], nil)
	checkEqual(t, "which is no issue either", f.album(devo)["issues"], []any{})

	r = f.call("Album/set", map[string]any{"update": map[string]any{devo: map[string]any{"artworkBlobId": "Anope"}}})
	e := r["notUpdated"].(map[string]any)[devo].(map[string]any)
	checkEqual(t, "an image not stored", []any{e["type"], idList(e["properties"])}, []any{"invalidProperties", []string{"artworkBlobId"}})
	checkSetRefused(t, f, "Whip It", map[string]any{"artworkBlobId": "Anope"}, "invalidProperties", "artworkBlobId")
}

// Without the review capability, Album is as it was: no issues, no
// candidates, no filter, and no choosing art.
func TestReviewNeedsItsCapability(t *testing.T) {
	f, a, _ := reviewFixture(t)
	devo := f.albumOf("Whip It")
	f.using = nil

	album := f.album(devo)
	_, hasIssues := album["issues"]
	_, hasCandidates := album["artworkCandidates"]
	checkEqual(t, "review properties absent", []bool{hasIssues, hasCandidates}, []bool{false, false})
	r := f.calls("Album/get", map[string]any{"ids": []string{devo}, "properties": []string{"issues"}})[0]
	checkEqual(t, "asking for one", r.Args["type"], "invalidArguments")
	r = f.calls("Album/query", map[string]any{"filter": map[string]any{"hasIssues": true}})[0]
	checkEqual(t, "filtering", r.Args["type"], "unsupportedFilter")
	r = f.calls("Album/set", map[string]any{"update": map[string]any{devo: map[string]any{"artworkBlobId": a}}})[0]
	e := r.Args["notUpdated"].(map[string]any)[devo].(map[string]any)
	checkEqual(t, "choosing album art", e["type"], "invalidProperties")
	checkSetRefused(t, f, "Whip It", map[string]any{"artworkBlobId": a}, "invalidProperties", "artworkBlobId")
}
