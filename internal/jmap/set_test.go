package jmap

import (
	"testing"

	"github.com/rjbs/gizmo/jmaptest"
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
func (f *fixture) set(typ string, args map[string]any) *jmaptest.Set {
	f.t.Helper()
	return f.Request(jmaptest.Do(typ+"/set", args)).SingleSentence(typ + "/set").AsSet().AssertNoErrors()
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
	trip, someday := r.CreatedID("trip"), r.CreatedID("empty")
	checkEqual(t, "defaulted trackIds are reported", r.Created("empty")["trackIds"], []any{})
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
	checkEqual(t, "destroyed", r.DestroyedIDs(), []string{mix})
	checkEqual(t, "gone", idList(f.call("Playlist/get", map[string]any{"ids": []string{mix}})["notFound"]), []string{mix})
	checkDestroyed(t, f, "Playlist", mid, mix)
	checkQuery(t, f, map[string]any{"inPlaylist": mix}, nil)

	// The two made since "before" were never seen, and Mix was destroyed.
	checkDestroyed(t, f, "Playlist", before, mix)
	changes := f.call("Playlist/changes", map[string]any{"sinceState": before})
	checkEqual(t, "created since before", sorted(idList(changes["created"])), sorted([]string{trip, someday}))
}

func TestPlaylistSetCreatedInSameCall(t *testing.T) {
	f := newFixture(t, Config{})
	r := f.set("Playlist", map[string]any{
		"create": map[string]any{"new": map[string]any{"title": "Draft"}},
		"update": map[string]any{"#new": map[string]any{"trackIds": f.tracks("Whip It")}},
	})
	id := r.CreatedID("new")
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

// checkAlbum gets an album and checks its title, artist, and the titles of
// the tracks on each disc.
func checkAlbum(t *testing.T, f *fixture, id, wantTitle string, wantArtist any, wantDiscs ...[]string) {
	t.Helper()
	list := f.call("Album/get", map[string]any{"ids": []string{id}})["list"].([]any)
	if len(list) != 1 {
		t.Errorf("album %s not found", id)
		return
	}
	a := list[0].(map[string]any)
	checkEqual(t, "album title", a["title"], wantTitle)
	checkEqual(t, "album artist", a["artist"], wantArtist)
	var discs [][]string
	for _, d := range a["discs"].([]any) {
		discs = append(discs, f.titlesOf(d.(map[string]any)["trackIds"].([]any)))
	}
	checkEqual(t, "album discs", discs, wantDiscs)
}

// checkAlbumOf checks a track's albumId: an id, or nil for no album.
func checkAlbumOf(t *testing.T, f *fixture, title string, want any) {
	t.Helper()
	r := f.call("Track/get", map[string]any{"ids": []string{f.ids[title]}, "properties": []string{"albumId"}})
	checkEqual(t, title+" albumId", r["list"].([]any)[0].(map[string]any)["albumId"], want)
}

// checkChangedIDs calls /changes and checks the ids it reports updated
// and destroyed, in any order.
func checkChangedIDs(t *testing.T, f *fixture, typ, since string, wantUpdated, wantDestroyed []string) {
	t.Helper()
	r := f.call(typ+"/changes", map[string]any{"sinceState": since})
	checkEqual(t, typ+" created", idList(r["created"]), []string{})
	checkEqual(t, typ+" updated", sorted(idList(r["updated"])), sorted(append([]string{}, wantUpdated...)))
	checkEqual(t, typ+" destroyed", sorted(idList(r["destroyed"])), sorted(append([]string{}, wantDestroyed...)))
}

func disc(trackIDs []string) map[string]any {
	return map[string]any{"trackIds": trackIDs}
}

// Two albums that should be one, as an import of per-disc folders makes:
// one update takes the other's tracks as a second disc, and a destroy
// removes the emptied album.  The two are separate calls in one request,
// because a /set destroys before it updates, and an album with tracks
// can't be destroyed.
func TestAlbumSetMergesAlbums(t *testing.T) {
	f := newFixture(t, Config{})
	weird := f.objectID("Album", "Off the Deep End")
	devo := f.objectID("Album", "We Are Not Devo")
	albums, tracks := f.state("Album"), f.state("Track")

	merge := map[string]any{"update": map[string]any{weird: map[string]any{"discs": []any{
		disc(f.tracks("Smells Like Nirvana", "Lost Track", "Trigger Happy")),
		map[string]any{"trackIds": f.tracks("Whip It", "Jocko Homo"), "title": "Bonus Disc"},
	}}}}

	r := f.Request(jmaptest.Do("Album/set", merge), jmaptest.Do("Album/set", map[string]any{"destroy": []string{devo}}))
	for _, s := range r.AssertSentences(2) {
		s.AssertNamed("Album/set").AsSet().AssertNoErrors()
	}
	checkAlbum(t, f, weird, "Off the Deep End", "Weird Al Yankovic",
		[]string{"Smells Like Nirvana", "Lost Track", "Trigger Happy"}, []string{"Whip It", "Jocko Homo"})
	checkAlbumOf(t, f, "Whip It", weird)
	checkChangedIDs(t, f, "Album", albums, []string{weird}, []string{devo})
	checkChangedIDs(t, f, "Track", tracks, f.tracks("Whip It", "Jocko Homo"), nil)
	checkQuery(t, f, map[string]any{"albumTitle": "Off the Deep End"}, by("albumOrder", true),
		"Smells Like Nirvana", "Lost Track", "Trigger Happy", "Whip It", "Jocko Homo")
}

func TestAlbumSetReorders(t *testing.T) {
	f := newFixture(t, Config{})
	queen := f.objectID("Album", "Live Killers")
	albums, tracks := f.state("Album"), f.state("Track")

	f.set("Album", map[string]any{"update": map[string]any{queen: map[string]any{
		"discs": []any{disc(f.tracks("Bohemian Rhapsody", "We Will Rock You"))},
		"title": "Live Killers (Remastered)",
	}}})
	checkAlbum(t, f, queen, "Live Killers (Remastered)", "Queen", []string{"Bohemian Rhapsody", "We Will Rock You"})
	checkChangedIDs(t, f, "Album", albums, []string{queen}, nil)
	checkChangedIDs(t, f, "Track", tracks, nil, nil)
}

// A track added to an album leaves the one it was on, which changes too;
// a track dropped from an album is left on none.
func TestAlbumSetMovesTracks(t *testing.T) {
	f := newFixture(t, Config{})
	queen := f.objectID("Album", "Live Killers")
	devo := f.objectID("Album", "We Are Not Devo")
	albums, tracks := f.state("Album"), f.state("Track")

	f.set("Album", map[string]any{"update": map[string]any{queen: map[string]any{
		"discs": []any{disc(f.tracks("We Will Rock You", "Whip It", "Brave Sir Robin"))},
	}}})
	checkAlbum(t, f, queen, "Live Killers", "Queen", []string{"We Will Rock You", "Whip It", "Brave Sir Robin"})
	checkAlbum(t, f, devo, "We Are Not Devo", nil, []string{"Jocko Homo"})
	checkAlbumOf(t, f, "Whip It", queen)
	checkAlbumOf(t, f, "Brave Sir Robin", queen)
	checkAlbumOf(t, f, "Bohemian Rhapsody", nil)
	checkChangedIDs(t, f, "Album", albums, []string{queen, devo}, nil)
	checkChangedIDs(t, f, "Track", tracks, f.tracks("Whip It", "Brave Sir Robin", "Bohemian Rhapsody"), nil)
}

func TestAlbumSetCreates(t *testing.T) {
	f := newFixture(t, Config{})
	r := f.set("Album", map[string]any{"create": map[string]any{
		"full":  map[string]any{"title": "Holy Grail", "artist": "Monty Python", "discs": []any{disc(f.tracks("Brave Sir Robin"))}},
		"empty": map[string]any{"title": "Someday"},
	}})
	grail := r.CreatedID("full")
	checkAlbum(t, f, grail, "Holy Grail", "Monty Python", []string{"Brave Sir Robin"})
	checkAlbumOf(t, f, "Brave Sir Robin", grail)
	checkEqual(t, "defaults are reported", r.Created("empty"), map[string]any{
		"id":       r.CreatedID("empty"),
		"artist":   nil,
		"released": nil,
		"discs":    []any{map[string]any{"trackIds": []any{}, "title": nil}},
	})
}

// A release date is set and cleared as a partial date string, refused
// in any other shape, and sorts albums with the undated last either way.
func TestAlbumSetReleased(t *testing.T) {
	f := newFixture(t, Config{})
	queen := f.objectID("Album", "Live Killers")
	r := f.set("Album", map[string]any{"create": map[string]any{
		"n": map[string]any{"title": "Night at the Opera", "artist": "Queen", "released": "1975-11-00"},
	}})
	opera := r.CreatedID("n")
	checkEqual(t, "created with a date", f.album(opera, "released")["released"], "1975-11-00")

	f.set("Album", map[string]any{"update": map[string]any{queen: map[string]any{"released": "1979-06-22"}}})
	checkEqual(t, "updated with a date", f.album(queen, "released")["released"], "1979-06-22")

	byRelease := func(ascending bool) []string {
		return idList(f.call("Album/query", map[string]any{"sort": by("released", ascending)})["ids"])
	}
	asc, desc := byRelease(true), byRelease(false)
	checkEqual(t, "oldest first", asc[:2], []string{opera, queen})
	checkEqual(t, "newest first", desc[:2], []string{queen, opera})
	checkEqual(t, "the undated last either way", sorted(asc[2:]), sorted(desc[2:]))

	f.set("Album", map[string]any{"update": map[string]any{queen: map[string]any{"released": nil}}})
	checkEqual(t, "cleared", f.album(queen, "released")["released"], nil)

	update := func(props map[string]any) map[string]any {
		return map[string]any{"update": map[string]any{queen: props}}
	}
	checkSetError(t, f, "Album", update(map[string]any{"released": "1979"}), "invalidProperties", "released")
	checkSetError(t, f, "Album", update(map[string]any{"released": "1979-13-00"}), "invalidProperties", "released")
	checkSetError(t, f, "Album", update(map[string]any{"released": "1979-00-22"}), "invalidProperties", "released")
	checkSetError(t, f, "Album", update(map[string]any{"released": 1979}), "invalidProperties", "released")
}

func TestAlbumSetRefusals(t *testing.T) {
	f := newFixture(t, Config{})
	weird := f.objectID("Album", "Off the Deep End")
	update := func(props map[string]any) map[string]any {
		return map[string]any{"update": map[string]any{weird: props}}
	}
	discs := func(d ...any) map[string]any { return update(map[string]any{"discs": d}) }

	checkSetError(t, f, "Album", map[string]any{"destroy": []string{weird}}, "albumNotEmpty")
	checkSetError(t, f, "Album", discs(), "invalidProperties", "discs")
	checkSetError(t, f, "Album", discs(disc(f.tracks("Whip It", "Whip It"))), "invalidProperties", "discs")
	checkSetError(t, f, "Album", discs(disc(f.tracks("Whip It")), disc(f.tracks("Whip It"))), "invalidProperties", "discs")
	checkSetError(t, f, "Album", discs(disc([]string{"Tnope"})), "invalidProperties", "discs")
	checkSetError(t, f, "Album", discs(map[string]any{"title": "No Tracks"}), "invalidProperties", "discs")
	checkSetError(t, f, "Album", discs(map[string]any{"trackIds": []string{}, "number": 1}), "invalidProperties", "discs")
	checkSetError(t, f, "Album", update(map[string]any{"artist": 5, "title": ""}), "invalidProperties", "artist", "title")
	checkSetError(t, f, "Album", update(map[string]any{"discs/0/trackIds": []string{}}), "invalidProperties", "discs/0/trackIds")
	checkSetError(t, f, "Album", map[string]any{"create": map[string]any{"x": map[string]any{"artist": "Nobody"}}}, "invalidProperties", "title")

	checkAlbum(t, f, weird, "Off the Deep End", "Weird Al Yankovic", []string{"Smells Like Nirvana", "Lost Track", "Trigger Happy"})
}

// checkTrackGenre sets a track's genre and checks what Track/get then says.
func checkTrackGenre(t *testing.T, f *fixture, title string, set any, want any) {
	t.Helper()
	id := f.ids[title]
	f.set("Track", map[string]any{"update": map[string]any{id: map[string]any{"genre": set}}})
	got := f.call("Track/get", map[string]any{"ids": []string{id}})["list"].([]any)[0].(map[string]any)
	checkEqual(t, "genre of "+title, got["genre"], want)
}

func TestTrackSetGenre(t *testing.T) {
	f := newFixture(t, Config{})
	checkTrackGenre(t, f, "Trigger Happy", "Polka", "Polka")
	checkTrackGenre(t, f, "Trigger Happy", "  Oompah  ", "Oompah")
	checkTrackGenre(t, f, "Trigger Happy", "", nil)
	checkTrackGenre(t, f, "Whip It", nil, nil)
	update := map[string]any{"update": map[string]any{f.ids["Whip It"]: map[string]any{"genre": 7}}}
	checkSetError(t, f, "Track", update, "invalidProperties", "genre")
}
