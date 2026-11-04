package tags

import "testing"

func checkReleased(t *testing.T, raw map[string]any, want string) {
	t.Helper()
	if got := Released(raw); got != want {
		t.Errorf("Released(%v) = %q, want %q", raw, got, want)
	}
}

func TestReleased(t *testing.T) {
	checkReleased(t, map[string]any{"TYER": "1997"}, "1997-00-00")
	checkReleased(t, map[string]any{"TYER": "1997", "TDAT": "2105"}, "1997-05-21")
	checkReleased(t, map[string]any{"TYER": "1997", "TDAT": "junk"}, "1997-00-00")
	checkReleased(t, map[string]any{"TYE": "1997\x00", "TDA": "0102"}, "1997-02-01")
	checkReleased(t, map[string]any{"TDRC": "2004-03-09T12:00:00"}, "2004-03-09")
	checkReleased(t, map[string]any{"TDRC": "2004-03"}, "2004-03-00")
	checkReleased(t, map[string]any{"TDRC": "2004"}, "2004-00-00")
	checkReleased(t, map[string]any{"\xa9day": "1977"}, "1977-00-00")
	checkReleased(t, map[string]any{"�day": "2011-06-21T07:00:00Z"}, "2011-06-21")
	checkReleased(t, map[string]any{"year": "1997"}, "1997-00-00")
	checkReleased(t, map[string]any{"TDRC": "", "TYER": "1988"}, "1988-00-00")
	checkReleased(t, map[string]any{"TYER": "1997-13-01"}, "")
	checkReleased(t, map[string]any{"TYER": "97"}, "")
	checkReleased(t, map[string]any{"TYER": 1997}, "")
	checkReleased(t, map[string]any{"TIT2": "No date here"}, "")
	checkReleased(t, nil, "")
}

func checkValid(t *testing.T, s string, want bool) {
	t.Helper()
	if got := ValidReleased(s); got != want {
		t.Errorf("ValidReleased(%q) = %v, want %v", s, got, want)
	}
}

func TestValidReleased(t *testing.T) {
	checkValid(t, "1991-00-00", true)
	checkValid(t, "1991-02-00", true)
	checkValid(t, "1991-02-19", true)
	checkValid(t, "1991-00-19", false)
	checkValid(t, "1991-13-00", false)
	checkValid(t, "1991-02-32", false)
	checkValid(t, "0000-00-00", false)
	checkValid(t, "1991", false)
	checkValid(t, "1991-2-1", false)
	checkValid(t, "", false)
}

func checkCommon(t *testing.T, values []string, want string) {
	t.Helper()
	if got := CommonReleased(values); got != want {
		t.Errorf("CommonReleased(%q) = %q, want %q", values, got, want)
	}
}

func TestCommonReleased(t *testing.T) {
	checkCommon(t, []string{"1997-00-00", "1997-00-00", "1998-00-00"}, "1997-00-00")
	checkCommon(t, []string{"1997-05-21", "1997-00-00"}, "1997-00-00")
	checkCommon(t, []string{"1997-05-21", "1997-05-21", "1997-00-00"}, "1997-05-21")
	checkCommon(t, []string{"", "2001-00-00", ""}, "2001-00-00")
	checkCommon(t, []string{"", ""}, "")
	checkCommon(t, nil, "")
}
