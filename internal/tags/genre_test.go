package tags

import "testing"

func checkGenre(t *testing.T, raw map[string]any, want string) {
	t.Helper()
	if got := Genre(raw); got != want {
		t.Errorf("Genre(%v) = %q, want %q", raw, got, want)
	}
}

func TestGenre(t *testing.T) {
	checkGenre(t, map[string]any{"TCON": "Rock"}, "Rock")
	checkGenre(t, map[string]any{"TCON": "(17)"}, "Rock")
	checkGenre(t, map[string]any{"TCON": "(36)Game"}, "Game")
	checkGenre(t, map[string]any{"TCON": "(4)Eurodisco"}, "Eurodisco")
	checkGenre(t, map[string]any{"TCON": "(100)"}, "Humour")
	checkGenre(t, map[string]any{"TCON": "(999)"}, "(999)")
	checkGenre(t, map[string]any{"TCON": "((Not a number)"}, "(Not a number)")
	checkGenre(t, map[string]any{"TCON": "  Folk\x00"}, "Folk")
	checkGenre(t, map[string]any{"TCO": "Drum and Bass"}, "Drum and Bass")
	checkGenre(t, map[string]any{"©gen": "Soundtrack"}, "Soundtrack")
	checkGenre(t, map[string]any{"genre": "Blues"}, "Blues")
	checkGenre(t, map[string]any{"TCON": "", "TCO": "Latin"}, "Latin")
	checkGenre(t, map[string]any{"TIT2": "No genre here"}, "")
	checkGenre(t, map[string]any{"TCON": 17}, "")
	checkGenre(t, nil, "")
}
