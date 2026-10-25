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
	load       func(ctx context.Context, q store.Queryer, ids []string) (map[string]object, []string, error)
	query      func(ctx context.Context, s *Server, args *queryArgs) ([]string, error)
}

func (s *Server) objectTypes() []*objectType {
	return []*objectType{
		{
			name:  store.TypeTrack,
			table: "track",
			properties: []string{
				"id", "title", "artist", "albumId", "rating", "blobId", "blobType",
				"playCount", "lastPlayed", "dateAdded",
			},
			load:  loadTracks,
			query: queryTracks,
		},
		{
			name:       store.TypeAlbum,
			table:      "album",
			properties: []string{"id", "title", "artist", "discs"},
			load:       loadAlbums,
			query:      queryByTitle("album"),
		},
		{
			name:       store.TypePlaylist,
			table:      "playlist",
			properties: []string{"id", "title", "trackIds"},
			load:       loadPlaylists,
			query:      queryByTitle("playlist"),
		},
	}
}

func (s *Server) methodTable() map[string]method {
	m := map[string]method{
		"Core/echo": {CapCore, func(_ context.Context, _ *Server, args json.RawMessage) (any, error) {
			return args, nil
		}},
		"Track/set": {CapJuke, func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			return s.setTracks(ctx, args)
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
	if args.Properties != nil {
		known := map[string]bool{}
		for _, p := range t.properties {
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
		SELECT id, title, artist, album_id, rating, blob_id, content_type, play_count, last_played, date_added
		FROM track`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := map[string]object{}
	var order []string
	for rows.Next() {
		var id, title, dateAdded string
		var artist, albumID, blobID, blobType, lastPlayed sql.NullString
		var rating sql.NullFloat64
		var playCount int64
		if err := rows.Scan(&id, &title, &artist, &albumID, &rating, &blobID, &blobType, &playCount, &lastPlayed, &dateAdded); err != nil {
			return nil, nil, err
		}
		obj := object{
			"id":         id,
			"title":      title,
			"artist":     nullString(artist),
			"albumId":    nullString(albumID),
			"rating":     nil,
			"blobId":     nullString(blobID),
			"blobType":   nullString(blobType),
			"playCount":  playCount,
			"lastPlayed": nullString(lastPlayed),
			"dateAdded":  dateAdded,
		}
		if rating.Valid {
			obj["rating"] = rating.Float64
		}
		out[id] = obj
		order = append(order, id)
	}
	return out, order, rows.Err()
}

func loadAlbums(ctx context.Context, q store.Queryer, ids []string) (map[string]object, []string, error) {
	where, args := inClause("id", ids)
	rows, err := q.QueryContext(ctx, `SELECT id, title, artist FROM album`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]object{}
	discs := map[string][]map[string]any{}
	var order []string
	for rows.Next() {
		var id, title string
		var artist sql.NullString
		if err := rows.Scan(&id, &title, &artist); err != nil {
			rows.Close()
			return nil, nil, err
		}
		out[id] = object{"id": id, "title": title, "artist": nullString(artist)}
		order = append(order, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
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
	rows, err := q.QueryContext(ctx, `SELECT id, title FROM playlist`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]object{}
	var order []string
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			rows.Close()
			return nil, nil, err
		}
		out[id] = object{"id": id, "title": title, "trackIds": []string{}}
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
	IsAscending *bool  `json:"isAscending"`
	Collation   string `json:"collation"`
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
	all, err := t.query(ctx, s, &args)
	if err != nil {
		return nil, err
	}

	total := len(all)
	position := args.Position
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

	limit := s.cfg.MaxQueryLimit
	limited := true
	if args.Limit != nil && *args.Limit <= s.cfg.MaxQueryLimit {
		limit, limited = *args.Limit, false
	}
	end := min(position+limit, total)
	ids := []string{}
	if position < total {
		ids = all[position:end]
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
		order := "id"
		if len(args.Sort) > 0 {
			var parts []string
			for _, c := range args.Sort {
				if c.Property != "title" {
					return nil, errorOf("unsupportedSort", "cannot sort on %q", c.Property)
				}
				dir := "ASC"
				if !c.ascending() {
					dir = "DESC"
				}
				parts = append(parts, "juke_fold(title) "+dir)
			}
			order = strings.Join(parts, ", ") + ", id"
		}
		return queryIDs(ctx, s.store.DB, "SELECT id FROM "+table+" ORDER BY "+order)
	}
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

type setArgs struct {
	AccountID string                                `json:"accountId"`
	IfInState *string                               `json:"ifInState"`
	Create    map[string]json.RawMessage            `json:"create"`
	Update    map[string]map[string]json.RawMessage `json:"update"`
	Destroy   []string                              `json:"destroy"`
}

// The Track properties a client may set.  Everything else about a track
// comes from its file or its album.
var settableTrackProperties = map[string]bool{"rating": true, "playCount": true, "lastPlayed": true}

func (s *Server) setTracks(ctx context.Context, raw json.RawMessage) (any, error) {
	var args setArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := s.checkAccount(args.AccountID); err != nil {
		return nil, err
	}
	if len(args.Create)+len(args.Update)+len(args.Destroy) > s.cfg.MaxObjectsInSet {
		return nil, errorOf("requestTooLarge", "at most %d objects per /set", s.cfg.MaxObjectsInSet)
	}

	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	oldState, _, err := s.state(ctx, tx, store.TypeTrack)
	if err != nil {
		return nil, err
	}
	if args.IfInState != nil && *args.IfInState != oldState {
		return nil, errorOf("stateMismatch", "state is %s", oldState)
	}

	notCreated := map[string]any{}
	for cid := range args.Create {
		notCreated[cid] = &methodError{Type: "forbidden", Description: "tracks are created only by import"}
	}
	notDestroyed := map[string]any{}
	for _, id := range args.Destroy {
		notDestroyed[id] = &methodError{Type: "forbidden", Description: "tracks are destroyed only by import"}
	}

	updated := map[string]any{}
	notUpdated := map[string]any{}
	ids := make([]string, 0, len(args.Update))
	for id := range args.Update {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cols, invalid := trackPatch(args.Update[id])
		if invalid != nil {
			notUpdated[id] = invalid
			continue
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM track WHERE id = ?)`, id).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			notUpdated[id] = &methodError{Type: "notFound"}
			continue
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
		if _, err := tx.ExecContext(ctx, `UPDATE track SET `+strings.Join(set, ", ")+` WHERE id = ?`, vals...); err != nil {
			return nil, err
		}
		updated[id] = nil
	}

	newState, _, err := s.state(ctx, tx, store.TypeTrack)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	orNull := func(m map[string]any) any {
		if len(m) == 0 {
			return nil
		}
		return m
	}
	return map[string]any{
		"accountId":    args.AccountID,
		"oldState":     oldState,
		"newState":     newState,
		"created":      nil,
		"updated":      orNull(updated),
		"destroyed":    nil,
		"notCreated":   orNull(notCreated),
		"notUpdated":   orNull(notUpdated),
		"notDestroyed": orNull(notDestroyed),
	}, nil
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
			Description: "only rating, playCount and lastPlayed may be set, to valid values",
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
