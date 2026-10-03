package db

import (
	"database/sql/driver"
	"strings"

	"modernc.org/sqlite"
)

// tt_lower is lower() with Go's Unicode case mapping. SQLite's built-in
// lower() only handles ASCII, so search uses this to match the same text
// Postgres's lower() does.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("tt_lower", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			switch v := args[0].(type) {
			case string:
				return strings.ToLower(v), nil
			case []byte:
				return strings.ToLower(string(v)), nil
			default:
				return v, nil
			}
		})
}
