package tags

import (
	"regexp"
	"strconv"
	"strings"
)

// releasedKeys are the raw keys that hold a release date, by tag
// format: ID3v2.4's timestamp, ID3v2.3's and v2.2's year, MP4's day
// atom, and ID3v1's year.  The day atom's key begins with a lone 0xA9
// byte, which the tag library passes through as is, so at import it is
// "\xa9day"; stored as JSON it became U+FFFD, and both are looked
// for. -- claude, 2026-11-03
var releasedKeys = []string{"TDRC", "TYER", "TYE", "\xa9day", "�day", "©day", "year"}

// dayKeys are the frames that give a day and month, as DDMM, beside a
// year-only frame.
var dayKeys = map[string]string{"TYER": "TDAT", "TYE": "TDA"}

var (
	datePrefix   = regexp.MustCompile(`^(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?`)
	dayMonth     = regexp.MustCompile(`^(\d{2})(\d{2})$`)
	releasedForm = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
)

// Released returns the release date a file's tags give, in the form
// Album.released has it: "YYYY-MM-DD", with "00" for a month or day the
// tags leave out, or "" when they give no year.
func Released(raw map[string]any) string {
	for _, key := range releasedKeys {
		s, _ := raw[key].(string)
		m := datePrefix.FindStringSubmatch(strings.Trim(s, " \t\x00"))
		if m == nil {
			continue
		}
		year, month, day := m[1], m[2], m[3]
		if month == "" {
			d, _ := raw[dayKeys[key]].(string)
			if dm := dayMonth.FindStringSubmatch(strings.Trim(d, " \t\x00")); dm != nil {
				day, month = dm[1], dm[2]
			}
		}
		if r := partialDate(year, month, day); r != "" {
			return r
		}
	}
	return ""
}

// partialDate puts a date together, "00" standing for a part not given,
// or returns "" for one that isn't a date.
func partialDate(year, month, day string) string {
	if month == "" {
		month = "00"
	}
	if day == "" {
		day = "00"
	}
	s := year + "-" + month + "-" + day
	if !ValidReleased(s) {
		return ""
	}
	return s
}

// ValidReleased says whether s is a release date as Album.released has
// it: "YYYY-MM-DD", the month and day each "00" when not known, and a
// known day needing a known month.  The ranges are checked, not the
// calendar: February 31st passes.
func ValidReleased(s string) bool {
	m := releasedForm.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	return year >= 1 && month <= 12 && day <= 31 && (day == 0 || month != 0)
}

// CommonReleased is the date most of an album's tracks give, "" ones
// ignored, or "" when none do.  The smallest wins a tie, so a year the
// tracks share beats one track's full date.
func CommonReleased(values []string) string {
	count := map[string]int{}
	best := ""
	for _, v := range values {
		if v == "" {
			continue
		}
		count[v]++
		if count[v] > count[best] || (count[v] == count[best] && v < best) {
			best = v
		}
	}
	return best
}
