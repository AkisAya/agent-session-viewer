// Package index maintains a local SQLite cache (on disk, or in memory when
// requested) of session summaries and orchestrates incremental scanning of
// every source.
package index

import (
	"database/sql"
	"errors"
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

	mu            sync.Mutex
	scanning      bool
	currentSource string
	scanDone      int
	scanTotal     int
	lastScan      time.Time
	lastError     string
}

// ScanStatus reports the progress of the current (or last) scan.
type ScanStatus struct {
	Scanning  bool      `json:"scanning"`
	Source    string    `json:"source,omitempty"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	LastScan  time.Time `json:"lastScan"`
	LastError string    `json:"lastError,omitempty"`
}

// activityHourSchema is split out so the migration can recreate the table:
// v12 added the day column, which CREATE TABLE IF NOT EXISTS cannot alter.
const activityHourSchema = `
CREATE TABLE IF NOT EXISTS activity_hour (
  agent TEXT NOT NULL,
  id    TEXT NOT NULL,
  src   TEXT NOT NULL,
  day   TEXT NOT NULL,
  hour  INTEGER NOT NULL,
  msgs  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (agent, id, src, day, hour)
);`

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
  compacts    INTEGER NOT NULL DEFAULT 0,
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
  compacts   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (agent, id, src)
);
CREATE INDEX IF NOT EXISTS session_usage_src_idx ON session_usage(agent, src);
` + activityHourSchema

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
	// WAL lets readers run while a scan writes, so a file-backed index uses a
	// small pool of connections. The shared in-memory database needs a single
	// connection: it is the one that keeps the database alive.
	if opts.Memory {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(4)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	// Migration for cache databases created before these columns existed.
	_, _ = db.Exec(`ALTER TABLE session ADD COLUMN preview TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE session ADD COLUMN parent_id TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE session ADD COLUMN relation TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE session ADD COLUMN compacts INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE session_usage ADD COLUMN compacts INTEGER NOT NULL DEFAULT 0`)

	// Bump when the parsing/extraction changes in a way that requires a full
	// re-scan (cursors are invalidated so every source is re-read once).
	// v4: activity/tool histograms + parent links; v5: MultiEdit alias fix;
	// v6: OpenCode parent_id relabelled as "subagent" (was "fork");
	// v7: session_source membership table; v8: per-source usage table
	// (session totals are summed from their resources); v9: Claude usage
	// deduped per message id, Codex input excludes cached tokens; v10:
	// per-session compaction count, Claude input includes cache writes;
	// v11: per-hour activity histogram for the work-rhythm chart;
	// v12: per-day hour histogram, so "today by hour" can be scoped.
	const schemaVersion = "12"
	var ver string
	_ = db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&ver)
	if ver != schemaVersion {
		_, _ = db.Exec(`DELETE FROM cursor`)
		_, _ = db.Exec(`DELETE FROM session`)
		_, _ = db.Exec(`DELETE FROM activity`)
		// The v12 shape adds a column, so drop and recreate this table.
		_, _ = db.Exec(`DROP TABLE IF EXISTS activity_hour`)
		_, _ = db.Exec(activityHourSchema)
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

// HasSource reports whether an agent name is registered.
func (ix *Index) HasSource(name string) bool {
	_, ok := ix.byName[name]
	return ok
}

// Scan performs an incremental scan of every source. It is safe to call
// concurrently; overlapping calls are skipped. A failing source does not stop
// the remaining sources; the joined error is returned at the end and the
// successful sources are still committed.
// Scan performs an incremental scan of every source. It is safe to call
// concurrently; overlapping calls are skipped. A failing source does not stop
// the remaining sources; the joined error is returned at the end and the
// successful sources are still committed.
func (ix *Index) Scan() error {
	if !ix.beginScan() {
		return nil
	}
	return ix.runScan()
}

// StartScan reserves the scan state synchronously, then runs the scan in the
// background. The returned channel receives the result once it finishes. This
// makes /api/scan report scanning=true immediately at startup, before the
// worker goroutine has had a chance to run.
func (ix *Index) StartScan() <-chan error {
	ch := make(chan error, 1)
	if !ix.beginScan() {
		ch <- nil
		close(ch)
		return ch
	}
	go func() {
		ch <- ix.runScan()
		close(ch)
	}()
	return ch
}

func (ix *Index) beginScan() bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.scanning {
		return false
	}
	ix.scanning = true
	ix.currentSource = ""
	ix.scanDone = 0
	ix.scanTotal = len(ix.sources)
	ix.lastError = ""
	return true
}

func (ix *Index) runScan() error {
	var errs []string
	for i, src := range ix.sources {
		ix.mu.Lock()
		ix.currentSource = src.Name()
		ix.scanDone = i
		ix.mu.Unlock()
		if err := ix.scanSource(src); err != nil {
			errs = append(errs, src.Name()+": "+err.Error())
		}
	}

	ix.mu.Lock()
	ix.scanDone = len(ix.sources)
	ix.currentSource = ""
	ix.scanning = false
	if len(errs) == 0 {
		// A partially failed scan must not claim to be up to date.
		ix.lastScan = time.Now()
		ix.lastError = ""
	} else {
		ix.lastError = strings.Join(errs, "; ")
	}
	msg := ix.lastError
	ix.mu.Unlock()

	if len(errs) > 0 {
		return errors.New(msg)
	}
	return nil
}

// Status returns a snapshot of the scan state for the HTTP API.
func (ix *Index) Status() ScanStatus {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ScanStatus{
		Scanning:  ix.scanning,
		Source:    ix.currentSource,
		Done:      ix.scanDone,
		Total:     ix.scanTotal,
		LastScan:  ix.lastScan,
		LastError: ix.lastError,
	}
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
	for _, table := range []string{"activity", "activity_hour", "tool_use"} {
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
