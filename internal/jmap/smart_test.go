package jmap

import (
	"reflect"
	"testing"
)

// smartCreate makes one smart playlist and returns its id and what the
// server reported in "created".
func (f *fixture) smartCreate(props map[string]any) (string, map[string]any) {
	f.t.Helper()
	r := f.set("SmartPlaylist", map[string]any{"create": map[string]any{"new": props}})
	created := r["created"].(map[string]any)["new"].(map[string]any)
	return created["id"].(string), created
}

// smartGet fetches one smart playlist.
func (f *fixture) smartGet(id string) map[string]any {
	f.t.Helper()
	list := f.call("SmartPlaylist/get", map[string]any{"ids": []string{id}})["list"].([]any)
	if len(list) != 1 {
		f.t.Fatalf("smart playlist %s not found", id)
	}
	return list[0].(map[string]any)
}

// checkSmartTracks runs a smart playlist through inSmartPlaylist and
// checks the titles it matches, in any order.
func checkSmartTracks(t *testing.T, f *fixture, id string, want ...string) {
	t.Helper()
	checkQuery(t, f, map[string]any{"inSmartPlaylist": id}, nil, want...)
}

func seedOf(sort any) string {
	first := sort.([]any)[0].(map[string]any)
	seed, _ := first["seed"].(string)
	return seed
}

func TestSmartPlaylistSet(t *testing.T) {
	f := newFixture(t, Config{})

	// A create reports what the server filled in: the seed it gave the
	// random sort, and the defaults of what wasn't given.
	rock, created := f.smartCreate(map[string]any{
		"title":  "Rock, shuffled",
		"filter": map[string]any{"genre": "Rock"},
		"sort":   []map[string]any{{"property": "random"}},
		"limit":  10,
	})
	if seedOf(created["sort"]) == "" {
		t.Errorf("created = %v, want a seeded sort", created)
	}
	if _, given := created["filter"]; given {
		t.Errorf("created = %v, reports the filter the client gave", created)
	}
	if v, ok := created["limitBytes"]; !ok || v != nil {
		t.Errorf("created = %v, want the limitBytes default", created)
	}

	got := f.smartGet(rock)
	checkEqual(t, "title", got["title"], "Rock, shuffled")
	checkEqual(t, "filter", got["filter"], map[string]any{"genre": "Rock"})
	checkEqual(t, "limit", got["limit"], float64(10))
	checkEqual(t, "limitSeconds", got["limitSeconds"], nil)
	checkEqual(t, "seed kept", seedOf(got["sort"]), seedOf(created["sort"]))
	checkSmartTracks(t, f, rock, "We Will Rock You", "Bohemian Rhapsody")

	// An update that changes the rule changes what it matches; reseeding
	// the sort is reported back.
	r := f.set("SmartPlaylist", map[string]any{"update": map[string]any{rock: map[string]any{
		"filter": map[string]any{"artist": "Devo"},
		"sort":   []map[string]any{{"property": "random"}},
	}}})
	reseeded := r["updated"].(map[string]any)[rock].(map[string]any)
	if seedOf(reseeded["sort"]) == "" || seedOf(reseeded["sort"]) == seedOf(created["sort"]) {
		t.Errorf("updated = %v, want a new seed", reseeded)
	}
	checkSmartTracks(t, f, rock, "Whip It")
	r = f.set("SmartPlaylist", map[string]any{"update": map[string]any{rock: map[string]any{"title": "Devo"}}})
	checkEqual(t, "a plain update reports nothing", r["updated"].(map[string]any)[rock], nil)

	// A rule may draw on another smart playlist, or a plain one, and
	// neither can be destroyed while it does.
	notDevo, _ := f.smartCreate(map[string]any{
		"title":  "Everything but Devo",
		"filter": map[string]any{"operator": "NOT", "conditions": []any{map[string]any{"inSmartPlaylist": rock}}},
		"sort":   []map[string]any{{"property": "title"}},
	})
	checkQuery(t, f, map[string]any{"inSmartPlaylist": notDevo, "hasRating": true}, nil,
		"Smells Like Nirvana", "Lost Track", "Trigger Happy", "We Will Rock You", "Bohemian Rhapsody")
	mix := f.objectID("Playlist", "Mix")
	inMix, _ := f.smartCreate(map[string]any{"title": "Mixed", "filter": map[string]any{"inPlaylist": mix}})
	checkSmartTracks(t, f, inMix, "Brave Sir Robin", "Smells Like Nirvana")

	checkSetError(t, f, "SmartPlaylist", map[string]any{"destroy": []string{rock}}, "playlistInUse")
	checkSetError(t, f, "Playlist", map[string]any{"destroy": []string{mix}}, "playlistInUse")
	f.set("SmartPlaylist", map[string]any{"destroy": []string{notDevo, inMix}})
	f.set("SmartPlaylist", map[string]any{"destroy": []string{rock}})
	f.set("Playlist", map[string]any{"destroy": []string{mix}})
	if list := f.call("SmartPlaylist/get", map[string]any{"ids": nil})["list"].([]any); len(list) != 0 {
		t.Errorf("after destroying everything, %d smart playlists remain", len(list))
	}
}

func TestSmartPlaylistSetRefusals(t *testing.T) {
	f := newFixture(t, Config{})
	create := func(props map[string]any) map[string]any {
		return map[string]any{"create": map[string]any{"x": props}}
	}
	a, _ := f.smartCreate(map[string]any{"title": "A"})
	b, _ := f.smartCreate(map[string]any{"title": "B", "filter": map[string]any{"inSmartPlaylist": a}})
	update := func(id string, props map[string]any) map[string]any {
		return map[string]any{"update": map[string]any{id: props}}
	}

	checkSetError(t, f, "SmartPlaylist", create(map[string]any{"filter": nil}), "invalidProperties", "title")
	checkSetError(t, f, "SmartPlaylist", create(map[string]any{"title": "x", "filter": map[string]any{"composer": "Bach"}}), "invalidProperties", "filter")
	checkSetError(t, f, "SmartPlaylist", create(map[string]any{"title": "x", "filter": map[string]any{"inSmartPlaylist": "Snope"}}), "invalidProperties", "filter")
	checkSetError(t, f, "SmartPlaylist", create(map[string]any{"title": "x", "filter": map[string]any{"inPlaylist": "Pnope"}}), "invalidProperties", "filter")
	checkSetError(t, f, "SmartPlaylist", create(map[string]any{"title": "x", "sort": []map[string]any{{"property": "composer"}}}), "invalidProperties", "sort")
	checkSetError(t, f, "SmartPlaylist", create(map[string]any{"title": "x", "sort": nil}), "invalidProperties", "sort")
	checkSetError(t, f, "SmartPlaylist", create(map[string]any{"title": "x", "limit": -1, "limitBytes": "lots"}), "invalidProperties", "limit", "limitBytes")
	checkSetError(t, f, "SmartPlaylist", create(map[string]any{"title": "x", "id": "Smine"}), "invalidProperties", "id")

	// B draws on A, so A may not draw on B, nor on itself.
	checkSetError(t, f, "SmartPlaylist", update(a, map[string]any{"filter": map[string]any{"inSmartPlaylist": b}}), "invalidProperties", "filter")
	checkSetError(t, f, "SmartPlaylist", update(a, map[string]any{"filter": map[string]any{"inSmartPlaylist": a}}), "invalidProperties", "filter")
	checkSetError(t, f, "SmartPlaylist", update("Snope", map[string]any{"title": "x"}), "notFound")
	checkSetError(t, f, "SmartPlaylist", map[string]any{"destroy": []string{"Snope"}}, "notFound")
}

func TestTrackQueryRandomAndCaps(t *testing.T) {
	f := newFixture(t, Config{})
	random := func(seed string) []map[string]any {
		return []map[string]any{{"property": "random", "seed": seed}}
	}
	ids := func(args map[string]any) []any { return f.call("Track/query", args)["ids"].([]any) }

	// The same seed orders the same way every time; another seed orders
	// another way.
	one := ids(map[string]any{"sort": random("one")})
	if again := ids(map[string]any{"sort": random("one")}); !reflect.DeepEqual(one, again) {
		t.Errorf("seed one ordered %v then %v", one, again)
	}
	if two := ids(map[string]any{"sort": random("two")}); reflect.DeepEqual(one, two) {
		t.Errorf("seeds one and two ordered alike: %v", one)
	}
	if len(one) != 8 {
		t.Errorf("random order has %d tracks, want 8", len(one))
	}
	checkMethodError(t, f, "Track/query", map[string]any{"sort": random("")}, "unsupportedSort")

	// A cap cuts the sorted list where the running total would pass it.
	// On "Off the Deep End" by title: Lost Track (no audio, no length),
	// Smells Like Nirvana (25 bytes, 218 s), Trigger Happy (13 bytes, 170 s).
	album := map[string]any{"filter": map[string]any{"albumTitle": "Off the Deep End"}, "sort": by("title", true), "calculateTotal": true}
	capped := func(extra map[string]any) map[string]any {
		args := map[string]any{}
		for k, v := range album {
			args[k] = v
		}
		for k, v := range extra {
			args[k] = v
		}
		return f.call("Track/query", args)
	}
	r := capped(map[string]any{"limitSeconds": 300})
	checkEqual(t, "limitSeconds", f.titlesOf(r["ids"].([]any)), []string{"Lost Track", "Smells Like Nirvana"})
	checkEqual(t, "limitSeconds total", r["total"], float64(2))
	r = capped(map[string]any{"limitBytes": 30})
	checkEqual(t, "limitBytes", f.titlesOf(r["ids"].([]any)), []string{"Lost Track", "Smells Like Nirvana"})
	r = capped(map[string]any{"limitBytes": 38, "limitSeconds": 388})
	checkEqual(t, "caps just met", f.titlesOf(r["ids"].([]any)), []string{"Lost Track", "Smells Like Nirvana", "Trigger Happy"})

	// A stored limit is a cap too, applied before Track/query's own paging.
	id, _ := f.smartCreate(map[string]any{
		"title": "Two of the deep end", "filter": album["filter"], "sort": album["sort"], "limit": 2,
	})
	checkSmartTracks(t, f, id, "Lost Track", "Smells Like Nirvana")
}

func TestSmartPlaylistReferences(t *testing.T) {
	f := newFixture(t, Config{})
	deep := map[string]any{"albumTitle": "Off the Deep End"}
	in := func(id string) map[string]any { return map[string]any{"inSmartPlaylist": id} }

	// A referenced rule's caps apply, in its own sort order, and a cap
	// cuts the list rather than skipping what doesn't fit: descending by
	// title, Trigger Happy (170 s) fits, Smells Like Nirvana (218 s)
	// doesn't, and Lost Track (no length) is past the cut.
	byBytes, _ := f.smartCreate(map[string]any{"title": "Deep end by bytes", "filter": deep, "sort": by("title", true), "limitBytes": 30})
	checkSmartTracks(t, f, byBytes, "Lost Track", "Smells Like Nirvana")
	bySeconds, _ := f.smartCreate(map[string]any{"title": "Deep end by seconds", "filter": deep, "sort": by("title", false), "limitSeconds": 300})
	checkSmartTracks(t, f, bySeconds, "Trigger Happy")

	// A rule named more than once, directly and through another, is
	// compiled once and agrees with itself.
	rated, _ := f.smartCreate(map[string]any{"title": "Rated deep end", "filter": map[string]any{"operator": "AND", "conditions": []any{in(byBytes), map[string]any{"hasRating": true}}}})
	both, _ := f.smartCreate(map[string]any{"title": "Both", "filter": map[string]any{"operator": "OR", "conditions": []any{in(byBytes), in(rated), in(bySeconds)}}})
	checkSmartTracks(t, f, both, "Lost Track", "Smells Like Nirvana", "Trigger Happy")
	checkQuery(t, f, map[string]any{"operator": "AND", "conditions": []any{in(both), in(byBytes), map[string]any{"operator": "NOT", "conditions": []any{in(bySeconds)}}}}, nil,
		"Lost Track", "Smells Like Nirvana")

	// A random sort inside a capped rule orders by its seed, as the
	// query's own would.
	random := []map[string]any{{"property": "random", "seed": "one"}}
	first := f.call("Track/query", map[string]any{"sort": random, "limit": 1})["ids"].([]any)[0]
	one, _ := f.smartCreate(map[string]any{"title": "One at random", "sort": random, "limit": 1})
	checkEqual(t, "one at random", f.call("Track/query", map[string]any{"filter": in(one)})["ids"], []any{first})
}
