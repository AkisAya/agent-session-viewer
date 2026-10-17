package index

import (
	"path/filepath"
	"testing"

	"github.com/akisaya/agent-session-viewer/internal/source"
)

func openTest(t *testing.T) *Index {
	t.Helper()
	ix, err := Open(Options{Path: filepath.Join(t.TempDir(), "index.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	return ix
}

// fold applies the histogram half of a delta (activity + tool use) the way a
// scan does, in its own transaction.
func fold(t *testing.T, ix *Index, agent string, d source.Delta) {
	t.Helper()
	tx, err := ix.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := applyActivity(tx, agent, d); err != nil {
		t.Fatalf("applyActivity: %v", err)
	}
	if err := applyTools(tx, agent, d); err != nil {
		t.Fatalf("applyTools: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func insertSession(t *testing.T, ix *Index, agent, id, cwd, parent, relation string) {
	t.Helper()
	_, err := ix.db.Exec(`INSERT INTO session(agent,id,title,cwd,updated_at,parent_id,relation)
		VALUES(?,?,?,?,?,?,?)`, agent, id, "session "+id, cwd, 1, parent, relation)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

// One Codex session can span several rollout files; their histograms must add
// up per source instead of overwriting each other, while a re-parse of a file
// still replaces that file's own contribution.
func TestHistogramPerSource(t *testing.T) {
	ix := openTest(t)
	insertSession(t, ix, "codex", "s1", "/tmp/p", "", "")

	// Full parse of the first rollout file.
	fold(t, ix, "codex", source.Delta{ID: "s1", Key: "rollout-a.jsonl",
		Activity: source.Histogram{Replace: true, Counts: map[string]int{"2026-01-02": 3}},
		Tools:    source.Histogram{Replace: true, Counts: map[string]int{"shell": 2}}})
	// Full parse of the second file of the same session: additive across src.
	fold(t, ix, "codex", source.Delta{ID: "s1", Key: "rollout-b.jsonl",
		Activity: source.Histogram{Replace: true, Counts: map[string]int{"2026-01-02": 4}},
		Tools:    source.Histogram{Replace: true, Counts: map[string]int{"shell": 1}}})
	// An incremental delta (newly appended bytes only) merges into its src.
	fold(t, ix, "codex", source.Delta{ID: "s1", Key: "rollout-a.jsonl",
		Activity: source.Histogram{Counts: map[string]int{"2026-01-03": 2}},
		Tools:    source.Histogram{Counts: map[string]int{"edit": 1}}})
	// A full re-parse of file A replaces only A's own rows.
	fold(t, ix, "codex", source.Delta{ID: "s1", Key: "rollout-a.jsonl",
		Activity: source.Histogram{Replace: true, Counts: map[string]int{"2026-01-02": 1}},
		Tools:    source.Histogram{Replace: true, Counts: map[string]int{"shell": 5}}})

	days, err := ix.Activity("codex", "/tmp/p")
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if len(days) != 1 || days[0].Day != "2026-01-02" || days[0].Msgs != 5 || days[0].Sessions != 1 {
		t.Fatalf("activity = %+v, want one day 2026-01-02 with 5 msgs", days)
	}

	tools, err := ix.Tools("codex", "/tmp/p", 0)
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if tools.Total != 6 {
		t.Fatalf("total = %d, want 6", tools.Total)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Tool != "shell" || tools.Tools[0].Count != 6 || tools.Tools[0].Sessions != 1 {
		t.Fatalf("tools = %+v, want shell 6 (1 session)", tools.Tools)
	}

	// Scoping by another agent or directory must find nothing.
	other, err := ix.Activity("claude", "")
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("claude activity = %+v, want empty", other)
	}
}

// Relations must return both sides of every parent/child link inside the
// scope, and leave unrelated sessions out.
func TestRelations(t *testing.T) {
	ix := openTest(t)
	insertSession(t, ix, "opencode", "root", "/tmp/p", "", "")
	insertSession(t, ix, "opencode", "kid", "/tmp/p", "root", "subagent")
	insertSession(t, ix, "opencode", "lonely", "/tmp/p", "", "")
	insertSession(t, ix, "codex", "other", "/other", "", "")

	rels, err := ix.Relations("opencode", "/tmp/p")
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	got := map[string]string{}
	for _, s := range rels {
		got[s.ID] = s.Relation
	}
	if len(got) != 2 {
		t.Fatalf("relations = %+v, want root and kid only", rels)
	}
	if _, ok := got["root"]; !ok {
		t.Fatalf("parent root missing from %+v", rels)
	}
	if r, ok := got["kid"]; !ok || r != "subagent" {
		t.Fatalf("child relation = %q, want subagent", r)
	}
}

// Session identity is (agent, id). A parent id that another agent happens to
// reuse must not pull that other agent's sessions into the result.
func TestRelationsIsAgentScoped(t *testing.T) {
	ix := openTest(t)
	insertSession(t, ix, "opencode", "root", "/tmp/p", "", "")
	insertSession(t, ix, "opencode", "kid", "/tmp/p", "root", "subagent")
	// Same directory, same parent id, different agent.
	insertSession(t, ix, "claude", "claude-kid", "/tmp/p", "root", "subagent")

	rels, err := ix.Relations("opencode", "/tmp/p")
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	for _, s := range rels {
		if s.Agent != "opencode" {
			t.Fatalf("relations leaked a %s session: %+v", s.Agent, s)
		}
	}
	if len(rels) != 2 {
		t.Fatalf("relations = %+v, want exactly opencode root and kid", rels)
	}
}
