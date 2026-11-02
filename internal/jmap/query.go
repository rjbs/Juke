package jmap

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"semiotic.systems/juke/internal/store"
)

// maxSmartDepth bounds how deeply smart playlists may reference one
// another through inSmartPlaylist.  Stored rules can't form a cycle, but
// a rule sent straight to Track/query could name a long chain.
const maxSmartDepth = 16

// queryTracks runs a Track/query as one SQL statement.  The filter and
// sort compile to a WHERE and ORDER BY over track t and album a; each
// smart playlist the filter names becomes a common table expression
// ahead of them, so the whole thing runs inside SQLite.  The query's own
// caps are cut here as the sorted rows stream.  The filter conditions
// are listed in doc/types.md.
func queryTracks(ctx context.Context, s *Server, args *queryArgs) ([]string, error) {
	c := &compiler{s: s, ctx: ctx, q: s.store.DB}
	where, params, err := c.filter(args.Filter)
	if err != nil {
		return nil, err
	}
	order, orderParams, err := compileTrackSort(args.Sort)
	if err != nil {
		return nil, err
	}
	with, withParams := c.with()
	rows, err := s.store.DB.QueryContext(ctx, with+`
		SELECT t.id, coalesce(t.size, 0), coalesce(t.duration, 0)
		FROM track t LEFT JOIN album a ON a.id = t.album_id
		WHERE `+where+`
		ORDER BY `+order, append(append(withParams, params...), orderParams...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	var bytes, seconds int64
	for rows.Next() {
		var id string
		var size, duration int64
		if err := rows.Scan(&id, &size, &duration); err != nil {
			return nil, err
		}
		// A cap cuts the list where the running total would pass it; a
		// track with no audio or no known length counts as nothing.
		bytes += size
		seconds += duration
		if args.LimitBytes != nil && bytes > *args.LimitBytes ||
			args.LimitSeconds != nil && seconds > *args.LimitSeconds {
			break
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// A compiler turns a filter into SQL over track t and album a.  It
// records the playlists the filter names, which SmartPlaylist/set keeps;
// with validate set it checks that they exist instead of compiling
// them, and the SQL it returns is not meant to run.
type compiler struct {
	s        *Server
	ctx      context.Context
	q        store.Queryer
	validate bool
	refs     []store.Ref

	// The smart playlists the filter names, each compiled once however
	// often it is named; see reference.  A capped one is a common table
	// expression, listed here in an order that defines each one after
	// those it names.
	ctes    []cte
	byID    map[string]membership
	pending map[string]bool // being compiled, so naming it again is a loop
	depth   int
}

type cte struct {
	name   string
	sql    string
	params []any
}

// A membership is the SQL that is true of a track in a smart playlist.
type membership struct {
	sql    string
	params []any
}

// with returns the WITH clause defining the filter's capped smart
// playlists, and its parameters, or nothing when it names none.  Each
// is materialized, so a rule named twice is evaluated once.
func (c *compiler) with() (string, []any) {
	if len(c.ctes) == 0 {
		return "", nil
	}
	var defs []string
	var params []any
	for _, e := range c.ctes {
		defs = append(defs, e.name+" AS MATERIALIZED ("+e.sql+")")
		params = append(params, e.params...)
	}
	return "WITH " + strings.Join(defs, ",\n") + "\n", params
}

// reference compiles membership in the smart playlist with the given
// id, caps included.  A rule with no cap is its filter, inlined where
// it is named: cheaper than a set of ids, even named several times,
// since a predicate costs nothing to set up.  A capped one must be a
// set, the first so many of its matches in its sort order, so it
// becomes a common table expression that keeps those within every cap
// by running counts over that order, and membership is an IN of it.
// -- claude, 2026-11-02
func (c *compiler) reference(id string) (string, []any, error) {
	if m, ok := c.byID[id]; ok {
		return m.sql, m.params, nil
	}
	if c.pending[id] {
		return "", nil, errInvalidArguments("smart playlists reference one another in a loop")
	}
	if c.depth >= maxSmartDepth {
		return "", nil, errInvalidArguments("smart playlists nest too deeply")
	}
	_, sq, err := store.LoadSmartPlaylist(c.ctx, c.q, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, errInvalidArguments("no smart playlist %q", id)
	}
	if err != nil {
		return "", nil, err
	}

	if c.byID == nil {
		c.byID = map[string]membership{}
		c.pending = map[string]bool{}
	}
	c.pending[id] = true
	c.depth++
	var filter json.RawMessage
	if sq.Filter != nil {
		filter = json.RawMessage(*sq.Filter)
	}
	where, params, err := c.filter(filter)
	c.depth--
	delete(c.pending, id)
	if err != nil {
		return "", nil, err
	}

	if sq.Limit == nil && sq.LimitBytes == nil && sq.LimitSeconds == nil {
		m := membership{sql: "(" + where + ")", params: params}
		c.byID[id] = m
		return m.sql, m.params, nil
	}

	var sort []comparator
	if err := json.Unmarshal([]byte(sq.Sort), &sort); err != nil {
		return "", nil, err
	}
	order, orderParams, err := compileTrackSort(sort)
	if err != nil {
		return "", nil, err
	}
	params = append(params, orderParams...)
	var caps []string
	{
		for _, cap := range []struct {
			column string
			limit  *int64
		}{{"n", sq.Limit}, {"bytes", sq.LimitBytes}, {"seconds", sq.LimitSeconds}} {
			if cap.limit != nil {
				caps = append(caps, cap.column+" <= ?")
				params = append(params, *cap.limit)
			}
		}
		// The running totals include the current row, so a row is kept
		// only if it fits; the totals never shrink, so once one passes
		// its cap every later row fails too, as the cut in queryTracks
		// does it. -- claude, 2026-11-02
	}
	body := `SELECT id FROM (
		SELECT t.id AS id, row_number() OVER w AS n,
		  sum(coalesce(t.size, 0)) OVER w AS bytes,
		  sum(coalesce(t.duration, 0)) OVER w AS seconds
		FROM track t LEFT JOIN album a ON a.id = t.album_id
		WHERE ` + where + `
		WINDOW w AS (ORDER BY ` + order + ` ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
	) WHERE ` + strings.Join(caps, " AND ")

	name := fmt.Sprintf("s%d", len(c.ctes)+1)
	c.ctes = append(c.ctes, cte{name: name, sql: body, params: params})
	m := membership{sql: "t.id IN (SELECT id FROM " + name + ")"}
	c.byID[id] = m
	return m.sql, nil, nil
}

// filter compiles a FilterOperator or FilterCondition.
func (c *compiler) filter(raw json.RawMessage) (string, []any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "1", nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", nil, errInvalidArguments("a filter must be an object")
	}

	if op, ok := fields["operator"]; ok {
		var operator string
		if err := json.Unmarshal(op, &operator); err != nil {
			return "", nil, errInvalidArguments("operator must be a string")
		}
		var conditions []json.RawMessage
		if err := json.Unmarshal(fields["conditions"], &conditions); err != nil || len(fields) != 2 {
			return "", nil, errInvalidArguments("a FilterOperator has exactly operator and conditions")
		}
		var parts []string
		var params []any
		for _, cond := range conditions {
			sql, p, err := c.filter(cond)
			if err != nil {
				return "", nil, err
			}
			parts = append(parts, "("+sql+")")
			params = append(params, p...)
		}
		switch operator {
		case "AND":
			if len(parts) == 0 {
				return "1", nil, nil
			}
			return strings.Join(parts, " AND "), params, nil
		case "OR":
			if len(parts) == 0 {
				return "0", nil, nil
			}
			return strings.Join(parts, " OR "), params, nil
		case "NOT":
			if len(parts) == 0 {
				return "1", nil, nil
			}
			return "NOT (" + strings.Join(parts, " OR ") + ")", params, nil
		default:
			return "", nil, errorOf("unsupportedFilter", "unknown operator %q", operator)
		}
	}

	parts := []string{"1"}
	var params []any
	for _, name := range sortedKeys(fields) {
		sql, p, err := c.condition(name, fields[name])
		if err != nil {
			return "", nil, err
		}
		// A condition is true or false of every track (RFC 8620 section
		// 5.5), so NOT and OR must see false, not SQL's null, when the
		// column it reads is null: "artist is not Devo" includes a track
		// with no artist. -- claude, 2026-11-01
		parts = append(parts, "coalesce(("+sql+"), 0)")
		params = append(params, p...)
	}
	return strings.Join(parts, " AND "), params, nil
}

func (c *compiler) condition(name string, raw json.RawMessage) (string, []any, error) {
	bad := func(want string) (string, []any, error) {
		return "", nil, errInvalidArguments("filter %s must be %s", name, want)
	}
	str := func() (string, bool) {
		var v string
		return v, json.Unmarshal(raw, &v) == nil
	}
	num := func() (float64, bool) {
		var v float64
		return v, json.Unmarshal(raw, &v) == nil && !bytes.Equal(raw, []byte("null"))
	}
	boolean := func() (bool, bool) {
		var v bool
		return v, json.Unmarshal(raw, &v) == nil && !bytes.Equal(raw, []byte("null"))
	}

	textColumns := map[string]string{"title": "t.title", "artist": "t.artist", "albumTitle": "a.title", "genre": "t.genre"}
	if col, ok := textColumns[name]; ok {
		v, ok := str()
		if !ok {
			return bad("a string")
		}
		return "juke_fold(" + col + ") = juke_fold(?)", []any{v}, nil
	}
	if col, ok := textColumns[strings.TrimSuffix(name, "Contains")]; ok && strings.HasSuffix(name, "Contains") {
		v, ok := str()
		if !ok {
			return bad("a string")
		}
		return "instr(juke_fold(" + col + "), juke_fold(?)) > 0", []any{v}, nil
	}

	switch name {
	case "ratingAtLeast", "ratingAtMost":
		v, ok := num()
		if !ok {
			return bad("a number")
		}
		op := map[string]string{"ratingAtLeast": ">=", "ratingAtMost": "<="}[name]
		return "t.rating " + op + " ?", []any{v}, nil
	case "playCountAtLeast", "playCountAtMost", "durationAtLeast", "durationAtMost":
		v, ok := num()
		if !ok || v < 0 || v != float64(int64(v)) {
			return bad("a non-negative integer")
		}
		col := map[string]string{"playCountAtLeast": "t.play_count", "playCountAtMost": "t.play_count", "durationAtLeast": "t.duration", "durationAtMost": "t.duration"}[name]
		op := ">="
		if strings.HasSuffix(name, "AtMost") {
			op = "<="
		}
		return col + " " + op + " ?", []any{int64(v)}, nil
	case "hasRating":
		v, ok := boolean()
		if !ok {
			return bad("a boolean")
		}
		return "(t.rating IS NOT NULL) = ?", []any{v}, nil
	case "hasArtist", "hasAlbum", "hasGenre":
		v, ok := boolean()
		if !ok {
			return bad("a boolean")
		}
		col := map[string]string{"hasArtist": "t.artist", "hasAlbum": "t.album_id", "hasGenre": "t.genre"}[name]
		return "(" + col + " IS NOT NULL) = ?", []any{v}, nil
	case "hasAudio":
		v, ok := boolean()
		if !ok {
			return bad("a boolean")
		}
		return "(t.blob_id IS NOT NULL) = ?", []any{v}, nil
	case "lastPlayedBefore", "lastPlayedAfter":
		v, ok := str()
		if !ok {
			return bad("a UTCDate")
		}
		date, err := parseUTCDate(v)
		if err != nil {
			return bad("a UTCDate")
		}
		op := map[string]string{"lastPlayedBefore": "<", "lastPlayedAfter": ">="}[name]
		return "t.last_played " + op + " ?", []any{date}, nil
	case "lastPlayedWithinDays":
		v, ok := num()
		if !ok || v < 0 || v != float64(int64(v)) {
			return bad("a non-negative integer")
		}
		since := c.s.cfg.Now().UTC().Add(-time.Duration(v) * 24 * time.Hour).Format(utcDateLayout)
		return "t.last_played >= ?", []any{since}, nil
	case "inPlaylist":
		v, ok := str()
		if !ok {
			return bad("a playlist id")
		}
		c.refs = append(c.refs, store.Ref{Type: store.TypePlaylist, ID: v})
		if c.validate {
			if err := mustExist(c.ctx, c.q, "playlist", v); err != nil {
				return bad("the id of a playlist")
			}
			return "1", nil, nil
		}
		return "EXISTS (SELECT 1 FROM playlist_track pt WHERE pt.playlist_id = ? AND pt.track_id = t.id)", []any{v}, nil
	case "inSmartPlaylist":
		v, ok := str()
		if !ok {
			return bad("a smart playlist id")
		}
		c.refs = append(c.refs, store.Ref{Type: store.TypeSmartPlaylist, ID: v})
		if c.validate {
			if err := mustExist(c.ctx, c.q, "smart_playlist", v); err != nil {
				return bad("the id of a smart playlist")
			}
			return "1", nil, nil
		}
		return c.reference(v)
	}
	return "", nil, errorOf("unsupportedFilter", "no filter condition %q", name)
}

// The Track sort properties, each as the SQL it sorts by.
var trackSortColumns = map[string][]string{
	"title":      {"juke_fold(t.title)"},
	"artist":     {"juke_fold(t.artist)"},
	"albumTitle": {"juke_fold(a.title)"},
	"genre":      {"juke_fold(t.genre)"},
	"rating":     {"t.rating"},
	"playCount":  {"t.play_count"},
	"duration":   {"t.duration"},
	// Never played counts as played before anything, so ascending is
	// "least recently played" and starts with the never-played; see
	// doc/types.md. -- claude, 2026-11-01
	"lastPlayed": {"coalesce(t.last_played, '')"},
	"albumOrder": {"juke_fold(a.title)", "t.album_id", "t.disc_index", "t.position"},
}

// compileTrackSort returns an ORDER BY clause and its parameters.  Nulls
// sort last in either direction, and ties always break by id, so a
// sorted, limited list is stable.  A random comparator orders by a hash
// of the id and its seed, so it is as stable as any other.
func compileTrackSort(sort []comparator) (string, []any, error) {
	var parts []string
	var params []any
	for _, c := range sort {
		dir := " ASC"
		if !c.ascending() {
			dir = " DESC"
		}
		if c.Property == "random" {
			if c.Seed == "" {
				return "", nil, errorOf("unsupportedSort", "a random sort needs a seed")
			}
			parts = append(parts, "juke_hash(t.id, ?)"+dir)
			params = append(params, c.Seed)
			continue
		}
		cols, ok := trackSortColumns[c.Property]
		if !ok {
			return "", nil, errorOf("unsupportedSort", "cannot sort on %q", c.Property)
		}
		for _, col := range cols {
			parts = append(parts, "("+col+" IS NULL)", col+dir)
		}
	}
	parts = append(parts, "t.id")
	return strings.Join(parts, ", "), params, nil
}

const utcDateLayout = "2006-01-02T15:04:05Z"

var utcDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$`)

// parseUTCDate checks a UTCDate (RFC 8620 section 1.4) and returns it
// in the form the database stores, without fractional seconds.
func parseUTCDate(s string) (string, error) {
	if !utcDatePattern.MatchString(s) {
		return "", errBadDate
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return "", errBadDate
	}
	return t.UTC().Format(utcDateLayout), nil
}
