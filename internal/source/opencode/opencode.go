// Package opencode reads OpenCode history from its SQLite database. The
// database is opened read-only so a running OpenCode instance is never
// disturbed.
//
// Two message layouts exist across OpenCode versions:
//   - current: a single `session_message` table with inline content
//   - legacy:  `message` + `part` tables
//
// Newer sessions use `session_message`; the parser falls back to the legacy
// tables when a session has no rows in the new table.
package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/model"
	"github.com/akisaya/agent-session-viewer/internal/source"

	_ "modernc.org/sqlite"
)

// Source reads the OpenCode SQLite database.
type Source struct {
	Path string // ~/.local/share/opencode/opencode.db
}

func (s *Source) Name() string { return "opencode" }

func (s *Source) open() (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", s.Path)
	return sql.Open("sqlite", dsn)
}

// Scan re-reads session metadata (cheap, indexed queries) and emits
// authoritative absolute values for every session.
func (s *Source) Scan(prev map[string]source.Cursor, emit func(source.Delta)) (map[string]source.Cursor, error) {
	next := map[string]source.Cursor{}
	db, err := s.open()
	if err != nil {
		return next, nil
	}
	defer db.Close()
	if db.Ping() != nil {
		return next, nil
	}

	msgCounts := s.messageCounts(db)
	previews := s.previews(db)
	activity := s.activity(db)
	tools := s.toolCounts(db)

	rows, err := db.Query(`SELECT id, COALESCE(title,''), COALESCE(directory,''), COALESCE(model,''),
		COALESCE(agent,''), COALESCE(tokens_input,0), COALESCE(tokens_output,0),
		COALESCE(tokens_cache_read,0), COALESCE(cost,0), time_created, time_updated,
		COALESCE(parent_id,''), COALESCE(fork_session_id,'') FROM session_v2`)
	if err != nil {
		return next, nil
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, title, dir, modelJSON, agent string
			tin, tout, cache                 int64
			cost                             float64
			created, updated                 int64
			parentID, forkID                 string
		)
		if rows.Scan(&id, &title, &dir, &modelJSON, &agent, &tin, &tout, &cache, &cost,
			&created, &updated, &parentID, &forkID) != nil {
			continue
		}
		// parent_id marks a session spawned as a subagent of another one,
		// fork_session_id a user-made fork. They behave differently: a
		// subagent belongs with its main session, a fork is a new line.
		d := source.Delta{
			Key:       id,
			ID:        id,
			Title:     source.FirstText(title, id),
			Preview:   previews[id],
			CWD:       dir,
			Model:     modelName(modelJSON, agent),
			CreatedAt: time.UnixMilli(created),
			UpdatedAt: time.UnixMilli(updated),
			Msgs:      source.Counter{Replace: true, Value: msgCounts[id]},
			Tokens:    source.Usage{Replace: true, In: tin, Out: tout, Cache: cache},
			Cost:      source.Amount{Replace: true, Value: cost},
			Activity:  source.Histogram{Replace: true, Counts: activity[id]},
			Tools:     source.Histogram{Replace: true, Counts: tools[id]},
			ParentID:  parentID,
			Cursor:    source.Cursor{ID: id},
		}
		switch {
		case parentID != "":
			d.Relation = "subagent"
		case forkID != "":
			d.ParentID = forkID
			d.Relation = "fork"
		}
		emit(d)
		next[id] = source.Cursor{ID: id}
	}
	return next, nil
}

// activity buckets every message of a session into its local calendar day.
func (s *Source) activity(db *sql.DB) map[string]map[string]int {
	out := map[string]map[string]int{}
	rows, err := db.Query(`SELECT session_id,
		substr(datetime(time_created/1000,'unixepoch','localtime'),1,10), count(*)
		FROM session_message WHERE type IN ('user','assistant') GROUP BY session_id, 2`)
	if err == nil {
		s.readDayCounts(rows, out, nil)
	}
	// Legacy sessions live only in the message table.
	rows2, err := db.Query(`SELECT session_id,
		substr(datetime(time_created/1000,'unixepoch','localtime'),1,10), count(*)
		FROM message GROUP BY session_id, 2`)
	if err == nil {
		s.readDayCounts(rows2, out, func(id string) bool { _, seen := out[id]; return !seen })
	}
	return out
}

func (s *Source) readDayCounts(rows *sql.Rows, out map[string]map[string]int, skip func(string) bool) {
	defer rows.Close()
	for rows.Next() {
		var id, day string
		var n int
		if rows.Scan(&id, &day, &n) != nil || day == "" {
			continue
		}
		if skip != nil && !skip(id) {
			continue
		}
		m := out[id]
		if m == nil {
			m = map[string]int{}
			out[id] = m
		}
		m[day] += n
	}
}

// toolCounts counts tool calls per session from assistant content parts.
func (s *Source) toolCounts(db *sql.DB) map[string]map[string]int {
	out := map[string]map[string]int{}
	rows, err := db.Query(`SELECT session_id, data FROM session_message WHERE type = 'assistant'`)
	if err == nil {
		s.readToolRows(rows, out)
	}
	// Legacy sessions keep their tool calls in the part table.
	rows2, err := db.Query(`SELECT session_id, data FROM part
		WHERE session_id NOT IN (SELECT DISTINCT session_id FROM session_message)`)
	if err != nil {
		// session_message is missing entirely: every session is legacy.
		if rowsAll, errAll := db.Query(`SELECT session_id, data FROM part`); errAll == nil {
			rows2 = rowsAll
		}
	}
	if rows2 != nil {
		s.readToolRows(rows2, out)
	}
	return out
}

func (s *Source) readToolRows(rows *sql.Rows, out map[string]map[string]int) {
	defer rows.Close()
	for rows.Next() {
		var id, data string
		if rows.Scan(&id, &data) != nil {
			continue
		}
		for _, name := range toolNamesIn(data) {
			m := out[id]
			if m == nil {
				m = map[string]int{}
				out[id] = m
			}
			m[name]++
		}
	}
}

// toolNamesIn returns the normalized tool names of one message payload. It
// handles both the current shape (a content array) and the legacy part shape.
func toolNamesIn(data string) []string {
	var d struct {
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(data), &d) == nil {
		var out []string
		for _, c := range d.Content {
			if c.Type == "tool" && c.Name != "" {
				out = append(out, source.ToolName(c.Name))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	var p struct {
		Type string `json:"type"`
		Tool string `json:"tool"`
	}
	if json.Unmarshal([]byte(data), &p) == nil && p.Type == "tool" && p.Tool != "" {
		return []string{source.ToolName(p.Tool)}
	}
	return nil
}

// messageCounts counts conversational turns per session, preferring the
// current table.
func (s *Source) messageCounts(db *sql.DB) map[string]int {
	counts := map[string]int{}
	seen := map[string]bool{}
	if rows, err := db.Query(`SELECT session_id, count(*) FROM session_message
		WHERE type IN ('user','assistant') GROUP BY session_id`); err == nil {
		for rows.Next() {
			var id string
			var n int
			if rows.Scan(&id, &n) == nil {
				counts[id] = n
				seen[id] = true
			}
		}
		rows.Close()
	}
	// Legacy fallback for sessions absent from the new table.
	if rows, err := db.Query(`SELECT session_id, count(*) FROM message GROUP BY session_id`); err == nil {
		for rows.Next() {
			var id string
			var n int
			if rows.Scan(&id, &n) == nil && !seen[id] {
				counts[id] = n
			}
		}
		rows.Close()
	}
	return counts
}

// previews returns the first user message text per session.
func (s *Source) previews(db *sql.DB) map[string]string {
	out := map[string]string{}
	rows, err := db.Query(`SELECT session_id, data, min(seq) FROM session_message
		WHERE type = 'user' GROUP BY session_id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, data string
		var seq int
		if rows.Scan(&id, &data, &seq) != nil {
			continue
		}
		var d struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(data), &d) == nil {
			out[id] = source.Truncate(strings.TrimSpace(d.Text), 200)
		}
	}
	return out
}

// Transcript loads a conversation, preferring session_message.
func (s *Source) Transcript(id string) ([]model.Message, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	msgs, err := s.transcriptNew(db, id)
	if err != nil {
		return nil, err
	}
	if len(msgs) > 0 {
		return msgs, nil
	}
	return s.transcriptLegacy(db, id)
}

func (s *Source) transcriptNew(db *sql.DB, id string) ([]model.Message, error) {
	rows, err := db.Query(`SELECT type, data FROM session_message WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Message
	for rows.Next() {
		var typ, data string
		if rows.Scan(&typ, &data) != nil {
			continue
		}
		out = append(out, renderSessionMessages(typ, data)...)
	}
	return out, nil
}

func (s *Source) transcriptLegacy(db *sql.DB, id string) ([]model.Message, error) {
	rows, err := db.Query(`
		SELECT m.id, m.data, p.data
		FROM message m JOIN part p ON p.message_id = m.id
		WHERE m.session_id = ?
		ORDER BY m.time_created, m.id, p.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roles := map[string]string{}
	var out []model.Message
	for rows.Next() {
		var mid, mdata, pdata string
		if rows.Scan(&mid, &mdata, &pdata) != nil {
			continue
		}
		role, ok := roles[mid]
		if !ok {
			role = legacyRole(mdata)
			roles[mid] = role
		}
		text := partText(pdata)
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, model.Message{Role: role, Text: text})
	}
	return out, nil
}

// renderSessionMessages converts a current-format row into one or more
// role-tagged messages.
func renderSessionMessages(typ, data string) []model.Message {
	switch typ {
	case "user":
		var d struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal([]byte(data), &d)
		if t := strings.TrimSpace(d.Text); t != "" {
			return []model.Message{{Role: "user", Text: t}}
		}
	case "assistant":
		var d struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"`
			} `json:"content"`
		}
		if json.Unmarshal([]byte(data), &d) != nil {
			return nil
		}
		var out []model.Message
		for _, c := range d.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(c.Text); t != "" {
					out = append(out, model.Message{Role: "assistant", Text: t})
				}
			case "reasoning":
				if t := strings.TrimSpace(c.Text); t != "" {
					out = append(out, model.Message{Role: "reasoning", Text: t})
				}
			case "tool":
				if c.Name != "" {
					out = append(out, model.Message{Role: "tool", Text: "[tool: " + c.Name + "]"})
				}
			}
		}
		return out
	case "system":
		var d struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal([]byte(data), &d)
		if t := strings.TrimSpace(d.Text); t != "" {
			return []model.Message{{Role: "system", Text: t}}
		}
	}
	return nil
}

func legacyRole(data string) string {
	var m struct {
		Role string `json:"role"`
	}
	if json.Unmarshal([]byte(data), &m) == nil && m.Role != "" {
		return m.Role
	}
	return "assistant"
}

// partText extracts displayable text from a legacy part payload.
func partText(data string) string {
	var p struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Tool string `json:"tool"`
	}
	if json.Unmarshal([]byte(data), &p) != nil {
		return ""
	}
	switch p.Type {
	case "text", "reasoning":
		return strings.TrimSpace(p.Text)
	case "tool":
		if p.Tool != "" {
			return "[tool: " + p.Tool + "]"
		}
	}
	return ""
}

func modelName(modelJSON, agent string) string {
	var m struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
	}
	if json.Unmarshal([]byte(modelJSON), &m) == nil && m.ID != "" {
		if m.ProviderID != "" {
			return m.ProviderID + "/" + m.ID
		}
		return m.ID
	}
	return agent
}
