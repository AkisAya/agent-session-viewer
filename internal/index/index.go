// Package index maintains a local SQLite cache of session summaries and
// orchestrates incremental scanning of every source.
package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/model"
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
  source_path TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (agent, id)
);
CREATE INDEX IF NOT EXISTS session_updated_idx ON session(updated_at DESC);
CREATE INDEX IF NOT EXISTS session_agent_idx ON session(agent);

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
`

// Open creates/opens the cache database and registers the sources.
func Open(path string, sources ...source.Source) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // serialize writes; reads are cheap
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	// Migration for cache databases created before the preview column existed.
	_, _ = db.Exec(`ALTER TABLE session ADD COLUMN preview TEXT NOT NULL DEFAULT ''`)

	// Bump when the parsing/extraction changes in a way that requires a full
	// re-scan (cursors are invalidated so every source is re-read once).
	const schemaVersion = "2"
	var ver string
	_ = db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&ver)
	if ver != schemaVersion {
		_, _ = db.Exec(`DELETE FROM cursor`)
		_, _ = db.Exec(`DELETE FROM session`)
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

	for _, d := range deltas {
		if err := applyDelta(tx, agent, d); err != nil {
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

	keys := make([]any, 0, len(next)+1)
	keys = append(keys, agent)
	placeholders := make([]string, 0, len(next))
	for k := range next {
		placeholders = append(placeholders, "?")
		keys = append(keys, k)
	}
	q := `DELETE FROM session WHERE agent = ?`
	if len(placeholders) > 0 {
		q += ` AND source_path NOT IN (` + strings.Join(placeholders, ",") + `)`
	}
	if _, err := tx.Exec(q, keys...); err != nil {
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

type row struct {
	exists  bool
	msg     int
	tin     int64
	tout    int64
	cache   int64
	cost    float64
	created int64
	updated int64
	title   string
	preview string
	cwd     string
	model   string
}

func applyDelta(tx *sql.Tx, agent string, d source.Delta) error {
	var old row
	err := tx.QueryRow(`SELECT title,preview,cwd,model,created_at,updated_at,msg_count,tokens_in,tokens_out,cache_read,cost
		FROM session WHERE agent=? AND id=?`, agent, d.ID).Scan(
		&old.title, &old.preview, &old.cwd, &old.model, &old.created, &old.updated,
		&old.msg, &old.tin, &old.tout, &old.cache, &old.cost)
	if err == nil {
		old.exists = true
	} else if err != sql.ErrNoRows {
		return err
	}

	r := old
	if d.MsgAbsolute {
		r.msg = d.MsgCount
	} else {
		r.msg = old.msg + d.MsgDelta
	}
	if d.TokensAbsolute {
		r.tin, r.tout, r.cache = d.TokensIn, d.TokensOut, d.CacheRead
	} else {
		r.tin, r.tout, r.cache = old.tin+d.TokensIn, old.tout+d.TokensOut, old.cache+d.CacheRead
	}
	if d.CostAbsolute {
		r.cost = d.Cost
	} else {
		r.cost = old.cost + d.Cost
	}

	r.title = pick(d.Title, old.title)
	r.preview = pick(d.Preview, old.preview)
	r.cwd = pick(d.CWD, old.cwd)
	r.model = pick(d.Model, old.model)

	nc := d.CreatedAt.UnixNano()
	switch {
	case old.created == 0:
		r.created = nc
	case nc != 0 && nc < old.created:
		r.created = nc
	default:
		r.created = old.created
	}
	nu := d.UpdatedAt.UnixNano()
	if nu > old.updated {
		r.updated = nu
	} else {
		r.updated = old.updated
	}

	_, err = tx.Exec(`INSERT OR REPLACE INTO session
		(agent,id,title,preview,cwd,model,created_at,updated_at,msg_count,tokens_in,tokens_out,cache_read,cost,source_path)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		agent, d.ID, r.title, r.preview, r.cwd, r.model, r.created, r.updated,
		r.msg, r.tin, r.tout, r.cache, r.cost, d.Key)
	return err
}

func pick(newV, oldV string) string {
	if newV != "" {
		return newV
	}
	return oldV
}

// Filter controls session listing.
type Filter struct {
	Agent string
	CWD   string // exact directory match; "__none__" selects sessions without a directory
	Query string
	Sort  string // "updated" (default) | "created" | "messages" | "tokens"
	Limit int
}

// Sessions returns cached session summaries.
func (ix *Index) Sessions(f Filter) ([]model.Session, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.Agent != "" {
		where = append(where, "agent = ?")
		args = append(args, f.Agent)
	}
	switch f.CWD {
	case "":
		// no directory filter
	case "__none__":
		where = append(where, "cwd = ''")
	default:
		where = append(where, "cwd = ?")
		args = append(args, f.CWD)
	}
	if f.Query != "" {
		where = append(where, "(title LIKE ? OR cwd LIKE ? OR preview LIKE ?)")
		like := "%" + f.Query + "%"
		args = append(args, like, like, like)
	}
	order := "updated_at DESC"
	switch f.Sort {
	case "created":
		order = "created_at DESC"
	case "messages":
		order = "msg_count DESC"
	case "tokens":
		order = "(tokens_in+tokens_out+cache_read) DESC"
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 500
	}
	args = append(args, limit)

	q := `SELECT agent,id,title,preview,cwd,model,created_at,updated_at,msg_count,tokens_in,tokens_out,cache_read,cost
		FROM session WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + order + ` LIMIT ?`
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Session
	for rows.Next() {
		var s model.Session
		var created, updated int64
		if err := rows.Scan(&s.Agent, &s.ID, &s.Title, &s.Preview, &s.CWD, &s.Model, &created, &updated,
			&s.MsgCount, &s.TokensIn, &s.TokensOut, &s.CacheRead, &s.Cost); err != nil {
			return nil, err
		}
		s.CreatedAt = unixNano(created)
		s.UpdatedAt = unixNano(updated)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Stats aggregates per-agent and total metrics.
func (ix *Index) Stats() (model.Stats, error) {
	rows, err := ix.db.Query(`SELECT agent, count(*), COALESCE(sum(msg_count),0),
		COALESCE(sum(tokens_in),0), COALESCE(sum(tokens_out),0), COALESCE(sum(cache_read),0), COALESCE(sum(cost),0)
		FROM session GROUP BY agent ORDER BY agent`)
	if err != nil {
		return model.Stats{}, err
	}
	defer rows.Close()

	var st model.Stats
	for rows.Next() {
		var a model.AgentStat
		if err := rows.Scan(&a.Agent, &a.Sessions, &a.Messages, &a.TokensIn, &a.TokensOut, &a.CacheRead, &a.Cost); err != nil {
			return st, err
		}
		st.Agents = append(st.Agents, a)
		st.Total.Sessions += a.Sessions
		st.Total.Messages += a.Messages
		st.Total.TokensIn += a.TokensIn
		st.Total.TokensOut += a.TokensOut
		st.Total.CacheRead += a.CacheRead
		st.Total.Cost += a.Cost
	}
	st.Total.Agent = "total"
	return st, rows.Err()
}

// Projects aggregates sessions by working directory, optionally scoped to an
// agent.
func (ix *Index) Projects(agent string) ([]model.Project, error) {
	where := "1=1"
	args := []any{}
	if agent != "" {
		where = "agent = ?"
		args = append(args, agent)
	}
	rows, err := ix.db.Query(`SELECT cwd, count(*), COALESCE(sum(msg_count),0),
		COALESCE(sum(tokens_in+tokens_out+cache_read),0), COALESCE(max(updated_at),0)
		FROM session WHERE `+where+` GROUP BY cwd ORDER BY max(updated_at) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Project
	for rows.Next() {
		var p model.Project
		var updated int64
		if err := rows.Scan(&p.CWD, &p.Count, &p.Messages, &p.Tokens, &updated); err != nil {
			return nil, err
		}
		p.UpdatedAt = unixNano(updated)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Transcript loads a full conversation on demand from the owning source.
func (ix *Index) Transcript(agent, id string) ([]model.Message, error) {
	src, ok := ix.byName[agent]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", agent)
	}
	return src.Transcript(id)
}

// LastScan reports when the last successful scan finished.
func (ix *Index) LastScan() time.Time {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.lastScan
}

func unixNano(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}
