package jmap

import "testing"

// checkHelperFlag creates a playlist of type typ from props, then applies
// each patch in turn, checking isHelper as /get reports it after the
// create and after each patch.  When props leaves isHelper out, the
// create must report its default.
func checkHelperFlag(t *testing.T, typ string, props map[string]any, wantCreated bool, steps ...helperStep) {
	t.Helper()
	f := newFixture(t, Config{})
	r := f.set(typ, map[string]any{"create": map[string]any{"new": props}})
	created, id := r.Created("new"), r.CreatedID("new")
	if _, given := props["isHelper"]; !given {
		checkEqual(t, typ+" created isHelper default", created["isHelper"], false)
	}
	get := func() any {
		list := f.call(typ+"/get", map[string]any{"ids": []string{id}})["list"].([]any)
		return list[0].(map[string]any)["isHelper"]
	}
	checkEqual(t, typ+" isHelper after create", get(), wantCreated)
	for _, s := range steps {
		since := f.state(typ)
		f.set(typ, map[string]any{"update": map[string]any{id: s.patch}})
		checkEqual(t, typ+" isHelper after update", get(), s.want)
		checkChangedIDs(t, f, typ, since, []string{id}, nil)
	}
}

type helperStep struct {
	patch map[string]any
	want  bool
}

func TestHelperFlag(t *testing.T) {
	checkHelperFlag(t, "Playlist", map[string]any{"title": "Hated"}, false,
		helperStep{map[string]any{"isHelper": true}, true},
		helperStep{map[string]any{"title": "Hated albums"}, true},
		helperStep{map[string]any{"isHelper": false}, false},
	)
	checkHelperFlag(t, "Playlist", map[string]any{"title": "Hated", "isHelper": true}, true)

	checkHelperFlag(t, "SmartPlaylist", map[string]any{"title": "Rock"}, false,
		helperStep{map[string]any{"isHelper": true}, true},
		helperStep{map[string]any{"filter": map[string]any{"genre": "Rock"}}, true},
		helperStep{map[string]any{"isHelper": false}, false},
	)
	checkHelperFlag(t, "SmartPlaylist", map[string]any{"title": "Rock", "isHelper": true}, true)
}

func TestHelperFlagRefusals(t *testing.T) {
	f := newFixture(t, Config{})
	for _, typ := range []string{"Playlist", "SmartPlaylist"} {
		create := map[string]any{"create": map[string]any{"x": map[string]any{"title": "x", "isHelper": "yes"}}}
		checkSetError(t, f, typ, create, "invalidProperties", "isHelper")
		create = map[string]any{"create": map[string]any{"x": map[string]any{"title": "x", "isHelper": nil}}}
		checkSetError(t, f, typ, create, "invalidProperties", "isHelper")
	}
}
