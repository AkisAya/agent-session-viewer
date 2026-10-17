package opencode

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/akisaya/agent-session-viewer/internal/source"
	"github.com/akisaya/agent-session-viewer/internal/sqlite"
)

// buildDB creates a minimal OpenCode-shaped database with one current-format
// session so Scan and Transcript can be exercised end to end.
//
// The database is built with the platform's own driver, so this only runs
// natively: under js/wasm there is no way to *create* a database file at all
// (see internal/sqlite). Writing "sqlite" here instead would silently depend on
// a driver another package happens to register.
func buildDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open(sqlite.Driver, "file:"+path)
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
	if d.Msgs.Value != 2 || d.Tokens.In != 30 || d.Tokens.Out != 40 || d.Tokens.Cache != 5 || d.Cost.Value != 0.25 {
		t.Fatalf("numbers = %+v", d)
	}
	if !d.Msgs.Replace || !d.Tokens.Replace || !d.Cost.Replace {
		t.Fatalf("OpenCode deltas must be authoritative: %+v", d)
	}
	if d.Tools.Counts["shell"] != 1 {
		t.Fatalf("tools = %+v, want shell=1", d.Tools)
	}
}

func TestTranscript(t *testing.T) {
	src := &Source{Path: buildDB(t)}
	msgs, err := src.Transcript("s1", nil)
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

// An unchanged session table must make the second scan emit nothing while
// still reporting every live session; a change must bring the deltas back.
func TestScanWatermarkSkipsUnchanged(t *testing.T) {
	path := buildDB(t)
	src := &Source{Path: path}

	first, err := src.Scan(nil, func(source.Delta) {})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}

	emitted := 0
	second, err := src.Scan(first, func(source.Delta) { emitted++ })
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if emitted != 0 {
		t.Fatalf("unchanged rescan emitted %d deltas, want 0", emitted)
	}
	if _, ok := second["s1"]; !ok {
		t.Fatalf("fast path dropped the live session id: %+v", second)
	}

	// Change the session: add a message and bump its update time.
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message VALUES ('s1','assistant','{"content":[{"type":"text","text":"more"}]}',3,1735725610000)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE session_v2 SET time_updated = 1735729210000 WHERE id='s1'`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	emitted = 0
	if _, err := src.Scan(second, func(source.Delta) { emitted++ }); err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if emitted != 1 {
		t.Fatalf("after a change, emitted %d deltas, want 1", emitted)
	}
}
