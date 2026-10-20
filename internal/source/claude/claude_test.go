package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akisaya/agent-session-viewer/internal/source"
)

func writeSession(t *testing.T, root, id string, lines ...string) {
	t.Helper()
	dir := filepath.Join(root, "encoded-cwd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTranscriptRoles(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "sess-1",
		`{"type":"user","timestamp":"2026-01-01T12:00:00Z","sessionId":"sess-1","cwd":"/tmp/p","message":{"role":"user","content":"hi there"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T12:00:05Z","sessionId":"sess-1","message":{"role":"assistant","content":[{"type":"text","text":"answer"},{"type":"thinking","thinking":"because"},{"type":"tool_use","name":"Bash","input":{}}]}}`,
		// A sidechain turn belongs to a sub-agent, not the visible conversation.
		`{"type":"user","timestamp":"2026-01-01T12:00:06Z","sessionId":"sess-1","isSidechain":true,"message":{"role":"user","content":"sidechain noise"}}`)

	msgs, err := (&Source{Root: root}).Transcript("sess-1", nil)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	type want struct{ role, text string }
	wants := []want{
		{"user", "hi there"},
		{"assistant", "answer"},
		{"reasoning", "because"},
		{"tool", "[tool: Bash]"},
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

// Scan must resolve the session id from the JSONL body (not just the filename)
// and normalize tool names into their canonical form.
func TestScanEmitsNormalizedTools(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "file-name",
		`{"type":"user","timestamp":"2026-01-01T12:00:00Z","sessionId":"real-id","cwd":"/tmp/p","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T12:00:05Z","sessionId":"real-id","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash"},{"type":"tool_use","name":"mcp__github__create_issue"}]}}`)

	var deltas []source.Delta
	if _, err := (&Source{Root: root}).Scan(nil, func(d source.Delta) { deltas = append(deltas, d) }); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(deltas) != 1 {
		t.Fatalf("got %d deltas, want 1", len(deltas))
	}
	d := deltas[0]
	if d.ID != "real-id" {
		t.Fatalf("id = %q, want the id from the body", d.ID)
	}
	if d.Tools.Counts["shell"] != 1 || d.Tools.Counts["mcp:github.create_issue"] != 1 {
		t.Fatalf("tools = %+v, want normalized shell + mcp", d.Tools)
	}
}

// Claude Code writes one line per content block, each repeating the response's
// usage; tokens must be counted once per message id.
func TestScanDedupesUsagePerMessage(t *testing.T) {
	root := t.TempDir()
	// input_tokens + cache_creation_input_tokens are both uncached input.
	u := `"usage":{"input_tokens":4,"cache_creation_input_tokens":6,"output_tokens":5,"cache_read_input_tokens":100}`
	writeSession(t, root, "s",
		`{"type":"assistant","timestamp":"2026-01-01T12:00:00Z","sessionId":"s","message":{"id":"m1","content":[{"type":"thinking"}],`+u+`}}`,
		`{"type":"assistant","timestamp":"2026-01-01T12:00:01Z","sessionId":"s","message":{"id":"m1","content":[{"type":"tool_use","name":"Bash"}],`+u+`}}`,
		`{"type":"assistant","timestamp":"2026-01-01T12:00:02Z","sessionId":"s","message":{"id":"m2","content":[{"type":"text","text":"x"}],`+u+`}}`)

	var d source.Delta
	if _, err := (&Source{Root: root}).Scan(nil, func(x source.Delta) { d = x }); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if d.Tokens.In != 20 || d.Tokens.Out != 10 || d.Tokens.Cache != 200 {
		t.Fatalf("tokens = %+v, want 20/10/200 (two distinct messages)", d.Tokens)
	}
}

// After /compact, Claude Code injects a user turn flagged isCompactSummary; it
// must become a compaction marker, not a user message.
func TestTranscriptCompactSummary(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "s",
		`{"type":"user","timestamp":"2026-01-01T12:00:00Z","sessionId":"s","message":{"role":"user","content":"before"}}`,
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-01-01T12:01:00Z","sessionId":"s","content":"Conversation compacted"}`,
		`{"type":"user","timestamp":"2026-01-01T12:01:01Z","sessionId":"s","isCompactSummary":true,"message":{"role":"user","content":"This session is being continued... summary"}}`,
		`{"type":"user","timestamp":"2026-01-01T12:02:00Z","sessionId":"s","message":{"role":"user","content":"after"}}`)

	var d source.Delta
	if _, err := (&Source{Root: root}).Scan(nil, func(x source.Delta) { d = x }); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if d.Compacts.Value != 1 || !d.Compacts.Replace {
		t.Fatalf("compacts = %+v, want 1 replace (one compact_boundary)", d.Compacts)
	}

	msgs, err := (&Source{Root: root}).Transcript("s", nil)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if len(msgs) != 3 || msgs[1].Role != "compact" || msgs[1].Text != "This session is being continued... summary" || msgs[2].Text != "after" {
		t.Fatalf("transcript = %+v, want before / compact / after", msgs)
	}
}

// A path hint from the index must be used directly: the file here lives
// outside the projects root, so only the hint can locate it.
func TestTranscriptUsesHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-9.jsonl")
	if err := os.WriteFile(path,
		[]byte(`{"type":"user","timestamp":"2026-01-01T12:00:00Z","sessionId":"sess-9","message":{"role":"user","content":"from hint"}}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := (&Source{Root: filepath.Join(dir, "empty-root")}).Transcript("sess-9", []string{path})
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "from hint" {
		t.Fatalf("transcript = %+v, want the hinted file", msgs)
	}
}

// Newer Claude Code keeps each subagent transcript beside its parent session:
// <encoded-cwd>/<sessionId>/subagents/agent-<id>.jsonl. Every line carries the
// parent's sessionId and isSidechain, so the scan must key the session by the
// file's agent id, link it to the parent, and read its own description.
func TestScanSubagentDir(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "parent",
		`{"type":"user","timestamp":"2026-01-01T12:00:00Z","sessionId":"parent","cwd":"/tmp/p","message":{"role":"user","content":"parent prompt"}}`)

	dir := filepath.Join(root, "encoded-cwd", "parent", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent-a1b2.jsonl")
	lines := `{"type":"user","timestamp":"2026-01-01T12:00:01Z","sessionId":"parent","isSidechain":true,"agentId":"a1b2","cwd":"/tmp/p","message":{"role":"user","content":"explore the repo"}}
{"type":"assistant","timestamp":"2026-01-01T12:00:02Z","sessionId":"parent","isSidechain":true,"agentId":"a1b2","cwd":"/tmp/p","message":{"role":"assistant","content":[{"type":"text","text":"found it"}]}}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-a1b2.meta.json"),
		[]byte(`{"agentType":"Explore","description":"探索仓库结构"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := map[string]source.Delta{}
	if _, err := (&Source{Root: root}).Scan(nil, func(d source.Delta) { got[d.ID] = d }); err != nil {
		t.Fatalf("scan: %v", err)
	}
	d, ok := got["agent-a1b2"]
	if !ok {
		t.Fatalf("no delta for the subagent, got %v", got)
	}
	if d.ParentID != "parent" || d.Relation != "subagent" {
		t.Fatalf("relation = %q/%q, want parent/subagent", d.ParentID, d.Relation)
	}
	if d.Title != "探索仓库结构" || d.Preview != "explore the repo" {
		t.Fatalf("title/preview = %q/%q", d.Title, d.Preview)
	}
	if d.Msgs.Value != 2 {
		t.Fatalf("msgs = %d, want 2", d.Msgs.Value)
	}
	if p := got["parent"]; p.ParentID != "" {
		t.Fatalf("parent session linked to %q", p.ParentID)
	}

	msgs, err := (&Source{Root: root}).Transcript("agent-a1b2", []string{path})
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if len(msgs) != 2 || msgs[1].Text != "found it" {
		t.Fatalf("transcript = %+v, want both sidechain turns", msgs)
	}
}
