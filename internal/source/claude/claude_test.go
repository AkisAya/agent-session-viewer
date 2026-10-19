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
	u := `"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100}`
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
