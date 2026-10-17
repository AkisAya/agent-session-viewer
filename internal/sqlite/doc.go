// Package sqlite selects the SQLite driver for the target platform and hides
// the one thing that does not port between them: opening a database.
//
// Native builds use modernc.org/sqlite, a pure-Go transpilation of SQLite with
// no cgo, which is what lets this tool ship as a single binary. wasm builds
// cannot use it -- modernc.org/libc has no wasm target, so the package does not
// compile at all -- and use ncruces/go-sqlite3 instead, which runs SQLite
// compiled to WebAssembly inside wazero and is therefore independent of
// GOOS/GOARCH.
//
// Both register a database/sql driver and accept the same "_pragma=" DSN
// syntax, so callers only need Driver. The difference that matters is that
// js/wasm has no mmap, so a file-backed SQLite VFS cannot work there: OpenRead
// mounts the file's bytes into an in-memory VFS on wasm, and opens the path
// directly elsewhere.
package sqlite

// Driver is the database/sql driver name registered for this platform.
//
// The two drivers deliberately keep their upstream names ("sqlite" and
// "sqlite3") so that a DSN copied from the upstream documentation still works.
const Driver = driver
