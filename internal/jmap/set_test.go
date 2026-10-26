package jmap

import (
	"testing"
)

// objectID finds an album or playlist by title.
func (f *fixture) objectID(typ, title string) string {
	f.t.Helper()
	for _, o := range f.call(typ+"/get", map[string]any{"ids": nil})["list"].([]any) {
		o := o.(map[string]any)
		if o["title"] == title {
			return o["id"].(string)
		}
	}
	f.t.Fatalf("no %s titled %q", typ, title)
	return ""
}

// tracks maps track titles to their ids, as a list to send.
func (f *fixture) tracks(titles ...string) []string {
	ids := []string{}
	for _, title := range titles {
		ids = append(ids, f.ids[title])
	}
	return ids
}

// set makes a /set call and fails the test if any object in it fails.
func (f *fixture) set(typ string, args map[string]any) map[string]any {
	f.t.Helper()
	r := f.call(typ+"/set", args)
	for _, k := range []string{"notCreated", "notUpdated", "notDestroyed"} {
		if r[k] != nil {
			f.t.Fatalf("%s/set %s: %v", typ, k, r[k])
		}
	}
	return r
}

// checkPlaylist gets a playlist and checks its title and tracks.
func checkPlaylist(t *testing.T, f *fixture, id, wantTitle string, wantTracks ...string) {
	t.Helper()
	r := f.call("Playlist/get", map[string]any{"ids": []string{id}})
	list := r["list"].([]any)
	if len(list) != 1 {
		t.Errorf("playlist %s not found", id)
		return
	}
	p := list[0].(map[string]any)
	checkEqual(t, "playlist title", p["title"], wantTitle)
	checkEqual(t, "playlist tracks", f.titlesOf(p["trackIds"].([]any)), append([]string{}, wantTracks...))
}

// checkSetError makes a /set call that should fail for exactly one object
// and checks the SetError's type and properties.
func checkSetError(t *testing.T, f *fixture, typ string, args map[string]any, wantType string, wantProps ...string) {
	t.Helper()
	r := f.call(typ+"/set", args)
	var errs []any
	for _, k := range []string{"notCreated", "notUpdated", "notDestroyed"} {
		if m, ok := r[k].(map[string]any); ok {
			for _, e := range m {
				errs = append(errs, e)
			}
		}
	}
	if len(errs) != 1 {
		t.Errorf("%s/set %v: got %d SetErrors, want 1", typ, args, len(errs))
		return
	}
	e := errs[0].(map[string]any)
	checkEqual(t, "SetError type", e["type"], wantType)
	if len(wantProps) > 0 {
		checkEqual(t, "SetError properties", idList(e["properties"]), wantProps)
	}
}

// checkDestroyed checks that /changes since a state reports exactly these
// ids destroyed.
func checkDestroyed(t *testing.T, f *fixture, typ, since string, want ...string) {
	t.Helper()
	r := f.call(typ+"/changes", map[string]any{"sinceState": since})
	checkEqual(t, typ+" destroyed", idList(r["destroyed"]), append([]string{}, want...))
}

func TestPlaylistSet(t *testing.T) {
	f := newFixture(t, Config{})
	before := f.state("Playlist")

	r := f.set("Playlist", map[string]any{"create": map[string]any{
		"trip":  map[string]any{"title": "Road Trip", "trackIds": f.tracks("Whip It", "Jocko Homo")},
		"empty": map[string]any{"title": "Someday"},
	}})
	created := r["created"].(map[string]any)
	trip := created["trip"].(map[string]any)["id"].(string)
	someday := created["empty"].(map[string]any)["id"].(string)
	checkEqual(t, "defaulted trackIds are reported", created["empty"].(map[string]any)["trackIds"], []any{})
	checkPlaylist(t, f, trip, "Road Trip", "Whip It", "Jocko Homo")
	checkPlaylist(t, f, someday, "Someday")

	f.set("Playlist", map[string]any{"update": map[string]any{
		trip: map[string]any{"trackIds": f.tracks("Jocko Homo", "Whip It", "Jocko Homo", "Lost Track")},
	}})
	checkPlaylist(t, f, trip, "Road Trip", "Jocko Homo", "Whip It", "Jocko Homo", "Lost Track")

	f.set("Playlist", map[string]any{"update": map[string]any{trip: map[string]any{"title": "Long Trip"}}})
	checkPlaylist(t, f, trip, "Long Trip", "Jocko Homo", "Whip It", "Jocko Homo", "Lost Track")

	mix := f.objectID("Playlist", "Mix")
	mid := f.state("Playlist")
	r = f.set("Playlist", map[string]any{"destroy": []string{mix}})
	checkEqual(t, "destroyed", r["destroyed"], []any{mix})
	checkEqual(t, "gone", idList(f.call("Playlist/get", map[string]any{"ids": []string{mix}})["notFound"]), []string{mix})
	checkDestroyed(t, f, "Playlist", mid, mix)
	checkQuery(t, f, map[string]any{"inPlaylist": mix}, nil)

	// The two made since "before" were never seen, and Mix was destroyed.
	checkDestroyed(t, f, "Playlist", before, mix)
	r = f.call("Playlist/changes", map[string]any{"sinceState": before})
	checkEqual(t, "created since before", sorted(idList(r["created"])), sorted([]string{trip, someday}))
}

func TestPlaylistSetCreatedInSameCall(t *testing.T) {
	f := newFixture(t, Config{})
	r := f.set("Playlist", map[string]any{
		"create": map[string]any{"new": map[string]any{"title": "Draft"}},
		"update": map[string]any{"#new": map[string]any{"trackIds": f.tracks("Whip It")}},
	})
	id := r["created"].(map[string]any)["new"].(map[string]any)["id"].(string)
	checkPlaylist(t, f, id, "Draft", "Whip It")
}

func TestPlaylistSetRefusals(t *testing.T) {
	f := newFixture(t, Config{})
	mix := f.objectID("Playlist", "Mix")
	create := func(props map[string]any) map[string]any {
		return map[string]any{"create": map[string]any{"x": props}}
	}
	update := func(props map[string]any) map[string]any {
		return map[string]any{"update": map[string]any{mix: props}}
	}

	checkSetError(t, f, "Playlist", create(map[string]any{"trackIds": []string{}}), "invalidProperties", "title")
	checkSetError(t, f, "Playlist", create(map[string]any{"title": "  "}), "invalidProperties", "title")
	checkSetError(t, f, "Playlist", create(map[string]any{"title": "x", "id": "Pmine"}), "invalidProperties", "id")
	checkSetError(t, f, "Playlist", update(map[string]any{"trackIds": []string{"Tnope"}}), "invalidProperties", "trackIds")
	checkSetError(t, f, "Playlist", update(map[string]any{"trackIds": nil, "title": 7}), "invalidProperties", "title", "trackIds")
	checkSetError(t, f, "Playlist", update(map[string]any{"trackIds/0": f.ids["Whip It"]}), "invalidProperties", "trackIds/0")
	checkSetError(t, f, "Playlist", map[string]any{"update": map[string]any{"Pnope": map[string]any{"title": "x"}}}, "notFound")
	checkSetError(t, f, "Playlist", map[string]any{"destroy": []string{"Pnope"}}, "notFound")
	checkSetError(t, f, "Playlist", map[string]any{"update": map[string]any{"#never": map[string]any{"title": "x"}}}, "notFound")

	checkPlaylist(t, f, mix, "Mix", "Brave Sir Robin", "Smells Like Nirvana", "Brave Sir Robin")
}
