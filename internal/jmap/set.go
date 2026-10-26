package jmap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
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

	// update applies a patch to an existing object.
	update func(ctx context.Context, tx *sql.Tx, id string, patch map[string]json.RawMessage) error

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
		if err := h.update(ctx, tx, id, args.Update[key]); err != nil {
			if err := setError(notUpdated, key, err); err != nil {
				return nil, err
			}
			continue
		}
		updated[id] = nil
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
func mustExist(ctx context.Context, tx *sql.Tx, table, id string) error {
	var found bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = ?)`, id).Scan(&found); err != nil {
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
