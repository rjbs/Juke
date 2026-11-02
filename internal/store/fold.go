package store

import (
	"database/sql/driver"
	"hash/fnv"
	"strings"

	"modernc.org/sqlite"
)

// Fold lowercases a string for case-insensitive comparison.  JMAP's
// default collation, i;unicode-casemap, folds all of Unicode, but
// SQLite's NOCASE folds only ASCII, so "BJÖRK" would not match "Björk".
// Queries use juke_fold() instead.  Simple lowercasing is not full
// Unicode case folding, but it is close enough for titles and names.
// -- claude, 2026-10-24
func Fold(s string) string { return strings.ToLower(s) }

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("juke_fold", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			switch v := args[0].(type) {
			case string:
				return Fold(v), nil
			case []byte:
				return Fold(string(v)), nil
			default:
				return v, nil
			}
		})

	// juke_hash(id, seed) orders a random sort: the same seed gives the
	// same order on every run and every client, and a new seed reshuffles.
	// -- claude, 2026-11-01
	sqlite.MustRegisterDeterministicScalarFunction("juke_hash", 2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			return int64(Hash(text(args[0]), text(args[1]))), nil
		})
}

// Hash is the hash juke_hash computes: FNV-1a over the id and the seed,
// as a non-negative number.
func Hash(id, seed string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write([]byte(seed))
	return h.Sum64() >> 1
}

func text(v driver.Value) string {
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}
