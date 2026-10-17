//go:build !js

package sqlite

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const driver = "sqlite"

// MemoryOnly reports whether SQLite databases can only live in memory on this
// platform. See the package comment.
const MemoryOnly = false

// OpenRead opens an existing database read-only, so a running agent is never
// disturbed.
func OpenRead(path string) (*sql.DB, error) {
	return sql.Open(Driver, fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", path))
}
