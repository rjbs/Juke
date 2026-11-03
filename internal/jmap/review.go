package jmap

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"semiotic.systems/juke/internal/store"
)

// The review page's view of an album: what might be wrong with it, and
// the pictures it could have.  Both are worked out from columns the
// artwork run and the import keep, so they cost a few subqueries per
// album and no new modseq when they change.  See doc/types.md.

// An albumIssue is one kind of trouble, with the SQL that says whether
// a row of album has it.  Art issues are moot once the album's art was
// chosen by hand; a tie, or folder art the tracks disagree with, is also
// settled once every track carrying some other picture has had its own
// art decided by hand, since deciding them is accepting the album's.
type albumIssue struct {
	name string
	sql  string
}

const artByRule = `album.artwork_source IS NOT 'user'`

const undecidedTrack = `EXISTS (SELECT 1 FROM track t WHERE t.album_id = album.id
		AND t.picture_id IS NOT NULL AND t.picture_id IS NOT album.artwork_id AND t.artwork_source IS NOT 'user')`

var albumIssues = []albumIssue{
	{"noArt", artByRule + ` AND album.artwork_id IS NULL
		AND EXISTS (SELECT 1 FROM track t WHERE t.album_id = album.id AND t.path IS NOT NULL)`},
	{"artOverrides", artByRule + ` AND EXISTS (SELECT 1 FROM track t WHERE t.album_id = album.id
		AND t.artwork_id IS NOT NULL AND t.artwork_source IS NOT 'user')`},
	{"artTie", artByRule + ` AND album.art_tie AND ` + undecidedTrack},
	{"folderArt", artByRule + ` AND album.folder_artwork_id IS NOT NULL AND ` + undecidedTrack},
	{"unnumbered", `EXISTS (SELECT 1 FROM track t WHERE t.album_id = album.id AND t.number_source = 'none')`},
	{"gaps", `EXISTS (SELECT 1 FROM track t WHERE t.album_id = album.id AND t.path IS NULL)`},
}

func issueByName(name string) *albumIssue {
	for i := range albumIssues {
		if albumIssues[i].name == name {
			return &albumIssues[i]
		}
	}
	return nil
}

// issueColumns is the SELECT list fragment giving each issue as a column,
// in albumIssues order, or a false for each when nobody is asking.
func issueColumns(wanted bool) string {
	var cols []string
	for _, i := range albumIssues {
		if wanted {
			cols = append(cols, "("+i.sql+")")
		} else {
			cols = append(cols, "0")
		}
	}
	return strings.Join(cols, ", ")
}

// without is props less the given ones.
func without(props, drop []string) []string {
	var out []string
	for _, p := range props {
		if !slices.Contains(drop, p) {
			out = append(out, p)
		}
	}
	return out
}

// anyIssue is the condition that an album has some issue.
func anyIssue() string {
	var conds []string
	for _, i := range albumIssues {
		conds = append(conds, "("+i.sql+")")
	}
	return strings.Join(conds, " OR ")
}

// A candidate is one image an album could have: the one in a file
// beside its tracks, or one that some of its tracks' files carry.
type candidate struct {
	BlobID   string   `json:"blobId"`
	Type     string   `json:"type"`
	TrackIDs []string `json:"trackIds"`
	Folder   bool     `json:"folder"`
}

// loadCandidates reads every album's candidates, or those of the albums
// with the given ids: the folder image first, then each picture by the
// first track carrying it, in play order.
func loadCandidates(ctx context.Context, q store.Queryer, ids []string) (map[string][]candidate, error) {
	out := map[string][]candidate{}
	index := map[string]map[string]int{} // album id -> blob id -> position in out
	add := func(albumID string, c candidate) *candidate {
		if index[albumID] == nil {
			index[albumID] = map[string]int{}
		}
		if i, ok := index[albumID][c.BlobID]; ok {
			return &out[albumID][i]
		}
		index[albumID][c.BlobID] = len(out[albumID])
		out[albumID] = append(out[albumID], c)
		return &out[albumID][len(out[albumID])-1]
	}

	where, args := inClause("id", ids)
	rows, err := q.QueryContext(ctx, `
		SELECT album.id, folder_artwork_id, (SELECT mime FROM artwork WHERE id = album.folder_artwork_id)
		FROM album`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var albumID string
		var folder, mime sql.NullString
		if err := rows.Scan(&albumID, &folder, &mime); err != nil {
			rows.Close()
			return nil, err
		}
		if folder.Valid {
			add(albumID, candidate{BlobID: folder.String, Type: mime.String, TrackIDs: []string{}, Folder: true})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	where, args = inClause("album_id", ids)
	if ids == nil {
		where = " WHERE album_id IS NOT NULL"
	}
	rows, err = q.QueryContext(ctx, `
		SELECT album_id, picture_id, (SELECT mime FROM artwork WHERE id = track.picture_id), id
		FROM track`+where+` AND picture_id IS NOT NULL ORDER BY album_id, disc_index, position`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var albumID, pictureID, mime, trackID string
		if err := rows.Scan(&albumID, &pictureID, &mime, &trackID); err != nil {
			return nil, err
		}
		c := add(albumID, candidate{BlobID: pictureID, Type: mime, TrackIDs: []string{}})
		c.TrackIDs = append(c.TrackIDs, trackID)
	}
	return out, rows.Err()
}

// albumFilter is what Album/query can filter on: albums with any issue,
// or with one particular issue.  Both together is the one issue.
type albumFilter struct {
	HasIssues *bool   `json:"hasIssues"`
	Issue     *string `json:"issue"`
}

// albumWhere turns an Album/query filter into a WHERE clause, "" for none.
func albumWhere(ctx context.Context, raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if !reviewing(ctx) {
		return "", errorOf("unsupportedFilter", "albums are filtered only under the review capability")
	}
	var f albumFilter
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return "", errorOf("unsupportedFilter", "albums are filtered only by hasIssues or issue")
	}
	if f.Issue != nil {
		i := issueByName(*f.Issue)
		if i == nil {
			return "", errorOf("unsupportedFilter", "no such issue %q", *f.Issue)
		}
		return " WHERE " + i.sql, nil
	}
	if f.HasIssues != nil {
		if *f.HasIssues {
			return " WHERE " + anyIssue(), nil
		}
		return " WHERE NOT (" + anyIssue() + ")", nil
	}
	return "", nil
}

// artworkExists says whether an image with this id is stored.
func artworkExists(ctx context.Context, q store.Queryer, id string) (bool, error) {
	var found bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM artwork WHERE id = ?)`, id).Scan(&found)
	return found, err
}
