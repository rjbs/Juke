package jmap

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rjbs/gizmo/jmaptest"

	"semiotic.systems/juke/internal/store"
)

// The fixture's clock: "today" for relative date filters.
var fixtureNow = time.Date(2014, 6, 15, 12, 0, 0, 0, time.UTC)

// fixture is a running server over a small, known library.  Tracks are
// named by title in ids, which every title in the fixture keeps unique.
type fixture struct {
	*jmaptest.Tester
	t       *testing.T
	store   *store.Store
	library string
	ids     map[string]string
	titles  map[string]string
}

type fixtureTrack struct {
	title      string
	artist     string
	genre      string  // "" for none
	duration   int64   // seconds; 0 for unknown
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
		{title: "Smells Like Nirvana", artist: "Weird Al Yankovic", genre: "Comedy", duration: 218, rating: 4, playCount: 10, lastPlayed: "2014-06-10T08:00:00Z", file: "smells.mp3", content: "SMELLS-LIKE-NIRVANA-AUDIO"},
		{title: "Lost Track", artist: "Weird Al Yankovic", genre: "Comedy", rating: 3, playCount: 2, lastPlayed: "2010-01-01T00:00:00Z"},
		{title: "Trigger Happy", artist: "Weird Al Yankovic", duration: 170, rating: 2.5, file: "trigger.mp3", content: "TRIGGER-HAPPY"},
	})
	addAlbum("Live Killers", "Queen",
		[]fixtureTrack{{title: "We Will Rock You", artist: "Queen", genre: "Rock", duration: 122, rating: 5, playCount: 4, file: "rock.m4a", content: "ROCK"}},
		[]fixtureTrack{{title: "Bohemian Rhapsody", artist: "Queen", genre: "Rock", duration: 355, rating: 4.5, playCount: 4, file: "rhapsody.m4a", content: "RHAPSODY"}},
	)
	addAlbum("We Are Not Devo", "", []fixtureTrack{
		{title: "Whip It", artist: "Devo", genre: "New Wave", duration: 160, playCount: 1, file: "whip.mp3", content: "WHIP"},
		{title: "Jocko Homo", artist: "Björk", genre: "New Wave", file: "jocko.mp3", content: "JOCKO"},
	})
	f.addTrack(nil, nil, nil, fixtureTrack{
		title: "Brave Sir Robin", artist: "Monty Python", duration: 300, playCount: 7, lastPlayed: "2014-06-14T20:00:00Z",
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
	srv := New(cfg)
	watching, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	go srv.Watch(watching)
	web := httptest.NewServer(srv.Handler())
	t.Cleanup(web.Close)
	f.Tester = jmaptest.New(t, web.URL, CapCore, CapJuke)
	f.AccountID = "music"
	return f
}

func (f *fixture) addTrack(albumID *string, disc, pos *int, tr fixtureTrack) {
	f.t.Helper()
	nt := store.NewTrack{Title: tr.title, PlayCount: tr.playCount, DateAdded: "2014-01-01T00:00:00Z", AlbumID: albumID, DiscIndex: disc, Position: pos}
	if tr.artist != "" {
		nt.Artist = &tr.artist
	}
	if tr.genre != "" {
		nt.Genre = &tr.genre
	}
	if tr.duration != 0 {
		nt.Duration = &tr.duration
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
		if strings.HasSuffix(tr.file, ".m4a") {
			ct = "audio/mp4"
		}
		nt.Path, nt.Size, nt.ContentType = &tr.file, &size, &ct
	}
	id, err := store.InsertTrack(context.Background(), f.store.DB, nt)
	if err != nil {
		f.t.Fatal(err)
	}
	f.ids[tr.title] = id
	f.titles[id] = tr.title
}

// call makes a single method call and returns its response's arguments,
// failing the test if it is an error.  The arguments are decoded plainly,
// so that numbers are float64.
func (f *fixture) call(method string, args jmaptest.Args) map[string]any {
	f.t.Helper()
	var out map[string]any
	f.Request(jmaptest.Do(method, args)).SingleSentence(method).Decode(&out)
	return out
}

func (f *fixture) state(typ string) string {
	f.t.Helper()
	return f.Request(jmaptest.Do(typ+"/get", jmaptest.Args{"ids": []string{}})).SingleSentence(typ + "/get").Str("state")
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
	f.Request(jmaptest.Do(method, args)).SingleSentence("error").AssertError(wantType)
}
