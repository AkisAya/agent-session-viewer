package opencode

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/akisaya/agent-session-viewer/internal/source"
)

// buildDB creates a minimal OpenCode-shaped database with one current-format
// session so Scan and Transcript can be exercised end to end.
func buildDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE session_v2 (
			id TEXT PRIMARY KEY, title TEXT, directory TEXT, model TEXT, agent TEXT,
			tokens_input INTEGER, tokens_output INTEGER, tokens_cache_read INTEGER,
			cost REAL, time_created INTEGER, time_updated INTEGER,
			parent_id TEXT, fork_session_id TEXT)`,
		`CREATE TABLE session_message (
			session_id TEXT, type TEXT, data TEXT, seq INTEGER, time_created INTEGER)`,
		`INSERT INTO session_v2 VALUES ('s1','OC session','/tmp/oc',
			'{"id":"gpt-x","providerID":"openai"}','build',30,40,5,0.25,
			1735725600000,1735729200000,'','')`,
		`INSERT INTO session_message VALUES ('s1','user','{"text":"hello oc"}',1,1735725600000)`,
		`INSERT INTO session_message VALUES ('s1','assistant',
			'{"content":[{"type":"text","text":"hi"},{"type":"tool","name":"shell"},{"type":"reasoning","text":"think"}]}',
			2,1735725605000)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	return path
}

func TestScan(t *testing.T) {
	src := &Source{Path: buildDB(t)}
	var deltas []source.Delta
	next, err := src.Scan(nil, func(d source.Delta) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, ok := next["s1"]; !ok {
		t.Fatalf("cursor for s1 missing: %+v", next)
	}
	if len(deltas) != 1 {
		t.Fatalf("got %d deltas, want 1", len(deltas))
	}
	d := deltas[0]
	if d.ID != "s1" || d.Title != "OC session" || d.CWD != "/tmp/oc" {
		t.Fatalf("delta identity = %+v", d)
	}
	if d.Model != "openai/gpt-x" {
		t.Fatalf("model = %q, want openai/gpt-x", d.Model)
	}
	if d.Preview != "hello oc" {
		t.Fatalf("preview = %q", d.Preview)
	}
	if d.MsgCount != 2 || d.TokensIn != 30 || d.TokensOut != 40 || d.CacheRead != 5 || d.Cost != 0.25 {
		t.Fatalf("numbers = %+v", d)
	}
	if !d.MsgAbsolute || !d.TokensAbsolute || !d.CostAbsolute {
		t.Fatalf("OpenCode deltas must be absolute: %+v", d)
	}
	if d.Tools["shell"] != 1 {
		t.Fatalf("tools = %+v, want shell=1", d.Tools)
	}
}

func TestTranscript(t *testing.T) {
	src := &Source{Path: buildDB(t)}
	msgs, err := src.Transcript("s1")
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	type want struct{ role, text string }
	wants := []want{
		{"user", "hello oc"},
		{"assistant", "hi"},
		{"tool", "[tool: shell]"},
		{"reasoning", "think"},
	}
	if len(msgs) != len(wants) {
		t.Fatalf("got %d messages, want %d: %+v", len(msgs), len(wants), msgs)
	}
	for i, w := range wants {
		if msgs[i].Role != w.role || msgs[i].Text != w.text {
			t.Fatalf("msg[%d] = %+v, want %+v", i, msgs[i], w)
		}
	}
}
