//go:build js

package sqlite

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
)

const driver = "sqlite3"

// MemoryOnly is true: js/wasm has no mmap, so the file-backed VFS cannot open
// anything and every database must be mounted into memory.
const MemoryOnly = true

// OpenRead mounts the database's bytes into an in-memory VFS and opens it.
//
// Two consequences are worth knowing about:
//
//   - memdb.Create rewrites the SQLite header's file-format bytes from WAL (2)
//     to rollback-journal (1), which is what makes a WAL-mode database readable
//     without its -wal file. The snapshot is therefore the last checkpointed
//     state: anything still sitting in the -wal is not visible.
//   - The whole file is read into memory.
func OpenRead(path string) (*sql.DB, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := memdbName(path)
	// Create takes ownership of the slice and rewrites the header in place.
	memdb.Create(name, raw)
	return sql.Open(Driver, "file:/"+name+"?vfs=memdb")
}

// memdbName derives a unique, URL-safe name for the in-memory mount. memdb
// looks the database up by the name SQLite passes to the VFS (the DSN path
// without its leading slash), so this must not contain any DSN punctuation.
func memdbName(path string) string {
	sum := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%s-%x", filepath.Base(path), sum[:6])
}
