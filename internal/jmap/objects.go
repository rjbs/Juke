package jmap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"semiotic.systems/juke/internal/store"
)

type object = map[string]any

// objectType describes one JMAP type: its properties, and how to load
// objects of it.  load returns the objects with the given ids, or every
// object when ids is nil, along with the ids found, in id order.
type objectType struct {
	name       string
	table      string
	properties []string
	// reviewProperties are the properties among them that only a request
	// using the review capability sees.
	reviewProperties []string
	load             func(ctx context.Context, q store.Queryer, ids []string) (map[string]object, []string, error)
	query            func(ctx context.Context, s *Server, args *queryArgs) ([]string, error)

	// page, if set, answers a query for one page in SQL: the ids from
	// position, at most limit of them, and the total.  It may decline by
	// returning false, and the query then runs through query and is
	// paged by the handler.
	page func(ctx context.Context, s *Server, args *queryArgs, position, limit int) ([]string, int, bool, error)
}

func (s *Server) objectTypes() []*objectType {
	return []*objectType{
		{
			name:  store.TypeTrack,
			table: "track",
			properties: []string{
				"id", "title", "artist", "genre", "albumId", "rating", "blobId", "blobType",
				"size", "duration", "playCount", "lastPlayed", "dateAdded", "artworkBlobId", "artworkType", "numberSource",
			},
			reviewProperties: []string{"numberSource"},
			load:             loadTracks,
			query:            queryTracks,
			page:             queryTracksPage,
		},
		{
			name:             store.TypeAlbum,
			table:            "album",
			properties:       []string{"id", "title", "artist", "released", "discs", "artworkBlobId", "artworkType", "issues", "artworkCandidates"},
			reviewProperties: []string{"issues", "artworkCandidates"},
			load:             loadAlbums,
			query:            queryAlbums,
		},
		{
			name:       store.TypePlaylist,
			table:      "playlist",
			properties: []string{"id", "title", "trackIds", "isHelper"},
			load:       loadPlaylists,
			query:      queryByTitle("playlist"),
		},
		{
			name:       store.TypeSmartPlaylist,
			table:      "smart_playlist",
			properties: []string{"id", "title", "filter", "sort", "limit", "limitBytes", "limitSeconds", "isHelper"},
			load:       loadSmartPlaylists,
			query:      queryByTitle("smart_playlist"),
		},
	}
}

func (s *Server) methodTable() map[string]method {
	m := map[string]method{
		"Track/set": {CapJuke, func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			return s.set(ctx, trackSetter, args)
		}},
		"Playlist/set": {CapJuke, func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			return s.set(ctx, playlistSetter, args)
		}},
		"Album/set": {CapJuke, func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			return s.set(ctx, albumSetter, args)
		}},
		"SmartPlaylist/set": {CapJuke, func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			return s.set(ctx, s.smartPlaylistSetter(), args)
		}},
	}
	for _, t := range s.objectTypes() {
		m[t.name+"/get"] = method{CapJuke, func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			return s.get(ctx, t, args)
		}}
		m[t.name+"/changes"] = method{CapJuke, func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			return s.changes(ctx, t, args)
		}}
		m[t.name+"/query"] = method{CapJuke, func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			return s.query(ctx, t, args)
		}}
		m[t.name+"/queryChanges"] = method{CapJuke, func(context.Context, *Server, json.RawMessage) (any, error) {
			return nil, errorOf("cannotCalculateChanges", "query changes are not supported; rerun the query")
		}}
	}
	return m
}

func (s *Server) checkAccount(id string) error {
	if id != s.cfg.AccountID {
		return errorOf("accountNotFound", "no account %q", id)
	}
	return nil
}

func (s *Server) state(ctx context.Context, q store.Queryer, typ string) (string, int64, error) {
	epoch, err := s.store.Epoch(ctx)
	if err != nil {
		return "", 0, err
	}
	modseq, err := store.TypeModseq(ctx, q, typ)
	if err != nil {
		return "", 0, err
	}
	return store.State(epoch, modseq), modseq, nil
}

// ---- /get -----------------------------------------------------------------

type getArgs struct {
	AccountID  string    `json:"accountId"`
	IDs        *[]string `json:"ids"`
	Properties *[]string `json:"properties"`
}

func (s *Server) get(ctx context.Context, t *objectType, raw json.RawMessage) (any, error) {
	var args getArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := s.checkAccount(args.AccountID); err != nil {
		return nil, err
	}

	props := t.properties
	if !reviewing(ctx) {
		props = without(props, t.reviewProperties)
	}
	if args.Properties != nil {
		known := map[string]bool{}
		for _, p := range props {
			known[p] = true
		}
		props = []string{"id"}
		for _, p := range *args.Properties {
			if !known[p] {
				return nil, errInvalidArguments("%s has no property %q", t.name, p)
			}
			if p != "id" {
				props = append(props, p)
			}
		}
	}

	var ids []string
	if args.IDs != nil {
		ids = dedupe(*args.IDs)
		if len(ids) > s.cfg.MaxObjectsInGet {
			return nil, errorOf("requestTooLarge", "at most %d ids per /get", s.cfg.MaxObjectsInGet)
		}
	} else {
		var n int
		if err := s.store.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+t.table).Scan(&n); err != nil {
			return nil, err
		}
		if n > s.cfg.MaxObjectsInGet {
			return nil, errorOf("requestTooLarge", "%d objects; at most %d per /get", n, s.cfg.MaxObjectsInGet)
		}
	}

	// State is read before the objects.  If a write lands between the two,
	// the client gets newer objects with an older state, and its next
	// /changes repeats a few updates; the reverse would lose them.
	// -- claude, 2026-10-24
	state, _, err := s.state(ctx, s.store.DB, t.name)
	if err != nil {
		return nil, err
	}
	objects, found, err := t.load(ctx, s.store.DB, ids)
	if err != nil {
		return nil, err
	}

	order := found
	if ids != nil {
		order = ids
	}
	list := []object{}
	notFound := []string{}
	for _, id := range order {
		obj, ok := objects[id]
		if !ok {
			notFound = append(notFound, id)
			continue
		}
		out := object{}
		for _, p := range props {
			out[p] = obj[p]
		}
		list = append(list, out)
	}
	return map[string]any{
		"accountId": args.AccountID,
		"state":     state,
		"list":      list,
		"notFound":  notFound,
	}, nil
}

func dedupe(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// inClause returns " WHERE col IN (?, ...)" and its arguments, or "" for
// a nil id list, which means every row.
func inClause(col string, ids []string) (string, []any) {
	if ids == nil {
		return "", nil
	}
	if len(ids) == 0 {
		return " WHERE 0", nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return " WHERE " + col + " IN (?" + strings.Repeat(", ?", len(ids)-1) + ")", args
}

func nullString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func loadTracks(ctx context.Context, q store.Queryer, ids []string) (map[string]object, []string, error) {
	where, args := inClause("id", ids)
	rows, err := q.QueryContext(ctx, `
		SELECT id, title, artist, genre, album_id, rating, blob_id, content_type, size, duration, play_count, last_played, date_added,
		       artwork_id, (SELECT mime FROM artwork WHERE id = track.artwork_id), number_source
		FROM track`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := map[string]object{}
	var order []string
	for rows.Next() {
		var id, title, dateAdded string
		var artist, genre, albumID, blobID, blobType, lastPlayed, artworkID, artworkType, numberSource sql.NullString
		var rating sql.NullFloat64
		var size, duration sql.NullInt64
		var playCount int64
		if err := rows.Scan(&id, &title, &artist, &genre, &albumID, &rating, &blobID, &blobType, &size, &duration, &playCount, &lastPlayed, &dateAdded, &artworkID, &artworkType, &numberSource); err != nil {
			return nil, nil, err
		}
		obj := object{
			"id":         id,
			"title":      title,
			"artist":     nullString(artist),
			"genre":      nullString(genre),
			"albumId":    nullString(albumID),
			"rating":     nil,
			"blobId":     nullString(blobID),
			"blobType":   nullString(blobType),
			"size":       nil,
			"duration":   nil,
			"playCount":  playCount,
			"lastPlayed": nullString(lastPlayed),
			"dateAdded":  dateAdded,

			"artworkBlobId": nullString(artworkID),
			"artworkType":   nullString(artworkType),
			"numberSource":  nullString(numberSource),
		}
		if rating.Valid {
			obj["rating"] = rating.Float64
		}
		if size.Valid {
			obj["size"] = size.Int64
		}
		if duration.Valid {
			obj["duration"] = duration.Int64
		}
		out[id] = obj
		order = append(order, id)
	}
	return out, order, rows.Err()
}

func loadAlbums(ctx context.Context, q store.Queryer, ids []string) (map[string]object, []string, error) {
	where, args := inClause("id", ids)
	rows, err := q.QueryContext(ctx, `
		SELECT id, title, artist, released, artwork_id, (SELECT mime FROM artwork WHERE id = album.artwork_id), `+issueColumns(reviewing(ctx))+`
		FROM album`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]object{}
	discs := map[string][]map[string]any{}
	var order []string
	for rows.Next() {
		var id, title string
		var artist, released, artworkID, artworkType sql.NullString
		flags := make([]bool, len(albumIssues))
		dest := []any{&id, &title, &artist, &released, &artworkID, &artworkType}
		for i := range flags {
			dest = append(dest, &flags[i])
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return nil, nil, err
		}
		issues := []string{}
		for i, flag := range flags {
			if flag {
				issues = append(issues, albumIssues[i].name)
			}
		}
		out[id] = object{
			"id": id, "title": title, "artist": nullString(artist), "released": nullString(released),
			"artworkBlobId": nullString(artworkID), "artworkType": nullString(artworkType),
			"issues": issues, "artworkCandidates": []candidate{},
		}
		order = append(order, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if reviewing(ctx) {
		candidates, err := loadCandidates(ctx, q, ids)
		if err != nil {
			return nil, nil, err
		}
		for id, c := range candidates {
			if obj, ok := out[id]; ok {
				obj["artworkCandidates"] = c
			}
		}
	}

	where, args = inClause("album_id", ids)
	rows, err = q.QueryContext(ctx, `SELECT album_id, disc_index, title FROM disc`+where+` ORDER BY album_id, disc_index`, args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var albumID string
		var index int
		var title sql.NullString
		if err := rows.Scan(&albumID, &index, &title); err != nil {
			rows.Close()
			return nil, nil, err
		}
		for len(discs[albumID]) <= index {
			discs[albumID] = append(discs[albumID], map[string]any{"trackIds": []string{}, "title": nil})
		}
		discs[albumID][index]["title"] = nullString(title)
	}
	rows.Close()

	if ids == nil {
		where = " WHERE album_id IS NOT NULL"
	}
	rows, err = q.QueryContext(ctx, `
		SELECT album_id, disc_index, id FROM track`+where+`
		ORDER BY album_id, disc_index, position`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var albumID, trackID string
		var index int
		if err := rows.Scan(&albumID, &index, &trackID); err != nil {
			return nil, nil, err
		}
		for len(discs[albumID]) <= index {
			discs[albumID] = append(discs[albumID], map[string]any{"trackIds": []string{}, "title": nil})
		}
		d := discs[albumID][index]
		d["trackIds"] = append(d["trackIds"].([]string), trackID)
	}
	for id, obj := range out {
		d := discs[id]
		if d == nil {
			d = []map[string]any{}
		}
		obj["discs"] = d
	}
	return out, order, rows.Err()
}

func loadPlaylists(ctx context.Context, q store.Queryer, ids []string) (map[string]object, []string, error) {
	where, args := inClause("id", ids)
	rows, err := q.QueryContext(ctx, `SELECT id, title, is_helper FROM playlist`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]object{}
	var order []string
	for rows.Next() {
		var id, title string
		var isHelper bool
		if err := rows.Scan(&id, &title, &isHelper); err != nil {
			rows.Close()
			return nil, nil, err
		}
		out[id] = object{"id": id, "title": title, "trackIds": []string{}, "isHelper": isHelper}
		order = append(order, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	where, args = inClause("playlist_id", ids)
	rows, err = q.QueryContext(ctx, `SELECT playlist_id, track_id FROM playlist_track`+where+` ORDER BY playlist_id, position`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, tid string
		if err := rows.Scan(&pid, &tid); err != nil {
			return nil, nil, err
		}
		if obj, ok := out[pid]; ok {
			obj["trackIds"] = append(obj["trackIds"].([]string), tid)
		}
	}
	return out, order, rows.Err()
}

// ---- /changes -------------------------------------------------------------

type changesArgs struct {
	AccountID  string `json:"accountId"`
	SinceState string `json:"sinceState"`
	MaxChanges *int   `json:"maxChanges"`
}

func (s *Server) changes(ctx context.Context, t *objectType, raw json.RawMessage) (any, error) {
	var args changesArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := s.checkAccount(args.AccountID); err != nil {
		return nil, err
	}
	if args.MaxChanges != nil && *args.MaxChanges <= 0 {
		return nil, errInvalidArguments("maxChanges must be positive")
	}
	epoch, err := s.store.Epoch(ctx)
	if err != nil {
		return nil, err
	}
	since, err := store.ParseState(epoch, args.SinceState)
	if err != nil {
		return nil, errorOf("cannotCalculateChanges", "state %q is not from this server", args.SinceState)
	}
	_, current, err := s.state(ctx, s.store.DB, t.name)
	if err != nil {
		return nil, err
	}
	if since > current {
		return nil, errorOf("cannotCalculateChanges", "state %q is in the future", args.SinceState)
	}

	limit := -1
	if args.MaxChanges != nil {
		limit = *args.MaxChanges + 1
	}
	rows, err := s.store.DB.QueryContext(ctx, `
		SELECT id, created_modseq, modseq, 0 FROM `+t.table+` WHERE modseq > ?
		UNION ALL
		SELECT id, created_modseq, modseq, 1 FROM tombstone WHERE type = ? AND modseq > ?
		ORDER BY 3
		LIMIT ?`, since, t.name, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	created, updated, destroyed := []string{}, []string{}, []string{}
	var last int64
	n := 0
	more := false
	for rows.Next() {
		var id string
		var createdAt, modseq int64
		var gone bool
		if err := rows.Scan(&id, &createdAt, &modseq, &gone); err != nil {
			return nil, err
		}
		if args.MaxChanges != nil && n == *args.MaxChanges {
			more = true
			break
		}
		n++
		last = modseq
		switch {
		case gone && createdAt > since:
			// Created and destroyed since the client last looked: it never
			// saw this object, so there is nothing to tell it.
		case gone:
			destroyed = append(destroyed, id)
		case createdAt > since:
			created = append(created, id)
		default:
			updated = append(updated, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	newModseq := current
	if more {
		newModseq = last
	}
	return map[string]any{
		"accountId":      args.AccountID,
		"oldState":       args.SinceState,
		"newState":       store.State(epoch, newModseq),
		"hasMoreChanges": more,
		"created":        created,
		"updated":        updated,
		"destroyed":      destroyed,
	}, nil
}

// ---- /query ---------------------------------------------------------------

type comparator struct {
	Property    string `json:"property"`
	IsAscending *bool  `json:"isAscending,omitempty"`
	Collation   string `json:"collation,omitempty"`
	// Seed is for the Track sort "random", which orders by a hash of the
	// id and the seed; see doc/types.md.
	Seed string `json:"seed,omitempty"`
}

func (c comparator) ascending() bool { return c.IsAscending == nil || *c.IsAscending }

type queryArgs struct {
	AccountID      string          `json:"accountId"`
	Filter         json.RawMessage `json:"filter"`
	Sort           []comparator    `json:"sort"`
	Position       int             `json:"position"`
	Anchor         *string         `json:"anchor"`
	AnchorOffset   int             `json:"anchorOffset"`
	Limit          *int            `json:"limit"`
	CalculateTotal bool            `json:"calculateTotal"`

	// Track/query's caps on the sorted list: at most this much audio by
	// size, or by playing time.  See doc/types.md.
	LimitBytes   *int64 `json:"limitBytes"`
	LimitSeconds *int64 `json:"limitSeconds"`
}

func (s *Server) query(ctx context.Context, t *objectType, raw json.RawMessage) (any, error) {
	var args queryArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := s.checkAccount(args.AccountID); err != nil {
		return nil, err
	}
	for _, c := range args.Sort {
		if c.Collation != "" && c.Collation != "i;unicode-casemap" {
			return nil, errorOf("unsupportedSort", "unsupported collation %q", c.Collation)
		}
	}
	if args.Limit != nil && *args.Limit < 0 {
		return nil, errInvalidArguments("limit must not be negative")
	}

	state, _, err := s.state(ctx, s.store.DB, t.name)
	if err != nil {
		return nil, err
	}
	limit := s.cfg.MaxQueryLimit
	limited := true
	if args.Limit != nil && *args.Limit <= s.cfg.MaxQueryLimit {
		limit, limited = *args.Limit, false
	}

	var ids []string
	var total, position int
	paged := false
	if t.page != nil && args.Anchor == nil && args.Position >= 0 {
		ids, total, paged, err = t.page(ctx, s, &args, args.Position, limit)
		if err != nil {
			return nil, err
		}
		position = args.Position
	}
	if !paged {
		all, err := t.query(ctx, s, &args)
		if err != nil {
			return nil, err
		}
		total = len(all)
		position = args.Position
		if args.Anchor != nil {
			idx := -1
			for i, id := range all {
				if id == *args.Anchor {
					idx = i
					break
				}
			}
			if idx < 0 {
				return nil, errorOf("anchorNotFound", "%q is not in the results", *args.Anchor)
			}
			position = max(idx+args.AnchorOffset, 0)
		} else if position < 0 {
			position = max(total+position, 0)
		}
		end := min(position+limit, total)
		ids = []string{}
		if position < total {
			ids = all[position:end]
		}
	}

	resp := map[string]any{
		"accountId":           args.AccountID,
		"queryState":          state,
		"canCalculateChanges": false,
		"position":            min(position, total),
		"ids":                 ids,
	}
	if args.CalculateTotal {
		resp["total"] = total
	}
	if limited && total-position > limit {
		resp["limit"] = limit
	}
	return resp, nil
}

// queryByTitle queries a type that supports no filter, sorted only by
// title, or by id when no sort is given.
func queryByTitle(table string) func(ctx context.Context, s *Server, args *queryArgs) ([]string, error) {
	return func(ctx context.Context, s *Server, args *queryArgs) ([]string, error) {
		if len(args.Filter) > 0 && string(args.Filter) != "null" {
			return nil, errorOf("unsupportedFilter", "this type cannot be filtered")
		}
		order, err := titleOrder(args)
		if err != nil {
			return nil, err
		}
		return queryIDs(ctx, s.store.DB, "SELECT id FROM "+table+" ORDER BY "+order)
	}
}

// queryAlbums queries albums, filtered by their issues if at all, and
// sorted by title or by artist.
func queryAlbums(ctx context.Context, s *Server, args *queryArgs) ([]string, error) {
	where, err := albumWhere(ctx, args.Filter)
	if err != nil {
		return nil, err
	}
	order, err := albumOrder(args)
	if err != nil {
		return nil, err
	}
	return queryIDs(ctx, s.store.DB, "SELECT id FROM album"+where+" ORDER BY "+order)
}

// albumOrder is the ORDER BY for Album/query: by title, by artist with
// the unnamed albums last either way, as the web page lists them, or by
// release date with the undated last either way; by id when no sort is
// given.
func albumOrder(args *queryArgs) (string, error) {
	if len(args.Sort) == 0 {
		return "id", nil
	}
	var parts []string
	for _, c := range args.Sort {
		dir := " ASC"
		if !c.ascending() {
			dir = " DESC"
		}
		switch c.Property {
		case "title":
			parts = append(parts, "juke_fold(title)"+dir)
		case "artist":
			parts = append(parts, "(artist IS NULL)", "juke_fold(artist)"+dir)
		case "released":
			parts = append(parts, "(released IS NULL)", "released"+dir)
		default:
			return "", errorOf("unsupportedSort", "cannot sort on %q", c.Property)
		}
	}
	return strings.Join(parts, ", ") + ", id", nil
}

// titleOrder is the ORDER BY for a type sorted only by title, or by id
// when no sort is given.
func titleOrder(args *queryArgs) (string, error) {
	if len(args.Sort) == 0 {
		return "id", nil
	}
	var parts []string
	for _, c := range args.Sort {
		if c.Property != "title" {
			return "", errorOf("unsupportedSort", "cannot sort on %q", c.Property)
		}
		dir := "ASC"
		if !c.ascending() {
			dir = "DESC"
		}
		parts = append(parts, "juke_fold(title) "+dir)
	}
	return strings.Join(parts, ", ") + ", id", nil
}

func queryIDs(ctx context.Context, q store.Queryer, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ---- Track/set ------------------------------------------------------------

// The Track properties a client may set.  Everything else about a track
// comes from its file or its album.  Genre is the one piece of tag data
// among them: the "No Genre" repair queue needs somewhere to put the
// answer, and a later metadata flush (#5) carries it to the file.
// Artwork is the review page choosing a track's own picture, or clearing
// one; set by hand, it is left alone by later artwork runs.
var settableTrackProperties = map[string]bool{"rating": true, "playCount": true, "lastPlayed": true, "genre": true, "artworkBlobId": true}

var trackSetter = &setter{
	typ: store.TypeTrack,
	create: func(context.Context, *sql.Tx, map[string]json.RawMessage) (object, error) {
		return nil, &methodError{Type: "forbidden", Description: "tracks are created only by import"}
	},
	update: func(ctx context.Context, tx *sql.Tx, id string, patch map[string]json.RawMessage) (object, error) {
		if _, ok := patch["artworkBlobId"]; ok && !reviewing(ctx) {
			return nil, invalidProperties("artworkBlobId is set only under the review capability", []string{"artworkBlobId"})
		}
		cols, invalid := trackPatch(patch)
		if invalid != nil {
			return nil, invalid
		}
		if err := mustExist(ctx, tx, "track", id); err != nil {
			return nil, err
		}
		// Art set by hand, cleared included, is marked as the user's, so the
		// artwork run leaves it alone and the review page counts the track
		// as decided.
		for i, c := range cols {
			if c.column != "artwork_id" {
				continue
			}
			if c.value != nil {
				if found, err := artworkExists(ctx, tx, c.value.(string)); err != nil {
					return nil, err
				} else if !found {
					return nil, invalidProperties("artworkBlobId must name a stored image, or be null", []string{"artworkBlobId"})
				}
			}
			cols = append(cols[:i:i], append([]columnValue{{"artwork_source", "user"}}, cols[i:]...)...)
			break
		}
		modseq, err := store.NextModseq(ctx, tx)
		if err != nil {
			return nil, err
		}
		set := []string{"modseq = ?"}
		vals := []any{modseq}
		for _, c := range cols {
			set = append(set, c.column+" = ?")
			vals = append(vals, c.value)
		}
		vals = append(vals, id)
		_, err = tx.ExecContext(ctx, `UPDATE track SET `+strings.Join(set, ", ")+` WHERE id = ?`, vals...)
		return nil, err
	},
	destroy: func(context.Context, *sql.Tx, string) error {
		return &methodError{Type: "forbidden", Description: "tracks are destroyed only by import"}
	},
}

type columnValue struct {
	column string
	value  any
}

// trackPatch validates a Track update and returns the columns it sets,
// or a SetError naming every property that was wrong.
func trackPatch(patch map[string]json.RawMessage) ([]columnValue, *methodError) {
	var cols []columnValue
	var bad []string
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, prop := range keys {
		val := patch[prop]
		if !settableTrackProperties[prop] {
			bad = append(bad, prop)
			continue
		}
		cv, ok := trackValue(prop, val)
		if !ok {
			bad = append(bad, prop)
			continue
		}
		cols = append(cols, cv)
	}
	if len(bad) > 0 {
		return nil, &methodError{
			Type:        "invalidProperties",
			Description: "only rating, playCount, lastPlayed, genre and artworkBlobId may be set, to valid values",
			Properties:  bad,
		}
	}
	return cols, nil
}

func trackValue(prop string, raw json.RawMessage) (columnValue, bool) {
	isNull := string(raw) == "null"
	switch prop {
	case "rating":
		if isNull {
			return columnValue{"rating", nil}, true
		}
		var r float64
		if json.Unmarshal(raw, &r) != nil || r < 1 || r > 5 || r*2 != float64(int(r*2)) {
			return columnValue{}, false
		}
		return columnValue{"rating", r}, true
	case "playCount":
		var n float64
		if isNull || json.Unmarshal(raw, &n) != nil || n < 0 || n != float64(int64(n)) || n > 1<<53-1 {
			return columnValue{}, false
		}
		return columnValue{"play_count", int64(n)}, true
	case "genre":
		if isNull {
			return columnValue{"genre", nil}, true
		}
		var str string
		if json.Unmarshal(raw, &str) != nil {
			return columnValue{}, false
		}
		if str = strings.TrimSpace(str); str == "" {
			return columnValue{"genre", nil}, true
		}
		return columnValue{"genre", str}, true
	case "artworkBlobId":
		if isNull {
			return columnValue{"artwork_id", nil}, true
		}
		var str string
		if json.Unmarshal(raw, &str) != nil || str == "" {
			return columnValue{}, false
		}
		return columnValue{"artwork_id", str}, true
	case "lastPlayed":
		if isNull {
			return columnValue{"last_played", nil}, true
		}
		var str string
		if json.Unmarshal(raw, &str) != nil {
			return columnValue{}, false
		}
		date, err := parseUTCDate(str)
		if err != nil {
			return columnValue{}, false
		}
		return columnValue{"last_played", date}, true
	}
	return columnValue{}, false
}

var errBadDate = errors.New("not a UTCDate")
