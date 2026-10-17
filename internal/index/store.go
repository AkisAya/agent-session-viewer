package index

import (
	"database/sql"

	"github.com/akisaya/agent-session-viewer/internal/source"
)

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
