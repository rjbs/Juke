package jmap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"semiotic.systems/juke/internal/store"
)

// The fixture's clock: "today" for relative date filters.
var fixtureNow = time.Date(2014, 6, 15, 12, 0, 0, 0, time.UTC)

// fixture is a running server over a small, known library.  Tracks are
// named by title in ids, which every title in the fixture keeps unique.
type fixture struct {
	t       *testing.T
	srv     *httptest.Server
	store   *store.Store
	library string
	ids     map[string]string
	titles  map[string]string
}

type fixtureTrack struct {
	title      string
	artist     string
	rating     float64 // 0 for none
	playCount  int64
	lastPlayed string // "" for never
	file       string // "" for a gap
	content    string
}

func newFixture(t *testing.T, cfg Config) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.Create(filepath.Join(dir, "juke.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	f := &fixture{t: t, store: s, library: filepath.Join(dir, "library"), ids: map[string]string{}, titles: map[string]string{}}

	addAlbum := func(title, artist string, discs ...[]fixtureTrack) {
		var a *string
		if artist != "" {
			a = &artist
		}
		albumID, err := store.InsertAlbum(ctx, s.DB, store.NewAlbum{Title: title, Artist: a, DiscTitles: make([]*string, len(discs))})
		if err != nil {
			t.Fatal(err)
		}
		for di, tracks := range discs {
			for pos, tr := range tracks {
				f.addTrack(&albumID, &di, &pos, tr)
			}
		}
	}

	addAlbum("Off the Deep End", "Weird Al Yankovic", []fixtureTrack{
		{title: "Smells Like Nirvana", artist: "Weird Al Yankovic", rating: 4, playCount: 10, lastPlayed: "2014-06-10T08:00:00Z", file: "smells.mp3", content: "SMELLS-LIKE-NIRVANA-AUDIO"},
		{title: "Lost Track", artist: "Weird Al Yankovic", rating: 3, playCount: 2, lastPlayed: "2010-01-01T00:00:00Z"},
		{title: "Trigger Happy", artist: "Weird Al Yankovic", rating: 2.5, file: "trigger.mp3", content: "TRIGGER-HAPPY"},
	})
	addAlbum("Live Killers", "Queen",
		[]fixtureTrack{{title: "We Will Rock You", artist: "Queen", rating: 5, playCount: 4, file: "rock.m4a", content: "ROCK"}},
		[]fixtureTrack{{title: "Bohemian Rhapsody", artist: "Queen", rating: 4.5, playCount: 4, file: "rhapsody.m4a", content: "RHAPSODY"}},
	)
	addAlbum("We Are Not Devo", "", []fixtureTrack{
		{title: "Whip It", artist: "Devo", playCount: 1, file: "whip.mp3", content: "WHIP"},
		{title: "Jocko Homo", artist: "Björk", file: "jocko.mp3", content: "JOCKO"},
	})
	f.addTrack(nil, nil, nil, fixtureTrack{
		title: "Brave Sir Robin", artist: "Monty Python", playCount: 7, lastPlayed: "2014-06-14T20:00:00Z",
		file: "robin.mp3", content: "BRAVE-SIR-ROBIN",
	})
	if _, err := store.InsertPlaylist(ctx, s.DB, "Mix", []string{
		f.ids["Brave Sir Robin"], f.ids["Smells Like Nirvana"], f.ids["Brave Sir Robin"],
	}); err != nil {
		t.Fatal(err)
	}

	cfg.Store = s
	cfg.Library = f.library
	cfg.Now = func() time.Time { return fixtureNow }
	cfg.Log = log.New(io.Discard, "", 0)
	f.srv = httptest.NewServer(New(cfg).Handler())
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) addTrack(albumID *string, disc, pos *int, tr fixtureTrack) {
	f.t.Helper()
	nt := store.NewTrack{Title: tr.title, PlayCount: tr.playCount, DateAdded: "2014-01-01T00:00:00Z", AlbumID: albumID, DiscIndex: disc, Position: pos}
	if tr.artist != "" {
		nt.Artist = &tr.artist
	}
	if tr.rating != 0 {
		nt.Rating = &tr.rating
	}
	if tr.lastPlayed != "" {
		nt.LastPlayed = &tr.lastPlayed
	}
	if tr.file != "" {
		if err := os.MkdirAll(f.library, 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.library, tr.file), []byte(tr.content), 0o644); err != nil {
			f.t.Fatal(err)
		}
		size := int64(len(tr.content))
		ct := "audio/mpeg"
		nt.Path, nt.Size, nt.ContentType = &tr.file, &size, &ct
	}
	id, err := store.InsertTrack(context.Background(), f.store.DB, nt)
	if err != nil {
		f.t.Fatal(err)
	}
	f.ids[tr.title] = id
	f.titles[id] = tr.title
}

// response is one method response.
type response struct {
	Name   string
	Args   map[string]any
	CallID string
}

// post sends a raw API request body and returns the HTTP status and the
// decoded JSON body.
func (f *fixture) post(body string) (int, map[string]any) {
	f.t.Helper()
	resp, err := http.Post(f.srv.URL+"/jmap/api", "application/json", bytes.NewBufferString(body))
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		f.t.Fatalf("decoding response: %v", err)
	}
	return resp.StatusCode, out
}

// calls makes one API request of several method calls, each given as
// method name and arguments; call ids are "0", "1" and so on.  An
// arguments map without accountId gets the fixture's account.
func (f *fixture) calls(calls ...any) []response {
	f.t.Helper()
	var methodCalls []any
	for i := 0; i < len(calls); i += 2 {
		args := calls[i+1].(map[string]any)
		if _, ok := args["accountId"]; !ok {
			args["accountId"] = "music"
		}
		methodCalls = append(methodCalls, []any{calls[i], args, string(rune('0' + i/2))})
	}
	body, _ := json.Marshal(map[string]any{
		"using":       []string{CapCore, CapJuke},
		"methodCalls": methodCalls,
	})
	status, out := f.post(string(body))
	if status != http.StatusOK {
		f.t.Fatalf("API status %d: %v", status, out)
	}
	var responses []response
	for _, r := range out["methodResponses"].([]any) {
		triple := r.([]any)
		responses = append(responses, response{triple[0].(string), triple[1].(map[string]any), triple[2].(string)})
	}
	return responses
}

// call makes a single method call and returns its response, failing the
// test if it is an error.
func (f *fixture) call(method string, args map[string]any) map[string]any {
	f.t.Helper()
	r := f.calls(method, args)[0]
	if r.Name == "error" {
		f.t.Fatalf("%s: error %v", method, r.Args)
	}
	return r.Args
}

func (f *fixture) state(typ string) string {
	f.t.Helper()
	return f.call(typ+"/get", map[string]any{"ids": []string{}})["state"].(string)
}

func (f *fixture) titlesOf(ids []any) []string {
	out := []string{}
	for _, id := range ids {
		out = append(out, f.titles[id.(string)])
	}
	return out
}

func idList(v any) []string {
	out := []string{}
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func sorted(s []string) []string {
	s = append([]string{}, s...)
	sort.Strings(s)
	return s
}

func checkEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %#v\nwant %#v", what, got, want)
	}
}

// checkMethodError makes a call and checks that it fails with the given
// JMAP error type.
func checkMethodError(t *testing.T, f *fixture, method string, args map[string]any, wantType string) {
	t.Helper()
	r := f.calls(method, args)[0]
	if r.Name != "error" {
		t.Errorf("%s %v: got %s, want error %s", method, args, r.Name, wantType)
		return
	}
	checkEqual(t, method+" error type", r.Args["type"], wantType)
}
