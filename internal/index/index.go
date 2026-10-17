// Package index maintains a local SQLite cache (on disk, or in memory when
// requested) of session summaries and orchestrates incremental scanning of
// every source.
package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

	for _, d := range deltas {
		if err := applyDelta(tx, agent, d); err != nil {
			return err
		}
		if err := applyActivity(tx, agent, d); err != nil {
			return err
		}
		if err := applyTools(tx, agent, d); err != nil {
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

type row struct {
	title    string
	preview  string
	cwd      string
	model    string
	created  int64
	updated  int64
	parent   string
	relation string
}

func applyDelta(tx *sql.Tx, agent string, d source.Delta) error {
	var old row
	err := tx.QueryRow(`SELECT title,preview,cwd,model,created_at,updated_at,parent_id,relation
		FROM session WHERE agent=? AND id=?`, agent, d.ID).Scan(
		&old.title, &old.preview, &old.cwd, &old.model, &old.created, &old.updated,
		&old.parent, &old.relation)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	// Fold this resource's contribution, then derive the session totals from
	// every resource that backs it.
	if err := applyUsage(tx, agent, d.ID, srcKey(d), d); err != nil {
		return err
	}
	var msg int
	var tin, tout, cache int64
	var cost float64
	if err := tx.QueryRow(`SELECT COALESCE(sum(msg_count),0), COALESCE(sum(tokens_in),0),
		COALESCE(sum(tokens_out),0), COALESCE(sum(cache_read),0), COALESCE(sum(cost),0)
		FROM session_usage WHERE agent=? AND id=?`, agent, d.ID).
		Scan(&msg, &tin, &tout, &cache, &cost); err != nil {
		return err
	}

	r := old
	r.title = pick(d.Title, old.title)
	r.preview = pick(d.Preview, old.preview)
	r.cwd = pick(d.CWD, old.cwd)
	r.model = pick(d.Model, old.model)
	r.parent = pick(d.ParentID, old.parent)
	r.relation = pick(d.Relation, old.relation)

	nc := d.CreatedAt.UnixNano()
	switch {
	case old.created == 0:
		r.created = nc
	case nc != 0 && nc < old.created:
		r.created = nc
	}
	nu := d.UpdatedAt.UnixNano()
	if nu > old.updated {
		r.updated = nu
	}

	_, err = tx.Exec(`INSERT OR REPLACE INTO session
		(agent,id,title,preview,cwd,model,created_at,updated_at,msg_count,tokens_in,tokens_out,cache_read,cost,parent_id,relation)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		agent, d.ID, r.title, r.preview, r.cwd, r.model, r.created, r.updated,
		msg, tin, tout, cache, cost, r.parent, r.relation)
	return err
}

// srcKey identifies the resource a delta describes, falling back to the
// session id for sources that do not expose a file path.
func srcKey(d source.Delta) string {
	if d.Key != "" {
		return d.Key
	}
	return d.ID
}

// applyUsage folds one resource's numbers into its session_usage row. Replace
// overwrites that resource's values; otherwise they are added, which is how an
// append-only source reports the delta of the newly read bytes.
func applyUsage(tx *sql.Tx, agent, id, src string, d source.Delta) error {
	var msg int
	var tin, tout, cache int64
	var cost float64
	err := tx.QueryRow(`SELECT msg_count,tokens_in,tokens_out,cache_read,cost
		FROM session_usage WHERE agent=? AND id=? AND src=?`, agent, id, src).
		Scan(&msg, &tin, &tout, &cache, &cost)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if d.Msgs.Replace {
		msg = d.Msgs.Value
	} else {
		msg += d.Msgs.Value
	}
	if d.Tokens.Replace {
		tin, tout, cache = d.Tokens.In, d.Tokens.Out, d.Tokens.Cache
	} else {
		tin, tout, cache = tin+d.Tokens.In, tout+d.Tokens.Out, cache+d.Tokens.Cache
	}
	if d.Cost.Replace {
		cost = d.Cost.Value
	} else {
		cost += d.Cost.Value
	}
	_, err = tx.Exec(`INSERT OR REPLACE INTO session_usage
		(agent,id,src,msg_count,tokens_in,tokens_out,cache_read,cost)
		VALUES(?,?,?,?,?,?,?,?)`, agent, id, src, msg, tin, tout, cache, cost)
	return err
}

// applyActivity folds a delta's per-day message histogram into the activity
// table. Histograms are stored per source file (src), so a session spanning
// several files (Codex resumes) accumulates them instead of overwriting them.
// Absolute deltas replace the rows of their own source, additive ones merge
// (incremental scans only parse newly appended bytes).
func applyActivity(tx *sql.Tx, agent string, d source.Delta) error {
	if d.Activity.Replace {
		if _, err := tx.Exec(`DELETE FROM activity WHERE agent=? AND id=? AND src=?`, agent, d.ID, srcKey(d)); err != nil {
			return err
		}
	} else if len(d.Activity.Counts) == 0 {
		return nil
	}
	for day, n := range d.Activity.Counts {
		if day == "" || n <= 0 {
			continue
		}
		const upsert = `INSERT INTO activity(agent,id,src,day,msgs) VALUES(?,?,?,?,?)
			ON CONFLICT(agent,id,src,day) DO UPDATE SET msgs = msgs + excluded.msgs`
		if _, err := tx.Exec(upsert, agent, d.ID, srcKey(d), day, n); err != nil {
			return err
		}
	}
	return nil
}

// applyTools folds a delta's tool-call counters into the tool_use table with
// the same per-source absolute/additive semantics as applyActivity.
func applyTools(tx *sql.Tx, agent string, d source.Delta) error {
	if d.Tools.Replace {
		if _, err := tx.Exec(`DELETE FROM tool_use WHERE agent=? AND id=? AND src=?`, agent, d.ID, srcKey(d)); err != nil {
			return err
		}
	} else if len(d.Tools.Counts) == 0 {
		return nil
	}
	for tool, n := range d.Tools.Counts {
		if tool == "" || n <= 0 {
			continue
		}
		const upsert = `INSERT INTO tool_use(agent,id,src,tool,cnt) VALUES(?,?,?,?,?)
			ON CONFLICT(agent,id,src,tool) DO UPDATE SET cnt = cnt + excluded.cnt`
		if _, err := tx.Exec(upsert, agent, d.ID, srcKey(d), tool, n); err != nil {
			return err
		}
	}
	return nil
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
	Day   string // only sessions with activity on this local day (YYYY-MM-DD)
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
	if f.Day != "" {
		// A session matches a day when its per-day histogram has a row for it.
		where = append(where, "id IN (SELECT a.id FROM activity a WHERE a.day = ? AND a.agent = session.agent)")
		args = append(args, f.Day)
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

	q := `SELECT agent,id,title,preview,cwd,model,created_at,updated_at,msg_count,tokens_in,tokens_out,cache_read,cost,parent_id,relation
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
			&s.MsgCount, &s.TokensIn, &s.TokensOut, &s.CacheRead, &s.Cost, &s.ParentID, &s.Relation); err != nil {
			return nil, err
		}
		s.CreatedAt = unixNano(created)
		s.UpdatedAt = unixNano(updated)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Stats aggregates per-agent and total metrics.
//
// The per-agent breakdown is scoped by directory only (so the agent tabs can
// show how many sessions each agent has inside the current project), while the
// total is scoped by both agent and directory to mirror the active filter.
func (ix *Index) Stats(agent, cwd, day string) (model.Stats, error) {
	var st model.Stats

	dayCond := ""
	hArgs := []any{}
	if day != "" {
		dayCond = " AND s.id IN (SELECT a.id FROM activity a WHERE a.day = ? AND a.agent = s.agent)"
		hArgs = append(hArgs, day)
	}

	where, args := scopeWhere("", cwd)
	where += dayCond
	args = append(args, hArgs...)
	rows, err := ix.db.Query(`SELECT agent, count(*), COALESCE(sum(msg_count),0),
		COALESCE(sum(tokens_in),0), COALESCE(sum(tokens_out),0), COALESCE(sum(cache_read),0), COALESCE(sum(cost),0)
		FROM session s WHERE `+where+` GROUP BY agent ORDER BY agent`, args...)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var a model.AgentStat
		if err := rows.Scan(&a.Agent, &a.Sessions, &a.Messages, &a.TokensIn, &a.TokensOut, &a.CacheRead, &a.Cost); err != nil {
			return st, err
		}
		st.Agents = append(st.Agents, a)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}

	tw, targs := scopeWhere(agent, cwd)
	tw += dayCond
	targs = append(targs, hArgs...)
	var t model.AgentStat
	if err := ix.db.QueryRow(`SELECT count(*), COALESCE(sum(msg_count),0),
		COALESCE(sum(tokens_in),0), COALESCE(sum(tokens_out),0), COALESCE(sum(cache_read),0), COALESCE(sum(cost),0)
		FROM session s WHERE `+tw, targs...).Scan(
		&t.Sessions, &t.Messages, &t.TokensIn, &t.TokensOut, &t.CacheRead, &t.Cost); err != nil {
		return st, err
	}
	t.Agent = "total"
	st.Total = t
	return st, nil
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
	rows, err := ix.db.Query(`SELECT cwd, agent, count(*), COALESCE(sum(msg_count),0),
		COALESCE(sum(tokens_in+tokens_out+cache_read),0), COALESCE(sum(cost),0), max(updated_at)
		FROM session WHERE `+where+` GROUP BY cwd, agent`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byCWD := map[string]*model.Project{}
	var order []string
	for rows.Next() {
		var cwd, ag string
		var count, msgs int
		var tokens int64
		var cost float64
		var updated int64
		if err := rows.Scan(&cwd, &ag, &count, &msgs, &tokens, &cost, &updated); err != nil {
			return nil, err
		}
		p := byCWD[cwd]
		if p == nil {
			p = &model.Project{CWD: cwd, Agents: map[string]int{}}
			byCWD[cwd] = p
			order = append(order, cwd)
		}
		p.Count += count
		p.Messages += msgs
		p.Tokens += tokens
		p.Cost += cost
		p.Agents[ag] += count
		if t := unixNano(updated); t.After(p.UpdatedAt) {
			p.UpdatedAt = t
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]model.Project, 0, len(order))
	for _, cwd := range order {
		out = append(out, *byCWD[cwd])
	}
	// Deterministic order: busiest first, then most recently active, then by
	// path. Without this the group-by scan order leaked into the sidebar.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].CWD < out[j].CWD
	})
	return out, nil
}

// Activity returns message counts per local calendar day, optionally scoped
// to an agent and/or working directory.
func (ix *Index) Activity(agent, cwd string) ([]model.ActivityDay, error) {
	where, args := scopeWhere(agent, cwd)
	rows, err := ix.db.Query(`SELECT a.day, COALESCE(sum(a.msgs),0), count(DISTINCT a.id)
		FROM activity a JOIN session s ON s.agent = a.agent AND s.id = a.id
		WHERE `+where+` GROUP BY a.day ORDER BY a.day`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ActivityDay
	for rows.Next() {
		var d model.ActivityDay
		if err := rows.Scan(&d.Day, &d.Msgs, &d.Sessions); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Trend returns per-day activity with a pro-rated share of each session's
// tokens and cost (distributed across the days that session was active,
// proportional to that day's message count). days <= 0 returns every day.
func (ix *Index) Trend(agent, cwd string, days int) ([]model.TrendPoint, error) {
	where, args := scopeWhere(agent, cwd)
	if days > 0 {
		where += ` AND a.day >= date('now','localtime', ?)`
		args = append(args, fmt.Sprintf("-%d day", days-1))
	}
	rows, err := ix.db.Query(`SELECT a.day, a.msgs,
			sum(a.msgs) OVER (PARTITION BY a.agent, a.id) AS sess_msgs,
			s.cost, s.tokens_in + s.tokens_out + s.cache_read AS sess_tokens, a.id
		FROM activity a JOIN session s ON s.agent = a.agent AND s.id = a.id
		WHERE `+where+` ORDER BY a.day`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type acc struct {
		msgs, tokens int64
		cost         float64
		sessions     map[string]struct{}
	}
	byDay := map[string]*acc{}
	var order []string
	for rows.Next() {
		var day, id string
		var msgs, sessMsgs, sessTokens int64
		var cost float64
		if err := rows.Scan(&day, &msgs, &sessMsgs, &cost, &sessTokens, &id); err != nil {
			return nil, err
		}
		a := byDay[day]
		if a == nil {
			a = &acc{sessions: map[string]struct{}{}}
			byDay[day] = a
			order = append(order, day)
		}
		a.msgs += msgs
		if sessMsgs > 0 {
			share := float64(msgs) / float64(sessMsgs)
			a.tokens += int64(float64(sessTokens) * share)
			a.cost += cost * share
		}
		a.sessions[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]model.TrendPoint, 0, len(order))
	for _, day := range order {
		a := byDay[day]
		out = append(out, model.TrendPoint{
			Day:      day,
			Msgs:     a.msgs,
			Sessions: len(a.sessions),
			Tokens:   a.tokens,
			Cost:     a.cost,
		})
	}
	return out, nil
}

// Models aggregates usage per model name within the given scope. Sessions with
// no recorded model are grouped under an empty name.
func (ix *Index) Models(agent, cwd string) ([]model.ModelStat, error) {
	where, args := scopeWhere(agent, cwd)
	rows, err := ix.db.Query(`SELECT model, count(*), COALESCE(sum(msg_count),0),
			COALESCE(sum(tokens_in+tokens_out+cache_read),0), COALESCE(sum(cost),0)
		FROM session s WHERE `+where+` GROUP BY model
		ORDER BY count(*) DESC, sum(msg_count) DESC, model`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ModelStat
	for rows.Next() {
		var m model.ModelStat
		if err := rows.Scan(&m.Model, &m.Sessions, &m.Messages, &m.Tokens, &m.Cost); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Tools returns the most used tools together with the untruncated total call
// count, so shares can be computed correctly on the client.
func (ix *Index) Tools(agent, cwd string, limit int) (model.ToolStats, error) {
	where, args := scopeWhere(agent, cwd)
	if limit <= 0 {
		limit = 20
	}
	var st model.ToolStats
	if err := ix.db.QueryRow(`SELECT COALESCE(sum(t.cnt),0)
		FROM tool_use t JOIN session s ON s.agent = t.agent AND s.id = t.id WHERE `+where,
		args...).Scan(&st.Total); err != nil {
		return st, err
	}

	qArgs := append(append([]any{}, args...), limit)
	rows, err := ix.db.Query(`SELECT t.tool, sum(t.cnt), count(DISTINCT t.id)
		FROM tool_use t JOIN session s ON s.agent = t.agent AND s.id = t.id
		WHERE `+where+` GROUP BY t.tool ORDER BY sum(t.cnt) DESC LIMIT ?`, qArgs...)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var t model.ToolStat
		if err := rows.Scan(&t.Tool, &t.Count, &t.Sessions); err != nil {
			return st, err
		}
		st.Tools = append(st.Tools, t)
	}
	return st, rows.Err()
}

// Relations returns every session involved in a parent/child link inside the
// given scope: the matching sessions, their parents and their children. That
// is enough for the client to assemble a forest.
func (ix *Index) Relations(agent, cwd string) ([]model.Session, error) {
	where, args := scopeWhere(agent, cwd)
	// Only sessions that take part in a link: those carrying a parent, and
	// those referenced as a parent by another session of the same agent.
	// Session identity is (agent, id), so every lookup below matches on the
	// pair -- otherwise an id reused by another agent would cross-link.
	const linked = ` AND (s.parent_id <> '' OR EXISTS (
		SELECT 1 FROM session c WHERE c.agent = s.agent AND c.parent_id = s.id))`
	base, err := ix.selectSessions(`SELECT `+sessionCols+` FROM session s WHERE `+where+linked, args)
	if err != nil || len(base) == 0 {
		return base, err
	}

	seen := map[string]bool{}
	out := append([]model.Session{}, base...)
	for _, s := range base {
		seen[s.Agent+"|"+s.ID] = true
	}

	// Parents referenced by the matched set, and children pointing back at it.
	var parents, children [][2]string
	for _, s := range base {
		if s.ParentID != "" {
			parents = append(parents, [2]string{s.Agent, s.ParentID})
		}
		children = append(children, [2]string{s.Agent, s.ID})
	}

	fill := func(cond string, condArgs []any) error {
		return ix.eachSession(`SELECT `+sessionCols+` FROM session WHERE `+cond, condArgs, func(s model.Session) {
			k := s.Agent + "|" + s.ID
			if !seen[k] {
				seen[k] = true
				out = append(out, s)
			}
		})
	}
	if len(parents) > 0 {
		cond, condArgs := pairClause("agent", "id", parents)
		if err := fill(cond, condArgs); err != nil {
			return nil, err
		}
	}
	cond, condArgs := pairClause("agent", "parent_id", children)
	if err := fill(cond, condArgs); err != nil {
		return nil, err
	}
	return out, nil
}

// pairClause builds "(lhs = ? AND rhs = ?) OR (...)" for a set of
// (agent, id) pairs, deduplicated so the clause stays small.
func pairClause(lhs, rhs string, pairs [][2]string) (string, []any) {
	seen := map[[2]string]bool{}
	conds := make([]string, 0, len(pairs))
	args := make([]any, 0, len(pairs)*2)
	for _, p := range pairs {
		if seen[p] {
			continue
		}
		seen[p] = true
		conds = append(conds, "("+lhs+" = ? AND "+rhs+" = ?)")
		args = append(args, p[0], p[1])
	}
	return strings.Join(conds, " OR "), args
}

// sessionCols is the column list behind model.Session.
const sessionCols = `agent,id,title,preview,cwd,model,created_at,updated_at,msg_count,
	tokens_in,tokens_out,cache_read,cost,parent_id,relation`

func (ix *Index) selectSessions(q string, args []any) ([]model.Session, error) {
	var out []model.Session
	err := ix.eachSession(q, args, func(s model.Session) { out = append(out, s) })
	return out, err
}

func (ix *Index) eachSession(q string, args []any, fn func(model.Session)) error {
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s model.Session
		var created, updated int64
		if err := rows.Scan(&s.Agent, &s.ID, &s.Title, &s.Preview, &s.CWD, &s.Model, &created, &updated,
			&s.MsgCount, &s.TokensIn, &s.TokensOut, &s.CacheRead, &s.Cost, &s.ParentID, &s.Relation); err != nil {
			return err
		}
		s.CreatedAt = unixNano(created)
		s.UpdatedAt = unixNano(updated)
		fn(s)
	}
	return rows.Err()
}

// scopeWhere builds the shared WHERE clause for the scoped aggregate
// endpoints. cwd "__none__" selects sessions without a directory.
func scopeWhere(agent, cwd string) (string, []any) {
	where := []string{"1=1"}
	args := []any{}
	if agent != "" {
		where = append(where, "s.agent = ?")
		args = append(args, agent)
	}
	switch cwd {
	case "":
	case "__none__":
		where = append(where, "s.cwd = ''")
	default:
		where = append(where, "s.cwd = ?")
		args = append(args, cwd)
	}
	return strings.Join(where, " AND "), args
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
