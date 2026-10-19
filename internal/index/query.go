package index

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/model"
)

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

	return ix.selectSessions(`SELECT `+sessionCols+` FROM session WHERE `+
		strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ?`, args)
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
	tokens_in,tokens_out,cache_read,cost,parent_id,relation,compacts`

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
			&s.MsgCount, &s.TokensIn, &s.TokensOut, &s.CacheRead, &s.Cost, &s.ParentID, &s.Relation, &s.Compacts); err != nil {
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
	hints, err := ix.sourcesFor(agent, id)
	if err != nil {
		return nil, err
	}
	return src.Transcript(id, hints)
}

// sourcesFor returns the resources the index recorded for a session, letting a
// source open the transcript without searching the filesystem.
func (ix *Index) sourcesFor(agent, id string) ([]string, error) {
	rows, err := ix.db.Query(`SELECT src FROM session_usage WHERE agent = ? AND id = ?`, agent, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
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
