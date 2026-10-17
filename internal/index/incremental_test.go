package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/akisaya/agent-session-viewer/internal/model"
	"github.com/akisaya/agent-session-viewer/internal/source"
	"github.com/akisaya/agent-session-viewer/internal/source/claude"
	"github.com/akisaya/agent-session-viewer/internal/source/codex"
)

func newIndex(t *testing.T, sources ...source.Source) *Index {
	t.Helper()
	ix, err := Open(Options{Path: filepath.Join(t.TempDir(), "index.db")}, sources...)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	return ix
}

func scan(t *testing.T, ix *Index) {
	t.Helper()
	if err := ix.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}
}

func writeLines(t *testing.T, path string, appendMode bool, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	flag := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
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

// snapshot is a canonical, order-independent dump of everything the index
// knows about one agent. Two indexes that parsed the same data must render the
// same snapshot whether the data arrived in one full scan or in appends.
type snapshot struct {
	sessions  map[string]model.Session
	activity  []model.ActivityDay
	tools     []model.ToolStat
	toolTotal int64
}

func takeSnapshot(t *testing.T, ix *Index, agent string) snapshot {
	t.Helper()
	sessions, err := ix.Sessions(Filter{Agent: agent})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	s := snapshot{sessions: map[string]model.Session{}}
	for _, x := range sessions {
		s.sessions[x.ID] = x
	}
	if s.activity, err = ix.Activity(agent, ""); err != nil {
		t.Fatalf("activity: %v", err)
	}
	tools, err := ix.Tools(agent, "", 0)
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	s.tools, s.toolTotal = tools.Tools, tools.Total
	return s
}

func (s snapshot) canon() string {
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	for _, id := range ids {
		x := s.sessions[id]
		fmt.Fprintf(&b, "S %s title=%q preview=%q cwd=%q model=%q msgs=%d tin=%d tout=%d cache=%d cost=%g parent=%q rel=%q created=%d updated=%d\n",
			id, x.Title, x.Preview, x.CWD, x.Model, x.MsgCount, x.TokensIn, x.TokensOut, x.CacheRead, x.Cost,
			x.ParentID, x.Relation, x.CreatedAt.UnixNano(), x.UpdatedAt.UnixNano())
	}
	for _, a := range s.activity {
		fmt.Fprintf(&b, "A %s %d %d\n", a.Day, a.Msgs, a.Sessions)
	}
	for _, tl := range s.tools {
		fmt.Fprintf(&b, "T %s %d %d\n", tl.Tool, tl.Count, tl.Sessions)
	}
	fmt.Fprintf(&b, "TT %d\n", s.toolTotal)
	return b.String()
}

// assertIncrementalMatchesFull is the core regression guard: parsing a file in
// one go and parsing it, then appending and re-parsing only the new bytes, must
// leave the index in an identical state.
func assertIncrementalMatchesFull(t *testing.T, build func(dir string) source.Source, agent string, first, second []string, file string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, file)
	writeLines(t, path, false, first...)

	inc := newIndex(t, build(dir))
	scan(t, inc)

	writeLines(t, path, true, second...)
	scan(t, inc)

	full := newIndex(t, build(dir))
	scan(t, full)

	got, want := takeSnapshot(t, inc, agent).canon(), takeSnapshot(t, full, agent).canon()
	if got != want {
		t.Fatalf("incremental scan differs from full scan\n--- incremental ---\n%s--- full ---\n%s", got, want)
	}
}

const claudeFirst = `{"type":"summary","timestamp":"2026-01-01T11:00:00Z","sessionId":"sess-1","summary":"Fix the bug"}`

func TestClaudeIncrementalMatchesFull(t *testing.T) {
	assertIncrementalMatchesFull(t,
		func(dir string) source.Source { return &claude.Source{Root: dir} },
		"claude",
		[]string{
			claudeFirst,
			`{"type":"user","timestamp":"2026-01-01T12:00:00Z","sessionId":"sess-1","cwd":"/tmp/p","message":{"role":"user","content":"hello there"}}`,
			`{"type":"assistant","timestamp":"2026-01-01T12:00:05Z","sessionId":"sess-1","cwd":"/tmp/p","message":{"role":"assistant","model":"claude-x","content":[{"type":"text","text":"working on it"},{"type":"tool_use","name":"Bash","input":{}}],"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2}}}`,
		},
		[]string{
			`{"type":"user","timestamp":"2026-01-02T12:00:00Z","sessionId":"sess-1","cwd":"/tmp/p","message":{"role":"user","content":"follow up"}}`,
			`{"type":"assistant","timestamp":"2026-01-02T12:00:09Z","sessionId":"sess-1","cwd":"/tmp/p","message":{"role":"assistant","model":"claude-x","content":[{"type":"text","text":"done"},{"type":"tool_use","name":"Edit","input":{}}],"usage":{"input_tokens":7,"output_tokens":4,"cache_read_input_tokens":1}}}`,
		},
		filepath.Join("proj", "sess-1.jsonl"))
}

func TestCodexIncrementalMatchesFull(t *testing.T) {
	assertIncrementalMatchesFull(t,
		func(dir string) source.Source {
			return &codex.Source{Root: dir, Index: filepath.Join(dir, "session_index.jsonl")}
		},
		"codex",
		[]string{
			`{"type":"session_meta","timestamp":"2026-02-01T11:00:00Z","payload":{"session_id":"cx-1","cwd":"/tmp/q","model_provider":"openai"}}`,
			`{"type":"turn_context","timestamp":"2026-02-01T11:00:01Z","payload":{"model":"gpt-5","cwd":"/tmp/q"}}`,
			`{"type":"event_msg","timestamp":"2026-02-01T12:00:05Z","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"text":"hello codex"}]}}}`,
			`{"type":"event_msg","timestamp":"2026-02-01T12:00:06Z","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"text":"hi there"}]}}}`,
			`{"type":"event_msg","timestamp":"2026-02-01T12:00:07Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":10,"output_tokens":20}}}}`,
			`{"type":"response_item","timestamp":"2026-02-01T12:00:05Z","payload":{"type":"message","role":"user","content":[{"type":"text","text":"hello codex"}]}}`,
			`{"type":"response_item","timestamp":"2026-02-01T12:00:06Z","payload":{"type":"message","role":"assistant","content":[{"type":"text","text":"hi there"}]}}`,
			`{"type":"response_item","timestamp":"2026-02-01T12:00:07Z","payload":{"type":"function_call","name":"exec_command"}}`,
		},
		[]string{
			`{"type":"event_msg","timestamp":"2026-02-02T12:00:05Z","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"text":"again"}]}}}`,
			`{"type":"event_msg","timestamp":"2026-02-02T12:00:06Z","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"text":"sure"}]}}}`,
			`{"type":"event_msg","timestamp":"2026-02-02T12:00:07Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":250,"cached_input_tokens":30,"output_tokens":55}}}}`,
			`{"type":"response_item","timestamp":"2026-02-02T12:00:05Z","payload":{"type":"message","role":"user","content":[{"type":"text","text":"again"}]}}`,
			`{"type":"response_item","timestamp":"2026-02-02T12:00:06Z","payload":{"type":"message","role":"assistant","content":[{"type":"text","text":"sure"}]}}`,
			`{"type":"response_item","timestamp":"2026-02-02T12:00:07Z","payload":{"type":"function_call","name":"apply_patch"}}`,
		},
		filepath.Join("2026", "02", "01", "rollout-2026-02-01T11-00-00-cx1.jsonl"))
}

// A Codex session can span several rollout files. Losing one of them must not
// delete the session while another still backs it.
func TestPruneKeepsMultiFileSession(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "rollout-a.jsonl")
	b := filepath.Join(dir, "rollout-b.jsonl")
	writeLines(t, a, false,
		`{"type":"session_meta","timestamp":"2026-03-01T12:00:00Z","payload":{"session_id":"multi","cwd":"/tmp/m"}}`,
		`{"type":"event_msg","timestamp":"2026-03-01T12:00:05Z","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"text":"part a"}]}}}`)
	writeLines(t, b, false,
		`{"type":"session_meta","timestamp":"2026-03-02T12:00:00Z","payload":{"session_id":"multi","cwd":"/tmp/m"}}`,
		`{"type":"event_msg","timestamp":"2026-03-02T12:00:05Z","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"text":"part b"}]}}}`)

	ix := newIndex(t, &codex.Source{Root: dir})
	scan(t, ix)

	if got := len(takeSnapshot(t, ix, "codex").sessions); got != 1 {
		t.Fatalf("sessions = %d, want 1", got)
	}

	// One of the two backing files disappears; the session must survive.
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	scan(t, ix)
	if got := len(takeSnapshot(t, ix, "codex").sessions); got != 1 {
		t.Fatalf("after removing one rollout file, sessions = %d, want 1", got)
	}

	// Removing the last backing file finally drops the session.
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	scan(t, ix)
	if got := len(takeSnapshot(t, ix, "codex").sessions); got != 0 {
		t.Fatalf("after removing every rollout file, sessions = %d, want 0", got)
	}
}
