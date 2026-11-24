package jmap

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
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
	f.Using = []string{CapCore, CapJuke, CapReview}
	ctx := context.Background()
	a, b = "Aaaaa", "Abbbb"
	exec := func(sql string, args ...any) {
		if _, err := f.store.DB.ExecContext(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO artwork (id, sha256, mime, size, dhash) VALUES (?, 'a', 'image/png', 1, 0), (?, 'b', 'image/jpeg', 1, -1)`, a, b)
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
	source := func(title string) any {
		r := f.call("Track/get", map[string]any{"ids": []string{f.ids[title]}, "properties": []string{"numberSource"}})
		return r["list"].([]any)[0].(map[string]any)["numberSource"]
	}
	checkEqual(t, "the track from nowhere", []any{source("Trigger Happy"), source("Smells Like Nirvana")}, []any{"none", nil})

	checkEqual(t, "candidates by first carrier", f.album(devo)["artworkCandidates"], []any{
		map[string]any{"blobId": a, "type": "image/png", "trackIds": []any{f.ids["Whip It"]}, "folder": false, "group": 1.0},
		map[string]any{"blobId": b, "type": "image/jpeg", "trackIds": []any{f.ids["Jocko Homo"]}, "folder": false, "group": 2.0},
	})
	checkEqual(t, "the folder image comes first", f.album(queen)["artworkCandidates"], []any{
		map[string]any{"blobId": a, "type": "image/png", "trackIds": []any{}, "folder": true, "group": 1.0},
		map[string]any{"blobId": b, "type": "image/jpeg", "trackIds": []any{f.ids["Bohemian Rhapsody"]}, "folder": false, "group": 2.0},
	})
	// A third image that hashes like "a" is "a" to the eye: the same group.
	exec := func(sql string, args ...any) {
		if _, err := f.store.DB.ExecContext(context.Background(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO artwork (id, sha256, mime, size, dhash) VALUES ('Aaaa2', 'a2', 'image/jpeg', 1, 3)`)
	exec(`UPDATE track SET picture_id = 'Aaaa2' WHERE id = ?`, f.ids["Jocko Homo"])
	groups := []any{}
	for _, c := range f.album(devo)["artworkCandidates"].([]any) {
		groups = append(groups, c.(map[string]any)["group"])
	}
	checkEqual(t, "near hashes share a group", groups, []any{1.0, 1.0})
	checkEqual(t, "no pictures anywhere", f.album(al)["artworkCandidates"], []any{})

	query := func(filter map[string]any) []string {
		r := f.call("Album/query", map[string]any{"filter": filter, "sort": []map[string]any{{"property": "title"}}})
		return idList(r["ids"])
	}
	checkEqual(t, "one issue", query(map[string]any{"issue": "artTie"}), []string{devo})
	checkEqual(t, "any issue", query(map[string]any{"hasIssues": true}), []string{queen, al, devo})
	checkEqual(t, "no issue", query(map[string]any{"hasIssues": false}), []string{})
	byArtist := f.call("Album/query", map[string]any{"filter": map[string]any{"hasIssues": true},
		"sort": []map[string]any{{"property": "artist"}, {"property": "title"}}})
	checkEqual(t, "by artist, the unnamed last", idList(byArtist["ids"]), []string{queen, al, devo})
	checkMethodError(t, f, "Album/query", map[string]any{"sort": []map[string]any{{"property": "rating"}}}, "unsupportedSort")
	checkMethodError(t, f, "Album/query", map[string]any{"filter": map[string]any{"issue": "badHair"}}, "unsupportedFilter")
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

	// Deciding each track that carries another picture settles a tie, or
	// folder art, without choosing the album's art outright.
	g, _, _ := reviewFixture(t)
	checkEqual(t, "a tie, before", g.album(g.albumOf("Whip It"))["issues"], []any{"artOverrides", "artTie"})
	g.call("Track/set", map[string]any{"update": map[string]any{g.ids["Jocko Homo"]: map[string]any{"artworkBlobId": nil}}})
	checkEqual(t, "a tie, every other picture decided", g.album(g.albumOf("Whip It"))["issues"], []any{})
	checkEqual(t, "folder art, before", g.album(g.albumOf("We Will Rock You"))["issues"], []any{"noArt", "folderArt"})
	g.call("Track/set", map[string]any{"update": map[string]any{g.ids["Bohemian Rhapsody"]: map[string]any{"artworkBlobId": nil}}})
	checkEqual(t, "folder art, the one dissenter decided", g.album(g.albumOf("We Will Rock You"))["issues"], []any{"noArt"})
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
	f.Using = []string{CapCore, CapJuke}

	album := f.album(devo)
	_, hasIssues := album["issues"]
	_, hasCandidates := album["artworkCandidates"]
	checkEqual(t, "review properties absent", []bool{hasIssues, hasCandidates}, []bool{false, false})
	checkMethodError(t, f, "Album/get", map[string]any{"ids": []string{devo}, "properties": []string{"issues"}}, "invalidArguments")
	checkMethodError(t, f, "Track/get", map[string]any{"ids": []string{f.ids["Whip It"]}, "properties": []string{"numberSource"}}, "invalidArguments")
	checkMethodError(t, f, "Album/query", map[string]any{"filter": map[string]any{"hasIssues": true}}, "unsupportedFilter")
	r := f.call("Album/set", map[string]any{"update": map[string]any{devo: map[string]any{"artworkBlobId": a}}})
	e := r["notUpdated"].(map[string]any)[devo].(map[string]any)
	checkEqual(t, "choosing album art", e["type"], "invalidProperties")
	checkSetRefused(t, f, "Whip It", map[string]any{"artworkBlobId": a}, "invalidProperties", "artworkBlobId")
}

// upload posts bytes to the upload endpoint and returns the status and
// the decoded response, or the error text.
func (f *fixture) upload(data []byte) (int, map[string]any, string) {
	f.t.Helper()
	resp, err := http.Post(f.BaseURL+"/jmap/upload/music/", "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if json.Unmarshal(body, &out) != nil {
		return resp.StatusCode, nil, strings.TrimSpace(string(body))
	}
	return resp.StatusCode, out, ""
}

func pngBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// An upload is an image, stored as artwork under its content id, which
// Album/set then takes; anything else is refused.
func TestUpload(t *testing.T) {
	f := newFixture(t, Config{MaxSizeUpload: 4096})
	f.Using = []string{CapCore, CapJuke, CapReview}
	data := pngBytes()
	status, r, _ := f.upload(data)
	checkEqual(t, "status", status, http.StatusCreated)
	checkEqual(t, "response", []any{r["accountId"], r["type"], r["size"]}, []any{"music", "image/png", float64(len(data))})
	id := r["blobId"].(string)
	status, again, _ := f.upload(data)
	checkEqual(t, "the same image again", []any{status, again["blobId"]}, []any{http.StatusCreated, id})

	devo := f.albumOf("Whip It")
	f.call("Album/set", map[string]any{"update": map[string]any{devo: map[string]any{"artworkBlobId": id}}})
	album := f.album(devo)
	checkEqual(t, "set as the album's art", []any{album["artworkBlobId"], album["artworkType"]}, []any{id, "image/png"})
	checkDownload(t, f, id, "", http.StatusOK, string(data))

	status, _, text := f.upload([]byte("this is a text file, not a picture"))
	checkEqual(t, "not an image", []any{status, text}, []any{http.StatusUnsupportedMediaType, "only images can be uploaded"})
	status, _, _ = f.upload(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 5000)...))
	checkEqual(t, "too large", status, http.StatusRequestEntityTooLarge)
	status, _, _ = f.upload(append([]byte("\x89PNG\r\n\x1a\n"), []byte("but not really a PNG")...))
	checkEqual(t, "an image that can't be decoded", status, http.StatusUnsupportedMediaType)
	resp, _ := http.Post(f.BaseURL+"/jmap/upload/nobody/", "image/png", bytes.NewReader(data))
	checkEqual(t, "another account", resp.StatusCode, http.StatusNotFound)
}
