package jmap

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rjbs/gizmo/jmaptest"

	"semiotic.systems/juke/internal/store"
)

// listen opens the event source with the given query.
//
// A stream that isn't closeafter=state opens with an event giving the
// state of each type asked for.  That event is not passed on, so that what
// a test sees is what happened after it began to listen; listenFromOpening
// is for the test of the opening event itself.
func (f *fixture) listen(query string) *jmaptest.Stream {
	f.t.Helper()
	stream := f.listenFromOpening(query)
	if !strings.Contains(query, "closeafter=state") {
		stream.NextState()
	}
	return stream
}

func (f *fixture) listenFromOpening(query string) *jmaptest.Stream {
	f.t.Helper()
	stream := f.Listen(query)
	if stream.Status != http.StatusOK {
		f.t.Fatalf("eventsource status %d", stream.Status)
	}
	checkEqual(f.t, "Content-Type", stream.Header.Get("Content-Type"), "text/event-stream")
	return stream
}

// checkStateChange waits for a state event and checks which types it
// says changed, and that it gives their current states.
func checkStateChange(t *testing.T, f *fixture, stream *jmaptest.Stream, wantTypes ...string) {
	t.Helper()
	changed := stream.NextState()["music"]
	want := map[string]string{}
	for _, typ := range wantTypes {
		want[typ] = f.state(typ)
	}
	checkEqual(t, "changed", changed, want)
}

func rate(f *fixture, title string, rating float64) {
	f.call("Track/set", map[string]any{"update": map[string]any{f.ids[title]: map[string]any{"rating": rating}}})
}

func TestPushOpening(t *testing.T) {
	f := newFixture(t, Config{PushPollInterval: time.Hour})
	checkStateChange(t, f, f.listenFromOpening("types=*&closeafter=no&ping=0"), pushTypes...)
	checkStateChange(t, f, f.listenFromOpening("types=Album,Frobnitz&closeafter=no&ping=0"), "Album")
	f.listenFromOpening("types=*&closeafter=state&ping=0").AssertQuiet()
}

func TestPushOnSet(t *testing.T) {
	f := newFixture(t, Config{PushPollInterval: time.Hour})
	events := f.listen("types=*&closeafter=no&ping=0")
	events.AssertQuiet()

	rate(f, "Whip It", 3)
	checkStateChange(t, f, events, "Track")

	f.call("Playlist/set", map[string]any{"create": map[string]any{"p": map[string]any{"title": "New"}}})
	checkStateChange(t, f, events, "Playlist")

	// Moving a track changes both types in one commit.
	f.call("Album/set", map[string]any{"update": map[string]any{
		f.objectID("Album", "Live Killers"): map[string]any{"discs": []any{disc(f.tracks("We Will Rock You", "Brave Sir Robin"))}},
	}})
	checkStateChange(t, f, events, "Album", "Track")
	events.AssertQuiet()
}

func TestPushTypes(t *testing.T) {
	f := newFixture(t, Config{PushPollInterval: time.Hour})
	events := f.listen("types=Album,Frobnitz&closeafter=no&ping=0")
	rate(f, "Whip It", 3)
	events.AssertQuiet()
	f.call("Album/set", map[string]any{"update": map[string]any{
		f.objectID("Album", "Live Killers"): map[string]any{"title": "Live Killers!"},
	}})
	checkStateChange(t, f, events, "Album")
}

func TestPushCloseAfterState(t *testing.T) {
	f := newFixture(t, Config{PushPollInterval: time.Hour})
	events := f.listen("types=*&closeafter=state&ping=0")
	rate(f, "Whip It", 3)
	checkStateChange(t, f, events, "Track")
	events.AssertClosed()
}

func TestPushPing(t *testing.T) {
	f := newFixture(t, Config{PushPollInterval: time.Hour})
	events := f.listen("types=*&closeafter=no&ping=1")
	ev := events.Next()
	var data map[string]any
	ev.Decode(t, &data)
	checkEqual(t, "event", ev.Type, "ping")
	checkEqual(t, "ping data", data, map[string]any{"interval": float64(1)})
}

// A write that doesn't go through the server, as an import's doesn't, is
// found by polling.
func TestPushSeesOutsideWrites(t *testing.T) {
	f := newFixture(t, Config{PushPollInterval: 50 * time.Millisecond})
	events := f.listen("types=*&closeafter=no&ping=0")
	if _, err := store.InsertPlaylist(context.Background(), f.store.DB, "Imported", nil); err != nil {
		t.Fatal(err)
	}
	checkStateChange(t, f, events, "Playlist")
}

func TestPushRefusals(t *testing.T) {
	f := newFixture(t, Config{})
	for _, query := range []string{"closeafter=sometimes", "ping=soon", "ping=-1"} {
		checkEqual(t, query, f.Listen(query).Status, http.StatusBadRequest)
	}
}
