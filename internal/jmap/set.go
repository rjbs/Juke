package jmap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	gizmo "github.com/rjbs/gizmo/jmap"
	"github.com/rjbs/gizmo/std"

	"semiotic.systems/juke/internal/store"
	"semiotic.systems/juke/internal/tags"
)

// ---- /set -----------------------------------------------------------------

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

// set runs a /set through Gizmo, which gives each object a savepoint in one
// transaction for the call, and destroys, then creates, then updates.  An
// update or destroy may name an object created in the same call as "#" and
// its creation id.
func (s *Server) set(c *gizmo.Call, h *setter, raw json.RawMessage) (any, error) {
	req, err := std.ParseSet(raw, nil)
	if err != nil {
		return nil, err
	}
	properties := func(raw json.RawMessage) (map[string]json.RawMessage, error) {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(raw, &props); err != nil {
			return nil, &gizmo.SetError{Type: "invalidProperties", Description: err.Error()}
		}
		return props, nil
	}
	return std.Set(c, s.begin, s.limits(), std.Setter[*txn]{
		Type: h.typ,
		Create: func(ctx context.Context, tx *txn, raw json.RawMessage) (string, map[string]any, error) {
			props, err := properties(raw)
			if err != nil {
				return "", nil, err
			}
			obj, err := h.create(ctx, tx.Tx, props)
			if err != nil {
				return "", nil, setError(err)
			}
			return obj["id"].(string), obj, nil
		},
		Update: func(ctx context.Context, tx *txn, id string, raw json.RawMessage) (map[string]any, error) {
			patch, err := properties(raw)
			if err != nil {
				return nil, err
			}
			changed, err := h.update(ctx, tx.Tx, id, patch)
			return changed, setError(err)
		},
		Destroy: func(ctx context.Context, tx *txn, id string) error {
			return setError(h.destroy(ctx, tx.Tx, id))
		},
	}, req)
}

// setError makes a SetError, for Gizmo, of the method error that a setter
// returns for an object that fails alone.  Any other error is left as it
// is, and fails the method.
func setError(err error) error {
	var me *methodError
	if errors.As(err, &me) {
		return &gizmo.SetError{Type: me.Type, Description: me.Description, Properties: me.Properties}
	}
	return err
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

// boolValue decodes a Boolean.
func boolValue(raw json.RawMessage) (bool, bool) {
	var v bool
	if string(raw) == "null" || json.Unmarshal(raw, &v) != nil {
		return false, false
	}
	return v, true
}

// createHelper stores isHelper for a playlist of either kind just
// inserted into table, which starts out false, or reports that default
// in created when the create didn't give it.
func createHelper(ctx context.Context, tx *sql.Tx, table, id string, isHelper *bool, created object) error {
	if isHelper == nil {
		created["isHelper"] = false
		return nil
	}
	if !*isHelper {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE `+table+` SET is_helper = 1 WHERE id = ?`, id)
	return err
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
	isHelper *bool
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
		return p, invalidProperties("only title, trackIds and isHelper may be set; trackIds must name tracks", bad)
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
		if err := createHelper(ctx, tx, "playlist", id, p.isHelper, created); err != nil {
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
		modseq, err := store.NextModseq(ctx, tx, store.TypePlaylist)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE playlist SET modseq = ?, title = coalesce(?, title), is_helper = coalesce(?, is_helper) WHERE id = ?`,
			modseq, p.title, p.isHelper, id,
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
	title       *string
	hasArtist   bool
	artist      *string
	hasReleased bool
	released    *string
	discs       []store.Disc
	hasArtwork  bool
	artwork     *string
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
		case "released":
			released, ok := nullableStringValue(raw)
			if !ok || (released != nil && !tags.ValidReleased(*released)) {
				bad = append(bad, name)
				continue
			}
			p.hasReleased, p.released = true, released
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
			"only title, artist, released, discs and artworkBlobId may be set; released is YYYY-MM-DD with 00 for a month or day not known; discs must name tracks, each once; artworkBlobId must name a stored image", bad)
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
		if !p.hasReleased {
			created["released"] = nil
		}
		if p.discs == nil {
			p.discs = []store.Disc{{TrackIDs: []string{}}}
			created["discs"] = []any{map[string]any{"trackIds": []string{}, "title": nil}}
		}
		id, err := store.InsertAlbum(ctx, tx, store.NewAlbum{Title: *p.title, Artist: p.artist, Released: p.released})
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
		modseq, err := store.NextModseq(ctx, tx, store.TypeAlbum)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE album SET modseq = ?, title = coalesce(?, title),
			  artist = CASE WHEN ? THEN ? ELSE artist END,
			  released = CASE WHEN ? THEN ? ELSE released END
			WHERE id = ?`, modseq, p.title, p.hasArtist, p.artist, p.hasReleased, p.released, id,
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
	modseq, err := store.NextModseq(ctx, tx, store.TypeTrack)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE track SET artwork_id = NULL, artwork_source = 'user',
		  modseq = CASE WHEN artwork_id IS NULL THEN modseq ELSE ? END
		WHERE album_id = ?`, modseq, id)
	return err
}
