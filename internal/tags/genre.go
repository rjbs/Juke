// Package tags derives Track properties from a file's tags, in the form
// the import keeps them: the tag library's raw map, or that map decoded
// from the original_tags column.  Deriving from the stored map rather
// than from the file is what lets a property be added to an existing
// database without a rescan; see doc/import.md.
package tags

import (
	"regexp"
	"strconv"
	"strings"
)

// genreKeys are the raw keys that hold a genre, by tag format: ID3v2.3
// and v2.4, ID3v2.2, MP4, and ID3v1, whose reader resolves the name
// itself.
var genreKeys = []string{"TCON", "TCO", "©gen", "genre"}

// ID3v2 lets a genre be a number in parentheses, from the ID3v1 list or
// Winamp's extension of it, optionally followed by a refinement in words:
// "(17)" is Rock and "(4)Eurodisco" is a kind of Disco.  iTunes wrote
// the refinement as the name itself, "(36)Game", so when there is one it
// is the whole answer. -- claude, 2026-11-01
var numberedGenre = regexp.MustCompile(`^\((\d+)\)(.*)$`)

// Genre returns the genre a file's tags give, as a person would read it,
// or "" when they give none.
func Genre(raw map[string]any) string {
	for _, key := range genreKeys {
		s, _ := raw[key].(string)
		if s = strings.Trim(s, " \t\x00"); s != "" {
			return expandGenre(s)
		}
	}
	return ""
}

func expandGenre(s string) string {
	if strings.HasPrefix(s, "((") {
		return s[1:]
	}
	m := numberedGenre.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	if rest := strings.TrimSpace(m[2]); rest != "" {
		return rest
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n >= len(genreNames) {
		return s
	}
	return genreNames[n]
}

// genreNames is the ID3v1 genre list as Winamp extended it, indexed by
// the number ID3v2 tags use.  Copied from github.com/dhowden/tag, which
// doesn't export it.
var genreNames = [...]string{
	"Blues", "Classic Rock", "Country", "Dance", "Disco", "Funk", "Grunge",
	"Hip-Hop", "Jazz", "Metal", "New Age", "Oldies", "Other", "Pop", "R&B",
	"Rap", "Reggae", "Rock", "Techno", "Industrial", "Alternative", "Ska",
	"Death Metal", "Pranks", "Soundtrack", "Euro-Techno", "Ambient",
	"Trip-Hop", "Vocal", "Jazz+Funk", "Fusion", "Trance", "Classical",
	"Instrumental", "Acid", "House", "Game", "Sound Clip", "Gospel",
	"Noise", "AlternRock", "Bass", "Soul", "Punk", "Space", "Meditative",
	"Instrumental Pop", "Instrumental Rock", "Ethnic", "Gothic",
	"Darkwave", "Techno-Industrial", "Electronic", "Pop-Folk",
	"Eurodance", "Dream", "Southern Rock", "Comedy", "Cult", "Gangsta",
	"Top 40", "Christian Rap", "Pop/Funk", "Jungle", "Native American",
	"Cabaret", "New Wave", "Psychedelic", "Rave", "Showtunes", "Trailer",
	"Lo-Fi", "Tribal", "Acid Punk", "Acid Jazz", "Polka", "Retro",
	"Musical", "Rock & Roll", "Hard Rock", "Folk", "Folk-Rock",
	"National Folk", "Swing", "Fast Fusion", "Bebob", "Latin", "Revival",
	"Celtic", "Bluegrass", "Avantgarde", "Gothic Rock", "Progressive Rock",
	"Psychedelic Rock", "Symphonic Rock", "Slow Rock", "Big Band",
	"Chorus", "Easy Listening", "Acoustic", "Humour", "Speech", "Chanson",
	"Opera", "Chamber Music", "Sonata", "Symphony", "Booty Bass", "Primus",
	"Porn Groove", "Satire", "Slow Jam", "Club", "Tango", "Samba",
	"Folklore", "Ballad", "Power Ballad", "Rhythmic Soul", "Freestyle",
	"Duet", "Punk Rock", "Drum Solo", "A capella", "Euro-House", "Dance Hall",
	"Goa", "Drum & Bass", "Club-House", "Hardcore", "Terror", "Indie",
	"Britpop", "Negerpunk", "Polsk Punk", "Beat", "Christian Gangsta Rap",
	"Heavy Metal", "Black Metal", "Crossover", "Contemporary Christian",
	"Christian Rock", "Merengue", "Salsa", "Thrash Metal", "Anime", "JPop",
	"Synthpop",
	"Christmas", "Art Rock", "Baroque", "Bhangra", "Big Beat", "Breakbeat",
	"Chillout", "Downtempo", "Dub", "EBM", "Eclectic", "Electro",
	"Electroclash", "Emo", "Experimental", "Garage", "Global", "IDM",
	"Illbient", "Industro-Goth", "Jam Band", "Krautrock", "Leftfield", "Lounge",
	"Math Rock", "New Romantic", "Nu-Breakz", "Post-Punk", "Post-Rock", "Psytrance",
	"Shoegaze", "Space Rock", "Trop Rock", "World Music", "Neoclassical", "Audiobook",
	"Audio Theatre", "Neue Deutsche Welle", "Podcast", "Indie Rock", "G-Funk", "Dubstep",
	"Garage Rock", "Psybient",
}
