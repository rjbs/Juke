package tags

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// trackNumberKeys are the raw keys that hold a track number, by tag
// format: ID3v2.3 and v2.4, ID3v2.2, MP4, and ID3v1.  The ID3 ones are
// strings, "n" or "n/total"; the others are numbers.
var trackNumberKeys = []string{"TRCK", "TRK", "trkn", "track"}

// TrackNumber returns the track number a file's tags give, or 0 when
// they give none.  The map may be the tag library's own, where numbers
// are ints, or one decoded from original_tags, where they are float64s.
func TrackNumber(raw map[string]any) int {
	for _, key := range trackNumberKeys {
		switch v := raw[key].(type) {
		case string:
			n, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(v, "/", 2)[0]))
			if n > 0 {
				return n
			}
		case int:
			if v > 0 {
				return v
			}
		case float64:
			if v > 0 {
				return int(v)
			}
		}
	}
	return 0
}

// numberPrefix is the "NN " or "D-NN " that iTunes put before a title in
// a filename.  Three digits at most, so "1999 Prince.mp3" keeps its title.
var numberPrefix = regexp.MustCompile(`^(?:(\d{1,2})-)?(\d{1,3})[-. ]+`)

// NumbersFromName returns the disc and track numbers a filename starts
// with, zero for either that is absent.
func NumbersFromName(name string) (disc, track int) {
	m := numberPrefix.FindStringSubmatch(name)
	if m == nil {
		return 0, 0
	}
	disc, _ = strconv.Atoi(m[1])
	track, _ = strconv.Atoi(m[2])
	return disc, track
}

// TitleFromName is a filename less its extension and number prefix, or
// the whole filename if that leaves nothing.
func TitleFromName(name string) string {
	stem := strings.TrimSpace(numberPrefix.ReplaceAllString(strings.TrimSuffix(name, path.Ext(name)), ""))
	if stem == "" {
		return name
	}
	return stem
}
