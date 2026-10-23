package index

import (
	"errors"
	"sort"

	"github.com/akisaya/agent-session-viewer/internal/model"
	"github.com/akisaya/agent-session-viewer/internal/source"
)

// Live lists the sessions with a running agent process, most urgent first,
// each joined with its cached summary when the index already has one. The
// status itself is never cached: every call reads the sources afresh. A
// failing source does not hide the others; its error is joined and returned
// with the rows that were read.
func (ix *Index) Live() ([]model.LiveSession, error) {
	var (
		rows []model.LiveSession
		errs []error
	)
	for _, src := range ix.sources {
		l, ok := src.(source.Live)
		if !ok {
			continue
		}
		got, err := l.Live()
		if err != nil {
			errs = append(errs, errors.New(src.Name()+": "+err.Error()))
		}
		rows = append(rows, got...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if ri, rj := liveRank(rows[i].Status), liveRank(rows[j].Status); ri != rj {
			return ri < rj
		}
		return rows[i].Since.After(rows[j].Since)
	})
	if err := ix.attachSessions(rows); err != nil {
		errs = append(errs, err)
	}
	return rows, errors.Join(errs...)
}

// liveRank orders statuses by how much they need the user: a session blocked
// on a prompt first, then working ones, then those idle at the input box.
func liveRank(status string) int {
	switch status {
	case "waiting":
		return 0
	case "busy", "shell":
		return 1
	case "idle":
		return 2
	}
	return 3
}

// attachSessions joins the cached summaries onto the live rows, in chunks so
// the OR-pair clause stays small.
func (ix *Index) attachSessions(rows []model.LiveSession) error {
	const chunk = 200
	for start := 0; start < len(rows); start += chunk {
		end := start + chunk
		if end > len(rows) {
			end = len(rows)
		}
		pairs := make([][2]string, 0, end-start)
		for _, r := range rows[start:end] {
			pairs = append(pairs, [2]string{r.Agent, r.ID})
		}
		cond, args := pairClause("agent", "id", pairs)
		found, err := ix.selectSessions(`SELECT `+sessionCols+` FROM session WHERE `+cond, args)
		if err != nil {
			return err
		}
		byKey := make(map[[2]string]*model.Session, len(found))
		for i := range found {
			byKey[[2]string{found[i].Agent, found[i].ID}] = &found[i]
		}
		for i := start; i < end; i++ {
			rows[i].Session = byKey[[2]string{rows[i].Agent, rows[i].ID}]
		}
	}
	return nil
}
