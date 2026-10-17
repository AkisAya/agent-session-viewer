package index

import (
	"database/sql"

	"github.com/akisaya/agent-session-viewer/internal/source"
)

// row is the metadata half of a session, loaded before a delta is merged in.
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

// writer applies deltas to one scan transaction through reusable prepared
// statements, so a full re-index does not re-parse the SQL for every row.
type writer struct {
	insSession  *sql.Stmt
	getSession  *sql.Stmt
	upUsage     *sql.Stmt
	sumUsage    *sql.Stmt
	delActivity *sql.Stmt
	upActivity  *sql.Stmt
	delTools    *sql.Stmt
	upTools     *sql.Stmt
}

func newWriter(tx *sql.Tx) (*writer, error) {
	w := &writer{}
	preps := []struct {
		dst **sql.Stmt
		q   string
	}{
		{&w.insSession, `INSERT OR REPLACE INTO session
			(agent,id,title,preview,cwd,model,created_at,updated_at,msg_count,tokens_in,tokens_out,cache_read,cost,parent_id,relation)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`},
		{&w.getSession, `SELECT title,preview,cwd,model,created_at,updated_at,parent_id,relation
			FROM session WHERE agent=? AND id=?`},
		{&w.upUsage, `INSERT INTO session_usage
			(agent,id,src,msg_count,tokens_in,tokens_out,cache_read,cost)
			VALUES(?,?,?,?,?,?,?,?)
			ON CONFLICT(agent,id,src) DO UPDATE SET
			  msg_count  = CASE WHEN ? THEN excluded.msg_count  ELSE session_usage.msg_count  + excluded.msg_count  END,
			  tokens_in  = CASE WHEN ? THEN excluded.tokens_in  ELSE session_usage.tokens_in  + excluded.tokens_in  END,
			  tokens_out = CASE WHEN ? THEN excluded.tokens_out ELSE session_usage.tokens_out + excluded.tokens_out END,
			  cache_read = CASE WHEN ? THEN excluded.cache_read ELSE session_usage.cache_read + excluded.cache_read END,
			  cost       = CASE WHEN ? THEN excluded.cost       ELSE session_usage.cost       + excluded.cost       END`},
		{&w.sumUsage, `SELECT COALESCE(sum(msg_count),0), COALESCE(sum(tokens_in),0),
			COALESCE(sum(tokens_out),0), COALESCE(sum(cache_read),0), COALESCE(sum(cost),0)
			FROM session_usage WHERE agent=? AND id=?`},
		{&w.delActivity, `DELETE FROM activity WHERE agent=? AND id=? AND src=?`},
		{&w.upActivity, `INSERT INTO activity(agent,id,src,day,msgs) VALUES(?,?,?,?,?)
			ON CONFLICT(agent,id,src,day) DO UPDATE SET msgs = msgs + excluded.msgs`},
		{&w.delTools, `DELETE FROM tool_use WHERE agent=? AND id=? AND src=?`},
		{&w.upTools, `INSERT INTO tool_use(agent,id,src,tool,cnt) VALUES(?,?,?,?,?)
			ON CONFLICT(agent,id,src,tool) DO UPDATE SET cnt = cnt + excluded.cnt`},
	}
	for _, p := range preps {
		stmt, err := tx.Prepare(p.q)
		if err != nil {
			w.close()
			return nil, err
		}
		*p.dst = stmt
	}
	return w, nil
}

func (w *writer) close() {
	for _, s := range []*sql.Stmt{
		w.insSession, w.getSession, w.upUsage, w.sumUsage,
		w.delActivity, w.upActivity, w.delTools, w.upTools,
	} {
		if s != nil {
			_ = s.Close()
		}
	}
}

// apply folds one delta into every table it touches.
func (w *writer) apply(agent string, d source.Delta) error {
	if err := w.applyUsage(agent, d); err != nil {
		return err
	}
	if err := w.applyActivity(agent, d); err != nil {
		return err
	}
	if err := w.applyTools(agent, d); err != nil {
		return err
	}
	return w.applySession(agent, d)
}

// applyUsage merges one resource's numbers into its session_usage row in a
// single upsert: Replace overwrites that resource's values, otherwise they are
// added (how an append-only source reports the delta of newly read bytes).
func (w *writer) applyUsage(agent string, d source.Delta) error {
	_, err := w.upUsage.Exec(
		agent, d.ID, srcKey(d),
		d.Msgs.Value, d.Tokens.In, d.Tokens.Out, d.Tokens.Cache, d.Cost.Value,
		flag(d.Msgs.Replace), flag(d.Tokens.Replace), flag(d.Tokens.Replace),
		flag(d.Tokens.Replace), flag(d.Cost.Replace))
	return err
}

// applyActivity folds a delta's per-day message histogram into the activity
// table. Histograms are stored per source file (src), so a session spanning
// several files (Codex resumes) accumulates them instead of overwriting them.
// Replace clears the source's own rows first; additive deltas merge.
func (w *writer) applyActivity(agent string, d source.Delta) error {
	if d.Activity.Replace {
		if _, err := w.delActivity.Exec(agent, d.ID, srcKey(d)); err != nil {
			return err
		}
	} else if len(d.Activity.Counts) == 0 {
		return nil
	}
	for day, n := range d.Activity.Counts {
		if day == "" || n <= 0 {
			continue
		}
		if _, err := w.upActivity.Exec(agent, d.ID, srcKey(d), day, n); err != nil {
			return err
		}
	}
	return nil
}

// applyTools folds a delta's tool-call counters into the tool_use table with
// the same per-source replace/additive semantics as applyActivity.
func (w *writer) applyTools(agent string, d source.Delta) error {
	if d.Tools.Replace {
		if _, err := w.delTools.Exec(agent, d.ID, srcKey(d)); err != nil {
			return err
		}
	} else if len(d.Tools.Counts) == 0 {
		return nil
	}
	for tool, n := range d.Tools.Counts {
		if tool == "" || n <= 0 {
			continue
		}
		if _, err := w.upTools.Exec(agent, d.ID, srcKey(d), tool, n); err != nil {
			return err
		}
	}
	return nil
}

// applySession rewrites the session row: metadata is merged with the delta
// (non-empty wins, timestamps expand) and the totals are summed from every
// resource that backs the session.
func (w *writer) applySession(agent string, d source.Delta) error {
	var old row
	err := w.getSession.QueryRow(agent, d.ID).Scan(
		&old.title, &old.preview, &old.cwd, &old.model, &old.created, &old.updated,
		&old.parent, &old.relation)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	var msg int
	var tin, tout, cache int64
	var cost float64
	if err := w.sumUsage.QueryRow(agent, d.ID).Scan(&msg, &tin, &tout, &cache, &cost); err != nil {
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
	if nu := d.UpdatedAt.UnixNano(); nu > old.updated {
		r.updated = nu
	}

	_, err = w.insSession.Exec(
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

func flag(b bool) int {
	if b {
		return 1
	}
	return 0
}

func pick(newV, oldV string) string {
	if newV != "" {
		return newV
	}
	return oldV
}
