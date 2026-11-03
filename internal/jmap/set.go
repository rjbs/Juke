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

// ---- /set -----------------------------------------------------------------

type setArgs struct {
	AccountID string                                `json:"accountId"`
	IfInState *string                               `json:"ifInState"`
	Create    map[string]map[string]json.RawMessage `json:"create"`
	Update    map[string]map[string]json.RawMessage `json:"update"`
	Destroy   []string                              `json:"destroy"`
}

// setter is what one type's /set does with each object, inside the
// method's transaction.  A *methodError result is a SetError for that
// object alone; any other error fails the whole method.
type setter struct {
	typ string

	// create makes an object and returns it as the client should see it
	// in "created": its id and any property the client didn't give.
	create func(ctx context.Context, tx *sql.Tx, props map[string]json.RawMessage) (object, error)

	// update applies a patch to an existing object, and returns any
	// property the server set as a side effect, for "updated".
	update func(ctx context.Context, tx *sql.Tx, id string, patch map[string]json.RawMessage) (object, error)

	// destroy removes an object, leaving a tombstone.
	destroy func(ctx context.Context, tx *sql.Tx, id string) error
}

// set runs a /set: creates, then updates, then destroys, as RFC 8620
// orders them, all in one transaction.  An update or destroy may name an
// object created in the same call as "#" and its creation id.
func (s *Server) set(ctx context.Context, h *setter, raw json.RawMessage) (any, error) {
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

	oldState, _, err := s.state(ctx, tx, h.typ)
	if err != nil {
		return nil, err
	}
	if args.IfInState != nil && *args.IfInState != oldState {
		return nil, errorOf("stateMismatch", "state is %s", oldState)
	}

	// setError sorts an object's failure: a SetError goes in the map, and
	// anything else is returned to fail the method.
	setError := func(failed map[string]any, key string, err error) error {
		var me *methodError
		if !errors.As(err, &me) {
			return err
		}
		failed[key] = me
		return nil
	}

	created, notCreated := map[string]any{}, map[string]any{}
	createdIDs := map[string]string{}
	for _, cid := range sortedKeys(args.Create) {
		obj, err := h.create(ctx, tx, args.Create[cid])
		if err != nil {
			if err := setError(notCreated, cid, err); err != nil {
				return nil, err
			}
			continue
		}
		created[cid] = obj
		createdIDs[cid] = obj["id"].(string)
	}

	// resolve turns "#cid" into the id of the object created as cid.
	resolve := func(id string) (string, bool) {
		cid, ok := strings.CutPrefix(id, "#")
		if !ok {
			return id, true
		}
		real, ok := createdIDs[cid]
		return real, ok
	}

	updated, notUpdated := map[string]any{}, map[string]any{}
	for _, key := range sortedKeys(args.Update) {
		id, ok := resolve(key)
		if !ok {
			notUpdated[key] = &methodError{Type: "notFound", Description: "no object was created as " + key}
			continue
		}
		changed, err := h.update(ctx, tx, id, args.Update[key])
		if err != nil {
			if err := setError(notUpdated, key, err); err != nil {
				return nil, err
			}
			continue
		}
		if len(changed) == 0 {
			updated[id] = nil
		} else {
			updated[id] = changed
		}
	}

	destroyed, notDestroyed := []string{}, map[string]any{}
	for _, key := range dedupe(args.Destroy) {
		id, ok := resolve(key)
		if !ok {
			notDestroyed[key] = &methodError{Type: "notFound", Description: "no object was created as " + key}
			continue
		}
		if err := h.destroy(ctx, tx, id); err != nil {
			if err := setError(notDestroyed, key, err); err != nil {
				return nil, err
			}
			continue
		}
		destroyed = append(destroyed, id)
	}

	newState, _, err := s.state(ctx, tx, h.typ)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.changed.wake()

	orNull := func(m map[string]any) any {
		if len(m) == 0 {
			return nil
		}
		return m
	}
	var destroyedOrNull any
	if len(destroyed) > 0 {
		destroyedOrNull = destroyed
	}
	return map[string]any{
		"accountId":    args.AccountID,
		"oldState":     oldState,
		"newState":     newState,
		"created":      orNull(created),
		"updated":      orNull(updated),
		"destroyed":    destroyedOrNull,
		"notCreated":   orNull(notCreated),
		"notUpdated":   orNull(notUpdated),
		"notDestroyed": orNull(notDestroyed),
	}, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mustExist returns a notFound SetError if the table has no row with the
// id.
func mustExist(ctx context.Context, q store.Queryer, table, id string) error {
	var found bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = ?)`, id).Scan(&found); err != nil {
		return err
	}
	if !found {
		return &methodError{Type: "notFound"}
	}
	return nil
}

// invalidProperties is the SetError for a create or patch that names
// properties it may not, or gives them bad values.
func invalidProperties(description string, props []string) *methodError {
	sort.Strings(props)
	return &methodError{Type: "invalidProperties", Description: description, Properties: props}
}

// ---- property values --------------------------------------------------------

// titleValue decodes a title: a string with something in it.
func titleValue(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil || strings.TrimSpace(s) == "" {
		return "", false
	}
	return s, true
}

// nullableStringValue decodes a String|null.
func nullableStringValue(raw json.RawMessage) (*string, bool) {
	if string(raw) == "null" {
		return nil, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return nil, false
	}
	return &s, true
}

// trackIDsValue decodes a list of track ids, every one of which must
// name a track.
func trackIDsValue(ctx context.Context, tx *sql.Tx, raw json.RawMessage) ([]string, bool, error) {
	var ids []string
	if json.Unmarshal(raw, &ids) != nil || ids == nil {
		return nil, false, nil
	}
	ok, err := tracksExist(ctx, tx, ids)
	return ids, ok, err
}

func tracksExist(ctx context.Context, tx *sql.Tx, ids []string) (bool, error) {
	unique := dedupe(ids)
	if len(unique) == 0 {
		return true, nil
	}
	where, args := inClause("id", unique)
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM track`+where, args...).Scan(&n); err != nil {
		return false, err
	}
	return n == len(unique), nil
}

// ---- Playlist/set -------------------------------------------------------------

// playlistProps is a Playlist create or patch, decoded.  A nil field was
// not given.
type playlistProps struct {
	title    *string
	trackIDs *[]string
}

func decodePlaylistProps(ctx context.Context, tx *sql.Tx, props map[string]json.RawMessage) (playlistProps, error) {
	var p playlistProps
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
		case "trackIds":
			ids, ok, err := trackIDsValue(ctx, tx, raw)
			if err != nil {
				return p, err
			}
			if !ok {
				bad = append(bad, name)
				continue
			}
			p.trackIDs = &ids
		default:
			bad = append(bad, name)
		}
	}
	if len(bad) > 0 {
		return p, invalidProperties("only title and trackIds may be set; trackIds must name tracks", bad)
	}
	return p, nil
}

var playlistSetter = &setter{
	typ: store.TypePlaylist,
	create: func(ctx context.Context, tx *sql.Tx, props map[string]json.RawMessage) (object, error) {
		p, err := decodePlaylistProps(ctx, tx, props)
		if err != nil {
			return nil, err
		}
		if p.title == nil {
			return nil, invalidProperties("a playlist needs a title", []string{"title"})
		}
		created := object{}
		if p.trackIDs == nil {
			p.trackIDs = &[]string{}
			created["trackIds"] = []string{}
		}
		id, err := store.InsertPlaylist(ctx, tx, *p.title, *p.trackIDs)
		if err != nil {
			return nil, err
		}
		created["id"] = id
		return created, nil
	},
	update: func(ctx context.Context, tx *sql.Tx, id string, patch map[string]json.RawMessage) (object, error) {
		if err := mustExist(ctx, tx, "playlist", id); err != nil {
			return nil, err
		}
		p, err := decodePlaylistProps(ctx, tx, patch)
		if err != nil {
			return nil, err
		}
		modseq, err := store.NextModseq(ctx, tx)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE playlist SET modseq = ?, title = coalesce(?, title) WHERE id = ?`, modseq, p.title, id,
		); err != nil {
			return nil, err
		}
		if p.trackIDs != nil {
			return nil, store.SetPlaylistTracks(ctx, tx, id, *p.trackIDs)
		}
		return nil, nil
	},
	destroy: func(ctx context.Context, tx *sql.Tx, id string) error {
		if err := mustExist(ctx, tx, "playlist", id); err != nil {
			return err
		}
		used, err := store.Referenced(ctx, tx, store.TypePlaylist, id)
		if err != nil {
			return err
		}
		if used {
			return errPlaylistInUse
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM playlist_track WHERE playlist_id = ?`, id); err != nil {
			return err
		}
		return store.Destroy(ctx, tx, store.TypePlaylist, id)
	},
}

// ---- Album/set ----------------------------------------------------------------

// albumProps is an Album create or patch, decoded.  A false has* or nil
// field was not given.
type albumProps struct {
	title      *string
	hasArtist  bool
	artist     *string
	discs      []store.Disc
	hasArtwork bool
	artwork    *string
}

func decodeAlbumProps(ctx context.Context, tx *sql.Tx, props map[string]json.RawMessage) (albumProps, error) {
	var p albumProps
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
		case "artist":
			artist, ok := nullableStringValue(raw)
			if !ok {
				bad = append(bad, name)
				continue
			}
			p.hasArtist, p.artist = true, artist
		case "discs":
			discs, ok, err := discsValue(ctx, tx, raw)
			if err != nil {
				return p, err
			}
			if !ok {
				bad = append(bad, name)
				continue
			}
			p.discs = discs
		case "artworkBlobId":
			art, ok := nullableStringValue(raw)
			if ok && !reviewing(ctx) {
				return p, invalidProperties("artworkBlobId is set only under the review capability", []string{name})
			}
			if ok && art != nil {
				found, err := artworkExists(ctx, tx, *art)
				if err != nil {
					return p, err
				}
				ok = found
			}
			if !ok {
				bad = append(bad, name)
				continue
			}
			p.hasArtwork, p.artwork = true, art
		default:
			bad = append(bad, name)
		}
	}
	if len(bad) > 0 {
		return p, invalidProperties(
			"only title, artist, discs and artworkBlobId may be set; discs must name tracks, each once; artworkBlobId must name a stored image", bad)
	}
	return p, nil
}

// discsValue decodes an album's discs: at least one, each with trackIds
// and perhaps a title, between them naming tracks that exist, each no
// more than once.
func discsValue(ctx context.Context, tx *sql.Tx, raw json.RawMessage) ([]store.Disc, bool, error) {
	var in []map[string]json.RawMessage
	if json.Unmarshal(raw, &in) != nil || len(in) == 0 {
		return nil, false, nil
	}
	var discs []store.Disc
	seen := map[string]bool{}
	var all []string
	for _, d := range in {
		var disc store.Disc
		for name, v := range d {
			switch name {
			case "trackIds":
				if json.Unmarshal(v, &disc.TrackIDs) != nil || disc.TrackIDs == nil {
					return nil, false, nil
				}
			case "title":
				title, ok := nullableStringValue(v)
				if !ok {
					return nil, false, nil
				}
				disc.Title = title
			default:
				return nil, false, nil
			}
		}
		if disc.TrackIDs == nil {
			return nil, false, nil
		}
		for _, id := range disc.TrackIDs {
			if seen[id] {
				return nil, false, nil
			}
			seen[id] = true
		}
		all = append(all, disc.TrackIDs...)
		discs = append(discs, disc)
	}
	ok, err := tracksExist(ctx, tx, all)
	return discs, ok, err
}

var albumSetter = &setter{
	typ: store.TypeAlbum,
	create: func(ctx context.Context, tx *sql.Tx, props map[string]json.RawMessage) (object, error) {
		p, err := decodeAlbumProps(ctx, tx, props)
		if err != nil {
			return nil, err
		}
		if p.title == nil {
			return nil, invalidProperties("an album needs a title", []string{"title"})
		}
		created := object{}
		if !p.hasArtist {
			created["artist"] = nil
		}
		if p.discs == nil {
			p.discs = []store.Disc{{TrackIDs: []string{}}}
			created["discs"] = []any{map[string]any{"trackIds": []string{}, "title": nil}}
		}
		id, err := store.InsertAlbum(ctx, tx, store.NewAlbum{Title: *p.title, Artist: p.artist})
		if err != nil {
			return nil, err
		}
		if err := store.SetDiscs(ctx, tx, id, p.discs); err != nil {
			return nil, err
		}
		if p.hasArtwork {
			if err := setAlbumArtwork(ctx, tx, id, p.artwork); err != nil {
				return nil, err
			}
		}
		created["id"] = id
		return created, nil
	},
	update: func(ctx context.Context, tx *sql.Tx, id string, patch map[string]json.RawMessage) (object, error) {
		if err := mustExist(ctx, tx, "album", id); err != nil {
			return nil, err
		}
		p, err := decodeAlbumProps(ctx, tx, patch)
		if err != nil {
			return nil, err
		}
		modseq, err := store.NextModseq(ctx, tx)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE album SET modseq = ?, title = coalesce(?, title),
			  artist = CASE WHEN ? THEN ? ELSE artist END
			WHERE id = ?`, modseq, p.title, p.hasArtist, p.artist, id,
		); err != nil {
			return nil, err
		}
		if p.discs != nil {
			if err := store.SetDiscs(ctx, tx, id, p.discs); err != nil {
				return nil, err
			}
		}
		if p.hasArtwork {
			return nil, setAlbumArtwork(ctx, tx, id, p.artwork)
		}
		return nil, nil
	},
	destroy: func(ctx context.Context, tx *sql.Tx, id string) error {
		if err := mustExist(ctx, tx, "album", id); err != nil {
			return err
		}
		// An album must be emptied before it's destroyed, so that a slip
		// can't leave its tracks on no album.  -- claude, 2026-10-25
		var tracks int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM track WHERE album_id = ?`, id).Scan(&tracks); err != nil {
			return err
		}
		if tracks > 0 {
			return &methodError{Type: "albumNotEmpty", Description: "move or remove the album's tracks first"}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM disc WHERE album_id = ?`, id); err != nil {
			return err
		}
		return store.Destroy(ctx, tx, store.TypeAlbum, id)
	},
}

// setAlbumArtwork gives an album the image chosen for it by hand, or none,
// and takes away its tracks' own images: the choice is for the whole
// album, and later artwork runs leave an album and tracks so marked
// alone.  A track with an image of its own can be given one again by
// Track/set.  Only the tracks whose art changes get a new modseq; the
// marking itself is nothing a client sees.  -- claude, 2026-11-02
func setAlbumArtwork(ctx context.Context, tx *sql.Tx, id string, art *string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE album SET artwork_id = ?, artwork_source = 'user' WHERE id = ?`, art, id); err != nil {
		return err
	}
	modseq, err := store.NextModseq(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE track SET artwork_id = NULL, artwork_source = 'user',
		  modseq = CASE WHEN artwork_id IS NULL THEN modseq ELSE ? END
		WHERE album_id = ?`, modseq, id)
	return err
}
