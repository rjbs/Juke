package jmap

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// queryTracks runs a Track/query, compiling its filter and sort to SQL.
// The filter conditions are listed in doc/types.md.
func queryTracks(ctx context.Context, s *Server, args *queryArgs) ([]string, error) {
	where := "1"
	var params []any
	if len(args.Filter) > 0 && string(args.Filter) != "null" {
		var err error
		where, params, err = s.compileFilter(args.Filter)
		if err != nil {
			return nil, err
		}
	}
	order, err := compileTrackSort(args.Sort)
	if err != nil {
		return nil, err
	}
	return queryIDs(ctx, s.store.DB, `
		SELECT t.id FROM track t LEFT JOIN album a ON a.id = t.album_id
		WHERE `+where+`
		ORDER BY `+order, params...)
}

// compileFilter turns a FilterOperator or FilterCondition into a SQL
// expression over track t and album a.
func (s *Server) compileFilter(raw json.RawMessage) (string, []any, error) {
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
		for _, c := range conditions {
			sql, p, err := s.compileFilter(c)
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
	for name, val := range fields {
		sql, p, err := s.compileCondition(name, val)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, "("+sql+")")
		params = append(params, p...)
	}
	return strings.Join(parts, " AND "), params, nil
}

func (s *Server) compileCondition(name string, raw json.RawMessage) (string, []any, error) {
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

	textColumns := map[string]string{"title": "t.title", "artist": "t.artist", "albumTitle": "a.title"}
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
	case "playCountAtLeast", "playCountAtMost":
		v, ok := num()
		if !ok || v < 0 || v != float64(int64(v)) {
			return bad("a non-negative integer")
		}
		op := map[string]string{"playCountAtLeast": ">=", "playCountAtMost": "<="}[name]
		return "t.play_count " + op + " ?", []any{int64(v)}, nil
	case "hasRating":
		v, ok := boolean()
		if !ok {
			return bad("a boolean")
		}
		return "(t.rating IS NOT NULL) = ?", []any{v}, nil
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
		// "Within N days" counts from the start of today, UTC, so the
		// answer changes once a day rather than every second; see
		// doc/playlists.md. -- claude, 2026-10-24
		today := s.cfg.Now().UTC().Truncate(24 * time.Hour)
		since := today.AddDate(0, 0, -int(v)).Format(utcDateLayout)
		return "t.last_played >= ?", []any{since}, nil
	case "inPlaylist":
		v, ok := str()
		if !ok {
			return bad("a playlist id")
		}
		return "EXISTS (SELECT 1 FROM playlist_track pt WHERE pt.playlist_id = ? AND pt.track_id = t.id)", []any{v}, nil
	}
	return "", nil, errorOf("unsupportedFilter", "no filter condition %q", name)
}

// The Track sort properties, each as the SQL it sorts by.
var trackSortColumns = map[string][]string{
	"title":      {"juke_fold(t.title)"},
	"artist":     {"juke_fold(t.artist)"},
	"albumTitle": {"juke_fold(a.title)"},
	"rating":     {"t.rating"},
	"playCount":  {"t.play_count"},
	"lastPlayed": {"t.last_played"},
	"albumOrder": {"juke_fold(a.title)", "t.album_id", "t.disc_index", "t.position"},
}

// compileTrackSort returns an ORDER BY clause.  Nulls sort last in either
// direction, and ties always break by id, so a sorted, limited list is
// stable.
func compileTrackSort(sort []comparator) (string, error) {
	var parts []string
	for _, c := range sort {
		cols, ok := trackSortColumns[c.Property]
		if !ok {
			return "", errorOf("unsupportedSort", "cannot sort on %q", c.Property)
		}
		dir := " ASC"
		if !c.ascending() {
			dir = " DESC"
		}
		for _, col := range cols {
			parts = append(parts, "("+col+" IS NULL)", col+dir)
		}
	}
	parts = append(parts, "t.id")
	return strings.Join(parts, ", "), nil
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
