package store

import (
	"database/sql/driver"
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
}
