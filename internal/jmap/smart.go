package jmap

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"

	"semiotic.systems/juke/internal/store"
)

// ---- SmartPlaylist ------------------------------------------------------------

func loadSmartPlaylists(ctx context.Context, q store.Queryer, ids []string) (map[string]object, []string, error) {
	where, args := inClause("id", ids)
	rows, err := q.QueryContext(ctx,
		`SELECT id, title, filter, sort, "limit", limit_bytes, limit_seconds, is_helper FROM smart_playlist`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := map[string]object{}
	var order []string
	for rows.Next() {
		var id, title, sort string
		var filter sql.NullString
		var limit, limitBytes, limitSeconds sql.NullInt64
		var isHelper bool
		if err := rows.Scan(&id, &title, &filter, &sort, &limit, &limitBytes, &limitSeconds, &isHelper); err != nil {
			return nil, nil, err
		}
		obj := object{
			"id":           id,
			"title":        title,
			"filter":       nil,
			"sort":         json.RawMessage(sort),
			"limit":        nullInt(limit),
			"limitBytes":   nullInt(limitBytes),
			"limitSeconds": nullInt(limitSeconds),
			"isHelper":     isHelper,
		}
		if filter.Valid {
			obj["filter"] = json.RawMessage(filter.String)
		}
		out[id] = obj
		order = append(order, id)
	}
	return out, order, rows.Err()
}

func nullInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

// ---- SmartPlaylist/set --------------------------------------------------------

// smartProps is a SmartPlaylist create or patch, decoded and checked.  A
// nil pointer, or a false has flag, means the property wasn't given.
type smartProps struct {
	title *string

	hasFilter bool
	filter    *string     // the filter's JSON, or nil for none
	refs      []store.Ref // the playlists the filter names

	sort   *string // the sort's JSON, seeds filled in
	seeded bool    // a random comparator was given no seed and got one

	hasLimit, hasLimitBytes, hasLimitSeconds bool
	limit, limitBytes, limitSeconds          *int64

	isHelper *bool
}

// decodeSmartProps checks every property given.  The filter and sort
// are compiled as Track/query would compile them, so a stored rule is
// one that runs, and a filter's references must name playlists that
// exist.
func (s *Server) decodeSmartProps(ctx context.Context, tx *sql.Tx, props map[string]json.RawMessage) (smartProps, error) {
	var p smartProps
	var bad []string
	for _, name := range sortedKeys(props) {
		raw := props[name]
		switch name {
		case "title":
			title, ok := titleValue(raw)
			if !ok {
				bad = append(bad, name)
				continue
			}
			p.title = &title
		case "filter":
			p.hasFilter = true
			if string(raw) == "null" {
				continue
			}
			c := &compiler{s: s, ctx: ctx, q: tx, validate: true}
			if _, _, err := c.filter(raw); err != nil {
				bad = append(bad, name)
				continue
			}
			compact, err := compactJSON(raw)
			if err != nil {
				bad = append(bad, name)
				continue
			}
			p.filter, p.refs = &compact, c.refs
		case "sort":
			var sort []comparator
			if json.Unmarshal(raw, &sort) != nil || sort == nil {
				bad = append(bad, name)
				continue
			}
			ok := true
			for i := range sort {
				if sort[i].Collation != "" && sort[i].Collation != "i;unicode-casemap" {
					ok = false
				}
				if sort[i].Property == "random" && sort[i].Seed == "" {
					seed, err := store.NewSeed()
					if err != nil {
						return p, err
					}
					sort[i].Seed = seed
					p.seeded = true
				}
			}
			if _, _, err := compileTrackSort(sort); err != nil || !ok {
				bad = append(bad, name)
				continue
			}
			encoded, err := json.Marshal(sort)
			if err != nil {
				return p, err
			}
			text := string(encoded)
			p.sort = &text
		case "limit", "limitBytes", "limitSeconds":
			v, ok := nullableCountValue(raw)
			if !ok {
				bad = append(bad, name)
				continue
			}
			switch name {
			case "limit":
				p.hasLimit, p.limit = true, v
			case "limitBytes":
				p.hasLimitBytes, p.limitBytes = true, v
			case "limitSeconds":
				p.hasLimitSeconds, p.limitSeconds = true, v
			}
		case "isHelper":
			v, ok := boolValue(raw)
			if !ok {
				bad = append(bad, name)
				continue
			}
			p.isHelper = &v
		default:
			bad = append(bad, name)
		}
	}
	if len(bad) > 0 {
		return p, invalidProperties("only title, filter, sort, limit, limitBytes, limitSeconds and isHelper may be set; a filter and sort must be ones Track/query accepts, naming playlists that exist", bad)
	}
	return p, nil
}

// nullableCountValue decodes an UnsignedInt|null.
func nullableCountValue(raw json.RawMessage) (*int64, bool) {
	if string(raw) == "null" {
		return nil, true
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil || f < 0 || f != math.Trunc(f) || f > 1<<53-1 {
		return nil, false
	}
	n := int64(f)
	return &n, true
}

func compactJSON(raw json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	out, err := json.Marshal(v)
	return string(out), err
}

// wouldCycle reports whether a smart playlist whose rule names refs
// could be reached from one of them: whether the references would form
// a loop through id.
func wouldCycle(ctx context.Context, q store.Queryer, id string, refs []store.Ref) (bool, error) {
	seen := map[string]bool{}
	var stack []string
	for _, r := range refs {
		if r.Type == store.TypeSmartPlaylist {
			stack = append(stack, r.ID)
		}
	}
	for len(stack) > 0 {
		next := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if next == id {
			return true, nil
		}
		if seen[next] {
			continue
		}
		seen[next] = true
		more, err := store.SmartPlaylistRefs(ctx, q, next)
		if err != nil {
			return false, err
		}
		stack = append(stack, more...)
	}
	return false, nil
}

var errPlaylistInUse = &methodError{Type: "playlistInUse", Description: "a smart playlist's rule names this playlist"}

func (s *Server) smartPlaylistSetter() *setter {
	return &setter{
		typ: store.TypeSmartPlaylist,
		create: func(ctx context.Context, tx *sql.Tx, props map[string]json.RawMessage) (object, error) {
			p, err := s.decodeSmartProps(ctx, tx, props)
			if err != nil {
				return nil, err
			}
			if p.title == nil {
				return nil, invalidProperties("a smart playlist needs a title", []string{"title"})
			}
			created := object{}
			if !p.hasFilter {
				created["filter"] = nil
			}
			if p.sort == nil {
				empty := "[]"
				p.sort = &empty
			}
			if p.seeded || props["sort"] == nil {
				created["sort"] = json.RawMessage(*p.sort)
			}
			for name, has := range map[string]bool{"limit": p.hasLimit, "limitBytes": p.hasLimitBytes, "limitSeconds": p.hasLimitSeconds} {
				if !has {
					created[name] = nil
				}
			}
			id, err := store.InsertSmartPlaylist(ctx, tx, *p.title, store.SmartQuery{
				Filter: p.filter, Sort: *p.sort, Limit: p.limit, LimitBytes: p.limitBytes, LimitSeconds: p.limitSeconds,
			})
			if err != nil {
				return nil, err
			}
			if err := store.SetSmartPlaylistRefs(ctx, tx, id, p.refs); err != nil {
				return nil, err
			}
			if err := createHelper(ctx, tx, "smart_playlist", id, p.isHelper, created); err != nil {
				return nil, err
			}
			created["id"] = id
			return created, nil
		},
		update: func(ctx context.Context, tx *sql.Tx, id string, patch map[string]json.RawMessage) (object, error) {
			if err := mustExist(ctx, tx, "smart_playlist", id); err != nil {
				return nil, err
			}
			p, err := s.decodeSmartProps(ctx, tx, patch)
			if err != nil {
				return nil, err
			}
			title, sq, err := store.LoadSmartPlaylist(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			if p.title != nil {
				title = *p.title
			}
			if p.hasFilter {
				cycle, err := wouldCycle(ctx, tx, id, p.refs)
				if err != nil {
					return nil, err
				}
				if cycle {
					return nil, invalidProperties("the filter would make a loop of smart playlists", []string{"filter"})
				}
				sq.Filter = p.filter
			}
			if p.sort != nil {
				sq.Sort = *p.sort
			}
			if p.hasLimit {
				sq.Limit = p.limit
			}
			if p.hasLimitBytes {
				sq.LimitBytes = p.limitBytes
			}
			if p.hasLimitSeconds {
				sq.LimitSeconds = p.limitSeconds
			}
			if err := store.WriteSmartPlaylist(ctx, tx, id, title, sq); err != nil {
				return nil, err
			}
			if p.isHelper != nil {
				if _, err := tx.ExecContext(ctx, `UPDATE smart_playlist SET is_helper = ? WHERE id = ?`, *p.isHelper, id); err != nil {
					return nil, err
				}
			}
			if p.hasFilter {
				if err := store.SetSmartPlaylistRefs(ctx, tx, id, p.refs); err != nil {
					return nil, err
				}
			}
			if p.seeded {
				return object{"sort": json.RawMessage(sq.Sort)}, nil
			}
			return nil, nil
		},
		destroy: func(ctx context.Context, tx *sql.Tx, id string) error {
			if err := mustExist(ctx, tx, "smart_playlist", id); err != nil {
				return err
			}
			used, err := store.Referenced(ctx, tx, store.TypeSmartPlaylist, id)
			if err != nil {
				return err
			}
			if used {
				return errPlaylistInUse
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM smart_playlist_ref WHERE smart_playlist_id = ?`, id); err != nil {
				return err
			}
			return store.Destroy(ctx, tx, store.TypeSmartPlaylist, id)
		},
	}
}
