package jmap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/rjbs/gizmo/jmaptest"

	"semiotic.systems/juke/internal/artwork"
	"semiotic.systems/juke/internal/store"
)

// checkQuery runs a Track/query and checks the titles it returns, in
// order.
func checkQuery(t *testing.T, f *fixture, filter any, sort []map[string]any, want ...string) {
	t.Helper()
	args := map[string]any{"filter": filter}
	if sort != nil {
		args["sort"] = sort
	}
	got := f.titlesOf(f.call("Track/query", args)["ids"].([]any))
	if sort == nil {
		got, want = sorted(got), sorted(want)
	}
	desc, _ := json.Marshal(args)
	checkEqual(t, "Track/query "+string(desc), got, want)
}

func by(property string, ascending bool) []map[string]any {
	return []map[string]any{{"property": property, "isAscending": ascending}}
}

func TestTrackQueryFilters(t *testing.T) {
	f := newFixture(t, Config{})

	checkQuery(t, f, map[string]any{"title": "WHIP IT"}, nil, "Whip It")
	checkQuery(t, f, map[string]any{"titleContains": "rock"}, nil, "We Will Rock You")
	checkQuery(t, f, map[string]any{"artist": "BJÖRK"}, nil, "Jocko Homo")
	checkQuery(t, f, map[string]any{"artistContains": "python"}, nil, "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"albumTitle": "live killers"}, nil, "We Will Rock You", "Bohemian Rhapsody")
	checkQuery(t, f, map[string]any{"albumTitleContains": "Devo"}, nil, "Whip It", "Jocko Homo")
	checkQuery(t, f, map[string]any{"ratingAtLeast": 4.5}, nil, "We Will Rock You", "Bohemian Rhapsody")
	checkQuery(t, f, map[string]any{"ratingAtMost": 2.5}, nil, "Trigger Happy")
	checkQuery(t, f, map[string]any{"hasRating": false}, nil, "Whip It", "Jocko Homo", "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"playCountAtLeast": 7}, nil, "Smells Like Nirvana", "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"playCountAtMost": 0}, nil, "Trigger Happy", "Jocko Homo")
	checkQuery(t, f, map[string]any{"lastPlayedBefore": "2014-01-01T00:00:00Z"}, nil, "Lost Track")
	checkQuery(t, f, map[string]any{"lastPlayedAfter": "2014-06-14T00:00:00Z"}, nil, "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"lastPlayedWithinDays": 7}, nil, "Smells Like Nirvana", "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"lastPlayedWithinDays": 5}, nil, "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"hasAudio": false}, nil, "Lost Track")
	checkQuery(t, f, map[string]any{"genre": "rock"}, nil, "We Will Rock You", "Bohemian Rhapsody")
	checkQuery(t, f, map[string]any{"genreContains": "WAVE"}, nil, "Whip It", "Jocko Homo")
	checkQuery(t, f, map[string]any{"hasGenre": false}, nil, "Trigger Happy", "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"hasAlbum": false}, nil, "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"hasArtist": false}, nil)
	checkQuery(t, f, map[string]any{"durationAtMost": 170}, nil, "Trigger Happy", "We Will Rock You", "Whip It")
	checkQuery(t, f, map[string]any{"durationAtLeast": 300}, nil, "Bohemian Rhapsody", "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"inPlaylist": f.ids["Brave Sir Robin"]}, nil)

	checkQuery(t, f, map[string]any{"operator": "AND", "conditions": []any{
		map[string]any{"albumTitle": "Off the Deep End"},
		map[string]any{"hasAudio": true},
	}}, nil, "Smells Like Nirvana", "Trigger Happy")
	checkQuery(t, f, map[string]any{"operator": "OR", "conditions": []any{
		map[string]any{"artist": "Devo"},
		map[string]any{"artist": "Monty Python"},
	}}, nil, "Whip It", "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"operator": "NOT", "conditions": []any{
		map[string]any{"hasRating": true},
	}}, nil, "Whip It", "Jocko Homo", "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"operator": "NOT", "conditions": []any{
		map[string]any{"albumTitle": "Off the Deep End"},
	}}, nil, "We Will Rock You", "Bohemian Rhapsody", "Whip It", "Jocko Homo", "Brave Sir Robin")
	checkQuery(t, f, map[string]any{"operator": "NOT", "conditions": []any{
		map[string]any{"ratingAtLeast": 3},
	}}, nil, "Trigger Happy", "Whip It", "Jocko Homo", "Brave Sir Robin")
}

func TestTrackQueryInPlaylist(t *testing.T) {
	f := newFixture(t, Config{})
	playlistID := f.call("Playlist/query", map[string]any{})["ids"].([]any)[0]
	checkQuery(t, f, map[string]any{"inPlaylist": playlistID}, nil, "Brave Sir Robin", "Smells Like Nirvana")
}

func TestTrackQuerySorts(t *testing.T) {
	f := newFixture(t, Config{})
	queen := map[string]any{"artist": "Queen"}
	albumless := map[string]any{"albumTitle": "Off the Deep End"}

	checkQuery(t, f, queen, by("title", true), "Bohemian Rhapsody", "We Will Rock You")
	checkQuery(t, f, queen, by("title", false), "We Will Rock You", "Bohemian Rhapsody")
	checkQuery(t, f, albumless, by("albumOrder", true), "Smells Like Nirvana", "Lost Track", "Trigger Happy")
	checkQuery(t, f, map[string]any{"albumTitleContains": "e"}, by("albumOrder", true),
		"We Will Rock You", "Bohemian Rhapsody",
		"Smells Like Nirvana", "Lost Track", "Trigger Happy",
		"Whip It", "Jocko Homo")

	// Unrated tracks sort last in both directions.
	unratedLast := map[string]any{"operator": "OR", "conditions": []any{queen, map[string]any{"artist": "Devo"}}}
	checkQuery(t, f, unratedLast, by("rating", true), "Bohemian Rhapsody", "We Will Rock You", "Whip It")
	checkQuery(t, f, unratedLast, by("rating", false), "We Will Rock You", "Bohemian Rhapsody", "Whip It")

	// Never played sorts as oldest, so ascending starts with it; a track
	// of unknown length sorts last either way.
	checkQuery(t, f, albumless, by("lastPlayed", true), "Trigger Happy", "Lost Track", "Smells Like Nirvana")
	checkQuery(t, f, albumless, by("lastPlayed", false), "Smells Like Nirvana", "Lost Track", "Trigger Happy")
	checkQuery(t, f, albumless, by("duration", true), "Trigger Happy", "Smells Like Nirvana", "Lost Track")
	checkQuery(t, f, albumless, by("duration", false), "Smells Like Nirvana", "Trigger Happy", "Lost Track")

	// A genre sort folds case and puts the genre-less last; title breaks
	// the ties within a genre.
	checkQuery(t, f, map[string]any{"albumTitleContains": "e"}, append(by("genre", false), by("title", true)...),
		"Bohemian Rhapsody", "We Will Rock You", "Jocko Homo", "Whip It", "Lost Track", "Smells Like Nirvana", "Trigger Happy")
}

func TestTrackQueryErrors(t *testing.T) {
	f := newFixture(t, Config{})
	checkMethodError(t, f, "Track/query", map[string]any{"filter": map[string]any{"composer": "Funk"}}, "unsupportedFilter")
	checkMethodError(t, f, "Track/query", map[string]any{"filter": map[string]any{"ratingAtLeast": "four"}}, "invalidArguments")
	checkMethodError(t, f, "Track/query", map[string]any{"filter": map[string]any{"lastPlayedAfter": "June"}}, "invalidArguments")
	checkMethodError(t, f, "Track/query", map[string]any{"sort": by("composer", true)}, "unsupportedSort")
	checkMethodError(t, f, "Track/query", map[string]any{"anchor": "Tnope"}, "anchorNotFound")
	checkMethodError(t, f, "Album/query", map[string]any{"filter": map[string]any{"title": "x"}}, "unsupportedFilter")
	checkMethodError(t, f, "Track/queryChanges", map[string]any{}, "cannotCalculateChanges")
}

// checkPage runs a Track/query sorted by title over every track and
// checks the page it returns and the position it reports.
func checkPage(t *testing.T, f *fixture, args map[string]any, wantPosition float64, want ...string) {
	t.Helper()
	args["sort"] = by("title", true)
	r := f.call("Track/query", args)
	checkEqual(t, "position", r["position"], wantPosition)
	checkEqual(t, "page", f.titlesOf(r["ids"].([]any)), append([]string{}, want...))
}

func TestTrackQueryPaging(t *testing.T) {
	f := newFixture(t, Config{})
	// By title: Bohemian Rhapsody, Brave Sir Robin, Jocko Homo, Lost Track,
	// Smells Like Nirvana, Trigger Happy, We Will Rock You, Whip It.
	checkPage(t, f, map[string]any{"limit": 2}, 0, "Bohemian Rhapsody", "Brave Sir Robin")
	checkPage(t, f, map[string]any{"position": 6}, 6, "We Will Rock You", "Whip It")
	checkPage(t, f, map[string]any{"position": -2}, 6, "We Will Rock You", "Whip It")
	checkPage(t, f, map[string]any{"position": 20}, 8)
	checkPage(t, f, map[string]any{"anchor": f.ids["Lost Track"], "limit": 1}, 3, "Lost Track")
	checkPage(t, f, map[string]any{"anchor": f.ids["Lost Track"], "anchorOffset": -1, "limit": 2}, 2, "Jocko Homo", "Lost Track")

	r := f.call("Track/query", map[string]any{"limit": 1, "calculateTotal": true})
	checkEqual(t, "total", r["total"], float64(8))
}

func TestServerLimitsQuery(t *testing.T) {
	f := newFixture(t, Config{MaxQueryLimit: 3})
	r := f.call("Track/query", map[string]any{})
	checkEqual(t, "ids returned", len(r["ids"].([]any)), 3)
	checkEqual(t, "limit reported", r["limit"], float64(3))
}

func TestGet(t *testing.T) {
	f := newFixture(t, Config{})
	r := f.call("Track/get", map[string]any{
		"ids":        []string{f.ids["Lost Track"], "Tnope", f.ids["Lost Track"]},
		"properties": []string{"title", "blobId", "rating"},
	})
	checkEqual(t, "list", r["list"], []any{map[string]any{
		"id": f.ids["Lost Track"], "title": "Lost Track", "blobId": nil, "rating": float64(3),
	}})
	checkEqual(t, "notFound", r["notFound"], []any{"Tnope"})

	all := f.call("Track/get", map[string]any{"ids": nil})
	checkEqual(t, "every track", len(all["list"].([]any)), 8)

	checkMethodError(t, f, "Track/get", map[string]any{"properties": []string{"composer"}}, "invalidArguments")
	checkMethodError(t, f, "Track/get", map[string]any{"accountId": "nobody"}, "accountNotFound")
	checkMethodError(t, f, "Track/get", map[string]any{"bogus": 1}, "invalidArguments")
}

func TestGetTooLarge(t *testing.T) {
	f := newFixture(t, Config{MaxObjectsInGet: 5})
	checkMethodError(t, f, "Track/get", map[string]any{"ids": nil}, "requestTooLarge")
	r := f.call("Album/get", map[string]any{"ids": nil})
	checkEqual(t, "albums", len(r["list"].([]any)), 3)
}

func TestAlbumAndPlaylistGet(t *testing.T) {
	f := newFixture(t, Config{})
	albums := map[string]map[string]any{}
	for _, a := range f.call("Album/get", map[string]any{"ids": nil})["list"].([]any) {
		a := a.(map[string]any)
		albums[a["title"].(string)] = a
	}

	discTitles := func(album string) [][]string {
		var out [][]string
		for _, d := range albums[album]["discs"].([]any) {
			out = append(out, f.titlesOf(d.(map[string]any)["trackIds"].([]any)))
		}
		return out
	}
	checkEqual(t, "a gap holds its place", discTitles("Off the Deep End"),
		[][]string{{"Smells Like Nirvana", "Lost Track", "Trigger Happy"}})
	checkEqual(t, "two discs", discTitles("Live Killers"),
		[][]string{{"We Will Rock You"}, {"Bohemian Rhapsody"}})
	checkEqual(t, "a compilation has no artist", albums["We Are Not Devo"]["artist"], nil)

	tracks := f.call("Track/get", map[string]any{"ids": []string{f.ids["Bohemian Rhapsody"], f.ids["Brave Sir Robin"]}, "properties": []string{"albumId"}})["list"].([]any)
	checkEqual(t, "albumId", tracks[0].(map[string]any)["albumId"], albums["Live Killers"]["id"])
	checkEqual(t, "no album", tracks[1].(map[string]any)["albumId"], nil)

	p := f.call("Playlist/get", map[string]any{"ids": nil})["list"].([]any)[0].(map[string]any)
	checkEqual(t, "playlist with a repeat", f.titlesOf(p["trackIds"].([]any)),
		[]string{"Brave Sir Robin", "Smells Like Nirvana", "Brave Sir Robin"})
}

// checkChanges calls /changes and checks which ids it reports.
func checkChanges(t *testing.T, f *fixture, typ, since string, maxChanges int, wantCreated, wantUpdated []string, wantMore bool) string {
	t.Helper()
	args := map[string]any{"sinceState": since}
	if maxChanges > 0 {
		args["maxChanges"] = maxChanges
	}
	r := f.call(typ+"/changes", args)
	checkEqual(t, typ+" created", sorted(f.titlesOf(r["created"].([]any))), sorted(wantCreated))
	checkEqual(t, typ+" updated", sorted(f.titlesOf(r["updated"].([]any))), sorted(wantUpdated))
	checkEqual(t, typ+" destroyed", r["destroyed"], []any{})
	checkEqual(t, typ+" hasMoreChanges", r["hasMoreChanges"], wantMore)
	return r["newState"].(string)
}

func TestSetAndChanges(t *testing.T) {
	f := newFixture(t, Config{})
	before := f.state("Track")

	r := f.call("Track/set", map[string]any{
		"ifInState": before,
		"update": map[string]any{
			f.ids["Whip It"]:         map[string]any{"rating": 3.5, "playCount": 2, "lastPlayed": "2014-06-15T09:30:00.123Z"},
			f.ids["Brave Sir Robin"]: map[string]any{"rating": nil},
		},
	})
	checkEqual(t, "updated", sorted(f.titlesOf(keys(r["updated"]))), []string{"Brave Sir Robin", "Whip It"})
	checkEqual(t, "oldState", r["oldState"], before)

	got := f.call("Track/get", map[string]any{"ids": []string{f.ids["Whip It"]}, "properties": []string{"rating", "playCount", "lastPlayed"}})
	checkEqual(t, "after set", got["list"].([]any)[0], map[string]any{
		"id": f.ids["Whip It"], "rating": 3.5, "playCount": float64(2), "lastPlayed": "2014-06-15T09:30:00Z",
	})

	after := checkChanges(t, f, "Track", before, 0, nil, []string{"Whip It", "Brave Sir Robin"}, false)
	checkEqual(t, "newState matches /get", after, r["newState"])
	checkChanges(t, f, "Track", after, 0, nil, nil, false)
	checkChanges(t, f, "Album", f.state("Album"), 0, nil, nil, false)

	checkMethodError(t, f, "Track/set", map[string]any{"ifInState": before, "update": map[string]any{}}, "stateMismatch")
}

func TestChangesPaging(t *testing.T) {
	f := newFixture(t, Config{})
	before := f.state("Track")
	for _, title := range []string{"Whip It", "Jocko Homo", "Lost Track"} {
		f.call("Track/set", map[string]any{"update": map[string]any{f.ids[title]: map[string]any{"playCount": 99}}})
	}
	mid := checkChanges(t, f, "Track", before, 2, nil, []string{"Whip It", "Jocko Homo"}, true)
	end := checkChanges(t, f, "Track", mid, 2, nil, []string{"Lost Track"}, false)
	checkEqual(t, "final state", end, f.state("Track"))
}

func TestChangesFromAnImport(t *testing.T) {
	f := newFixture(t, Config{})
	r := f.call("Track/changes", map[string]any{"sinceState": strings.SplitN(f.state("Track"), "-", 2)[0] + "-0"})
	checkEqual(t, "everything is created", len(r["created"].([]any)), 8)

	other := newFixture(t, Config{})
	checkMethodError(t, f, "Track/changes", map[string]any{"sinceState": other.state("Track")}, "cannotCalculateChanges")
	checkMethodError(t, f, "Track/changes", map[string]any{"sinceState": "nonsense"}, "cannotCalculateChanges")
	checkMethodError(t, f, "Track/changes", map[string]any{"sinceState": strings.SplitN(f.state("Track"), "-", 2)[0] + "-999"}, "cannotCalculateChanges")
	checkMethodError(t, f, "Track/changes", map[string]any{"sinceState": f.state("Track"), "maxChanges": 0}, "invalidArguments")
}

// checkSetRefused makes a Track/set update of one track and checks it is
// refused with the given error type and properties.
func checkSetRefused(t *testing.T, f *fixture, title string, patch map[string]any, wantType string, wantProps ...string) {
	t.Helper()
	id := f.ids[title]
	if id == "" {
		id = title
	}
	r := f.call("Track/set", map[string]any{"update": map[string]any{id: patch}})
	if r["notUpdated"] == nil {
		t.Errorf("update %v of %s: not refused", patch, title)
		return
	}
	e := r["notUpdated"].(map[string]any)[id].(map[string]any)
	checkEqual(t, "SetError type", e["type"], wantType)
	if len(wantProps) > 0 {
		checkEqual(t, "SetError properties", idList(e["properties"]), wantProps)
	}
}

func TestSetRefusals(t *testing.T) {
	f := newFixture(t, Config{})
	checkSetRefused(t, f, "Whip It", map[string]any{"rating": 3.3}, "invalidProperties", "rating")
	checkSetRefused(t, f, "Whip It", map[string]any{"rating": 0}, "invalidProperties", "rating")
	checkSetRefused(t, f, "Whip It", map[string]any{"rating": 6}, "invalidProperties", "rating")
	checkSetRefused(t, f, "Whip It", map[string]any{"playCount": -1}, "invalidProperties", "playCount")
	checkSetRefused(t, f, "Whip It", map[string]any{"playCount": 1.5}, "invalidProperties", "playCount")
	checkSetRefused(t, f, "Whip It", map[string]any{"lastPlayed": "2014-06-15"}, "invalidProperties", "lastPlayed")
	checkSetRefused(t, f, "Whip It", map[string]any{"title": "Whip It Good", "albumId": nil}, "invalidProperties", "albumId", "title")
	checkSetRefused(t, f, "Tnope", map[string]any{"rating": 3}, "notFound")

	r := f.call("Track/set", map[string]any{
		"create":  map[string]any{"new": map[string]any{"title": "x"}},
		"destroy": []string{f.ids["Whip It"]},
	})
	checkEqual(t, "create forbidden", r["notCreated"].(map[string]any)["new"].(map[string]any)["type"], "forbidden")
	checkEqual(t, "destroy forbidden", r["notDestroyed"].(map[string]any)[f.ids["Whip It"]].(map[string]any)["type"], "forbidden")
	checkEqual(t, "nothing changed", f.call("Track/get", map[string]any{"ids": []string{f.ids["Whip It"]}, "properties": []string{"rating"}})["list"],
		[]any{map[string]any{"id": f.ids["Whip It"], "rating": nil}})
}

func keys(v any) []any {
	var out []any
	for k := range v.(map[string]any) {
		out = append(out, k)
	}
	return out
}

func TestResultReferences(t *testing.T) {
	f := newFixture(t, Config{})
	r := f.Request(
		jmaptest.Do("Album/get", jmaptest.Args{"ids": nil}),
		jmaptest.Do("Track/get", jmaptest.Args{
			"#ids":       jmaptest.Ref("0", "Album/get", "/list/*/discs/*/trackIds"),
			"properties": []string{"title"},
		}),
	)
	var got struct{ List []map[string]any }
	r.Sentence(1).AssertNamed("Track/get").Decode(&got)
	checkEqual(t, "tracks on any album", len(got.List), 7)

	r = f.Request(
		jmaptest.Do("Track/query", jmaptest.Args{}),
		jmaptest.Do("Track/get", jmaptest.Args{"#ids": jmaptest.Ref("0", "Track/get", "/ids")}),
		jmaptest.Do("Track/get", jmaptest.Args{"#ids": jmaptest.Ref("0", "Track/query", "/nope")}),
		jmaptest.Do("Track/get", jmaptest.Args{"ids": nil, "#ids": jmaptest.Ref("0", "Track/query", "/ids")}),
	)
	r.Sentence(1).AssertError("invalidResultReference")
	r.Sentence(2).AssertError("invalidResultReference")
	r.Sentence(3).AssertError("invalidArguments")
}

func TestMethodsAndCapabilities(t *testing.T) {
	f := newFixture(t, Config{})
	checkMethodError(t, f, "Album/import", map[string]any{}, "unknownMethod")
	checkMethodError(t, f, "Track/frobnicate", map[string]any{}, "unknownMethod")

	echo := f.call("Core/echo", map[string]any{"hello": "world"})
	checkEqual(t, "echo", echo["hello"], "world")

	// A method needs its capability in use.
	f.Post(`{"using":["urn:ietf:params:jmap:core"],"methodCalls":[["Track/get",{"accountId":"music"},"a"]]}`).
		Response(t).SingleSentence("error").AssertError("unknownMethod")
}

// checkRequestError posts a raw body and checks the request-level error.
func checkRequestError(t *testing.T, f *fixture, body, wantType string) {
	t.Helper()
	f.Post(body).AssertRequestError(t, wantType)
}

func TestRequestErrors(t *testing.T) {
	f := newFixture(t, Config{MaxCallsInRequest: 1})
	checkRequestError(t, f, `{"using":`, "notJSON")
	checkRequestError(t, f, `{"using":[]}`, "notRequest")
	checkRequestError(t, f, `{"using":["urn:example:nope"],"methodCalls":[]}`, "unknownCapability")
	checkRequestError(t, f, `{"using":[],"methodCalls":[["Core/echo",{},"a"],["Core/echo",{},"b"]]}`, "limit")
}

func TestSession(t *testing.T) {
	f := newFixture(t, Config{})
	s := f.Session()
	checkEqual(t, "primary account", s["primaryAccounts"], map[string]any{CapJuke: "music", CapReview: "music"})
	checkEqual(t, "apiUrl", s["apiUrl"], f.BaseURL+"/jmap/api")
	checkEqual(t, "downloadUrl", s["downloadUrl"], f.BaseURL+"/jmap/download/{accountId}/{blobId}/{name}?type={type}")
	if state, _ := s["state"].(string); state == "" {
		t.Errorf("the session has no state: %v", s["state"])
	}
}

// checkDownload fetches a blob, optionally a byte range of it, and checks
// the status and body.
func checkDownload(t *testing.T, f *fixture, blobID, rangeHeader string, wantStatus int, wantBody string) {
	t.Helper()
	got := f.DownloadRange(blobID, "song.mp3", "audio/mpeg", rangeHeader)
	checkEqual(t, "download status "+rangeHeader, got.Status, wantStatus)
	if wantStatus < 300 {
		checkEqual(t, "download body "+rangeHeader, string(got.Body), wantBody)
	}
}

func TestDownload(t *testing.T) {
	f := newFixture(t, Config{})
	blob := func(title string) string {
		r := f.call("Track/get", map[string]any{"ids": []string{f.ids[title]}, "properties": []string{"blobId"}})
		return r["list"].([]any)[0].(map[string]any)["blobId"].(string)
	}
	robin := blob("Brave Sir Robin")
	checkDownload(t, f, robin, "", http.StatusOK, "BRAVE-SIR-ROBIN")
	checkDownload(t, f, robin, "bytes=6-8", http.StatusPartialContent, "SIR")
	checkDownload(t, f, robin, "bytes=10-", http.StatusPartialContent, "ROBIN")
	checkDownload(t, f, "Gnotablob", "", http.StatusNotFound, "")
}

// checkBlobType gets one track's blobType and compares it with the
// expected media type, or nil for a track with no audio.
func checkBlobType(t *testing.T, f *fixture, title string, want any) {
	t.Helper()
	r := f.call("Track/get", map[string]any{"ids": []string{f.ids[title]}, "properties": []string{"blobType"}})
	checkEqual(t, title, r["list"].([]any)[0].(map[string]any)["blobType"], want)
}

func TestBlobType(t *testing.T) {
	f := newFixture(t, Config{})
	checkBlobType(t, f, "Whip It", "audio/mpeg")
	checkBlobType(t, f, "We Will Rock You", "audio/mp4")
	checkBlobType(t, f, "Lost Track", nil)
}

func TestTrackQueryCountOnly(t *testing.T) {
	f := newFixture(t, Config{})
	r := f.call("Track/query", map[string]any{"limit": 0, "calculateTotal": true})
	checkEqual(t, "no ids", r["ids"], []any{})
	checkEqual(t, "total", r["total"], float64(8))
	checkEqual(t, "position", r["position"], float64(0))
	r = f.call("Track/query", map[string]any{"limit": 0, "position": 20, "calculateTotal": true})
	checkEqual(t, "position past the end", r["position"], float64(8))

	// A capped query can't be counted without running it in full.
	r = f.call("Track/query", map[string]any{
		"filter": map[string]any{"albumTitle": "Off the Deep End"}, "sort": by("title", true),
		"limitSeconds": 300, "limit": 0, "calculateTotal": true,
	})
	checkEqual(t, "capped total", r["total"], float64(2))
}

// An image stored as album art is a blob too: named by Album and Track,
// and served with its own media type.
func TestArtworkBlobs(t *testing.T) {
	f := newFixture(t, Config{})
	ctx := context.Background()
	data := []byte("\x89PNG\r\n\x1a\nnot really a picture")
	sum := sha256.Sum256(data)
	id := store.ArtworkID(hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(artwork.Dir(f.library), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artwork.Path(f.library, id), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.ExecContext(ctx, `INSERT INTO artwork (id, sha256, mime, size) VALUES (?, ?, 'image/png', ?)`, id, hex.EncodeToString(sum[:]), len(data)); err != nil {
		t.Fatal(err)
	}
	albumID := f.call("Track/get", map[string]any{"ids": []string{f.ids["Whip It"]}, "properties": []string{"albumId"}})["list"].([]any)[0].(map[string]any)["albumId"].(string)
	if _, err := f.store.DB.ExecContext(ctx, `UPDATE album SET artwork_id = ? WHERE id = ?`, id, albumID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.ExecContext(ctx, `UPDATE track SET artwork_id = ? WHERE id = ?`, id, f.ids["Brave Sir Robin"]); err != nil {
		t.Fatal(err)
	}

	art := func(typ, id string) []any {
		r := f.call(typ+"/get", map[string]any{"ids": []string{id}, "properties": []string{"artworkBlobId", "artworkType"}})
		obj := r["list"].([]any)[0].(map[string]any)
		return []any{obj["artworkBlobId"], obj["artworkType"]}
	}
	checkEqual(t, "album art", art("Album", albumID), []any{id, "image/png"})
	checkEqual(t, "track with its own art", art("Track", f.ids["Brave Sir Robin"]), []any{id, "image/png"})
	checkEqual(t, "track with its album's art", art("Track", f.ids["Whip It"]), []any{nil, nil})

	got := f.Download(id, "cover", "")
	checkEqual(t, "artwork status", got.Status, http.StatusOK)
	checkEqual(t, "artwork type", got.Header.Get("Content-Type"), "image/png")
	checkEqual(t, "artwork body", string(got.Body), string(data))
}
