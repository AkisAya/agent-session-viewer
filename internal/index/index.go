// Package index maintains a local SQLite cache (on disk, or in memory when
// requested) of session summaries and orchestrates incremental scanning of
// every source.
package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/source"

	_ "modernc.org/sqlite"
)

// Index owns the cache database and the set of sources.
type Index struct {
	db      *sql.DB
	sources []source.Source
	byName  map[string]source.Source

	mu       sync.Mutex
	scanning bool
	lastScan time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS session (
  agent       TEXT NOT NULL,
  id          TEXT NOT NULL,
  title       TEXT NOT NULL DEFAULT '',
  preview     TEXT NOT NULL DEFAULT '',
  cwd         TEXT NOT NULL DEFAULT '',
  model       TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL DEFAULT 0,
  updated_at  INTEGER NOT NULL DEFAULT 0,
  msg_count   INTEGER NOT NULL DEFAULT 0,
  tokens_in   INTEGER NOT NULL DEFAULT 0,
  tokens_out  INTEGER NOT NULL DEFAULT 0,
  cache_read  INTEGER NOT NULL DEFAULT 0,
  cost        REAL NOT NULL DEFAULT 0,
  parent_id   TEXT NOT NULL DEFAULT '',
  relation    TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (agent, id)
);
CREATE INDEX IF NOT EXISTS session_updated_idx ON session(updated_at DESC);
CREATE INDEX IF NOT EXISTS session_agent_idx ON session(agent);
CREATE INDEX IF NOT EXISTS session_parent_idx ON session(parent_id);

CREATE TABLE IF NOT EXISTS cursor (
  agent  TEXT NOT NULL,
  key    TEXT NOT NULL,
  id     TEXT NOT NULL DEFAULT '',
  size   INTEGER NOT NULL DEFAULT 0,
  mtime  INTEGER NOT NULL DEFAULT 0,
  offset INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (agent, key)
);

CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS activity (
  agent TEXT NOT NULL,
  id    TEXT NOT NULL,
  src   TEXT NOT NULL,
  day   TEXT NOT NULL,
  msgs  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (agent, id, src, day)
);
CREATE INDEX IF NOT EXISTS activity_day_idx ON activity(day);

CREATE TABLE IF NOT EXISTS tool_use (
  agent TEXT NOT NULL,
  id    TEXT NOT NULL,
  src   TEXT NOT NULL,
  tool  TEXT NOT NULL,
  cnt   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (agent, id, src, tool)
);

-- session_usage is the per-resource (file) contribution to a session. A
-- session's totals are the sum over its resources, so a Codex session that
-- spans several rollout files accumulates them instead of overwriting one
-- another. Membership here also decides whether a session is still alive.
CREATE TABLE IF NOT EXISTS session_usage (
  agent      TEXT NOT NULL,
  id         TEXT NOT NULL,
  src        TEXT NOT NULL,
  msg_count  INTEGER NOT NULL DEFAULT 0,
  tokens_in  INTEGER NOT NULL DEFAULT 0,
  tokens_out INTEGER NOT NULL DEFAULT 0,
  cache_read INTEGER NOT NULL DEFAULT 0,
  cost       REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (agent, id, src)
);
CREATE INDEX IF NOT EXISTS session_usage_src_idx ON session_usage(agent, src);
`

// Options configures how the cache is stored.
type Options struct {
	// Path is the on-disk SQLite database. Ignored when Memory is set.
	Path string
	// Memory keeps the cache in an in-memory SQLite database: nothing is
	// written to disk and the index is rebuilt with a full scan on every start.
	Memory bool
}

// Open creates/opens the cache database and registers the sources.
func Open(opts Options, sources ...source.Source) (*Index, error) {
	var dsn string
	if opts.Memory {
		// A named shared in-memory database: every pooled connection sees the
		// same data, and the database lives as long as this process holds a
		// connection open. No WAL/synchronous pragmas — they are meaningless
		// (WAL is unsupported) for in-memory databases.
		dsn = fmt.Sprintf("file:asv-%d?mode=memory&cache=shared", os.Getpid())
	} else {
		if err := os.MkdirAll(filepath.Dir(opts.Path), 0o755); err != nil {
			return nil, err
		}
		dsn = fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", opts.Path)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Serialize writes; reads are cheap. A single connection also keeps the
	// shared in-memory database alive for the lifetime of the process.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	// Migration for cache databases created before these columns existed.
	_, _ = db.Exec(`ALTER TABLE session ADD COLUMN preview TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE session ADD COLUMN parent_id TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE session ADD COLUMN relation TEXT NOT NULL DEFAULT ''`)

	// Bump when the parsing/extraction changes in a way that requires a full
	// re-scan (cursors are invalidated so every source is re-read once).
	// v4: activity/tool histograms + parent links; v5: MultiEdit alias fix;
	// v6: OpenCode parent_id relabelled as "subagent" (was "fork");
	// v7: session_source membership table; v8: per-source usage table
	// (session totals are summed from their resources).
	const schemaVersion = "8"
	var ver string
	_ = db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&ver)
	if ver != schemaVersion {
		_, _ = db.Exec(`DELETE FROM cursor`)
		_, _ = db.Exec(`DELETE FROM session`)
		_, _ = db.Exec(`DELETE FROM activity`)
		_, _ = db.Exec(`DELETE FROM tool_use`)
		_, _ = db.Exec(`DELETE FROM session_usage`)
		_, _ = db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('schema_version', ?)`, schemaVersion)
	}
	ix := &Index{db: db, sources: sources, byName: map[string]source.Source{}}
	for _, s := range sources {
		ix.byName[s.Name()] = s
	}
	return ix, nil
}

// Close releases the database handle.
func (ix *Index) Close() error { return ix.db.Close() }

// Scan performs an incremental scan of every source. It is safe to call
// concurrently; overlapping calls are skipped.
func (ix *Index) Scan() error {
	ix.mu.Lock()
	if ix.scanning {
		ix.mu.Unlock()
		return nil
	}
	ix.scanning = true
	ix.mu.Unlock()
	defer func() {
		ix.mu.Lock()
		ix.scanning = false
		ix.lastScan = time.Now()
		ix.mu.Unlock()
	}()

	for _, src := range ix.sources {
		if err := ix.scanSource(src); err != nil {
			return err
		}
	}
	return nil
}

func (ix *Index) scanSource(src source.Source) error {
	agent := src.Name()
	prev, err := ix.loadCursors(agent)
	if err != nil {
		return err
	}

	var deltas []source.Delta
	next, err := src.Scan(prev, func(d source.Delta) { deltas = append(deltas, d) })
	if err != nil {
		return err
	}

	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	w, err := newWriter(tx)
	if err != nil {
		return err
	}
	defer w.close()

	for _, d := range deltas {
		if err := w.apply(agent, d); err != nil {
			return err
		}
	}

	// Replace cursors and prune sessions whose backing resource disappeared.
	if _, err := tx.Exec(`DELETE FROM cursor WHERE agent = ?`, agent); err != nil {
		return err
	}
	insCur, err := tx.Prepare(`INSERT INTO cursor(agent,key,id,size,mtime,offset) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insCur.Close()
	for key, c := range next {
		if _, err := insCur.Exec(agent, key, c.ID, c.Size, c.Mtime, c.Offset); err != nil {
			return err
		}
	}

	// Drop the resources that disappeared, then any session left without a
	// backing resource. Membership (not a single source_path) decides this, so
	// a session spanning several files survives the loss of just one.
	keys := make([]any, 0, len(next)+1)
	keys = append(keys, agent)
	placeholders := make([]string, 0, len(next))
	for k := range next {
		placeholders = append(placeholders, "?")
		keys = append(keys, k)
	}
	inSrc := ""
	if len(placeholders) > 0 {
		inSrc = ` AND src NOT IN (` + strings.Join(placeholders, ",") + `)`
	}
	if _, err := tx.Exec(`DELETE FROM session_usage WHERE agent = ?`+inSrc, keys...); err != nil {
		return err
	}
	// Histograms of disappeared source files would otherwise leak forever.
	for _, table := range []string{"activity", "tool_use"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE agent = ?`+inSrc, keys...); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM session WHERE agent = ? AND id NOT IN (
		SELECT id FROM session_usage WHERE agent = ?)`, agent, agent); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (ix *Index) loadCursors(agent string) (map[string]source.Cursor, error) {
	rows, err := ix.db.Query(`SELECT key,id,size,mtime,offset FROM cursor WHERE agent = ?`, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]source.Cursor{}
	for rows.Next() {
		var k string
		var c source.Cursor
		if rows.Scan(&k, &c.ID, &c.Size, &c.Mtime, &c.Offset) == nil {
			out[k] = c
		}
	}
	return out, rows.Err()
}
