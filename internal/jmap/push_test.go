package jmap

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"semiotic.systems/juke/internal/store"
)

type pushEvent struct {
	name string
	data map[string]any
}

// listen opens the event source with the given query and returns its
// events as they arrive.  The channel closes when the server ends the
// stream.
//
// A stream that isn't closeafter=state opens with an event giving the
// state of each type asked for.  That event is not passed on, so that what
// a test sees is what happened after it began to listen; listenFromOpening
// is for the test of the opening event itself.
func (f *fixture) listen(query string) <-chan pushEvent {
	f.t.Helper()
	events := f.listenFromOpening(query)
	if !strings.Contains(query, "closeafter=state") {
		select {
		case ev := <-events:
			checkEqual(f.t, "opening event", ev.name, "state")
		case <-time.After(5 * time.Second):
			f.t.Fatal("no opening state event")
		}
	}
	return events
}

func (f *fixture) listenFromOpening(query string) <-chan pushEvent {
	f.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", f.BaseURL+"/jmap/eventsource?"+query, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("eventsource status %d", resp.StatusCode)
	}
	checkEqual(f.t, "Content-Type", resp.Header.Get("Content-Type"), "text/event-stream")
	events := make(chan pushEvent, 16)
	go func() {
		defer resp.Body.Close()
		defer close(events)
		var ev pushEvent
		lines := bufio.NewScanner(resp.Body)
		for lines.Scan() {
			line := lines.Text()
			switch {
			case line == "":
				if ev.name != "" {
					events <- ev
				}
				ev = pushEvent{}
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.data)
			}
		}
	}()
	return events
}

// checkStateChange waits for a state event and checks which types it
// says changed, and that it gives their current states.
func checkStateChange(t *testing.T, f *fixture, events <-chan pushEvent, wantTypes ...string) {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("the stream ended")
		}
		checkEqual(t, "event", ev.name, "state")
		checkEqual(t, "@type", ev.data["@type"], "StateChange")
		changed, _ := ev.data["changed"].(map[string]any)["music"].(map[string]any)
		want := map[string]any{}
		for _, typ := range wantTypes {
			want[typ] = f.state(typ)
		}
		checkEqual(t, "changed", changed, want)
	case <-time.After(5 * time.Second):
		t.Fatalf("no state event; wanted %v", wantTypes)
	}
}

// checkQuiet checks that no event arrives for a moment.
func checkQuiet(t *testing.T, events <-chan pushEvent) {
	t.Helper()
	select {
	case ev := <-events:
		t.Errorf("unexpected event %s %v", ev.name, ev.data)
	case <-time.After(300 * time.Millisecond):
	}
}

func rate(f *fixture, title string, rating float64) {
	f.call("Track/set", map[string]any{"update": map[string]any{f.ids[title]: map[string]any{"rating": rating}}})
}

func TestPushOpening(t *testing.T) {
	f := newFixture(t, Config{PushPollInterval: time.Hour})
	checkStateChange(t, f, f.listenFromOpening("types=*&closeafter=no&ping=0"), pushTypes...)
	checkStateChange(t, f, f.listenFromOpening("types=Album,Frobnitz&closeafter=no&ping=0"), "Album")
	checkQuiet(t, f.listenFromOpening("types=*&closeafter=state&ping=0"))
}

func TestPushOnSet(t *testing.T) {
	f := newFixture(t, Config{PushPollInterval: time.Hour})
	events := f.listen("types=*&closeafter=no&ping=0")
	checkQuiet(t, events)

	rate(f, "Whip It", 3)
	checkStateChange(t, f, events, "Track")

	f.call("Playlist/set", map[string]any{"create": map[string]any{"p": map[string]any{"title": "New"}}})
	checkStateChange(t, f, events, "Playlist")

	// Moving a track changes both types in one commit.
	f.call("Album/set", map[string]any{"update": map[string]any{
		f.objectID("Album", "Live Killers"): map[string]any{"discs": []any{disc(f.tracks("We Will Rock You", "Brave Sir Robin"))}},
	}})
	checkStateChange(t, f, events, "Album", "Track")
	checkQuiet(t, events)
}

func TestPushTypes(t *testing.T) {
	f := newFixture(t, Config{PushPollInterval: time.Hour})
	events := f.listen("types=Album,Frobnitz&closeafter=no&ping=0")
	rate(f, "Whip It", 3)
	checkQuiet(t, events)
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
	select {
	case ev, ok := <-events:
		if ok {
			t.Errorf("unexpected event %s after closeafter=state", ev.name)
		}
	case <-time.After(5 * time.Second):
		t.Error("the stream stayed open after closeafter=state")
	}
}

func TestPushPing(t *testing.T) {
	f := newFixture(t, Config{PushPollInterval: time.Hour})
	events := f.listen("types=*&closeafter=no&ping=1")
	select {
	case ev := <-events:
		checkEqual(t, "event", ev.name, "ping")
		checkEqual(t, "ping data", ev.data, map[string]any{"interval": float64(1)})
	case <-time.After(5 * time.Second):
		t.Fatal("no ping")
	}
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
		resp, err := http.Get(f.BaseURL + "/jmap/eventsource?" + query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		checkEqual(t, query, resp.StatusCode, http.StatusBadRequest)
	}
}
