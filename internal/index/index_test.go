package index

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/model"
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

// fold applies the histogram half of a delta (activity + hours + tool use) the
// way a scan does, in its own transaction.
func fold(t *testing.T, ix *Index, agent string, d source.Delta) {
	t.Helper()
	tx, err := ix.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	w, err := newWriter(tx)
	if err != nil {
		t.Fatalf("writer: %v", err)
	}
	defer w.close()
	if err := w.applyActivity(agent, d); err != nil {
		t.Fatalf("applyActivity: %v", err)
	}
	if err := w.applyHours(agent, d); err != nil {
		t.Fatalf("applyHours: %v", err)
	}
	if err := w.applyTools(agent, d); err != nil {
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
	today := time.Now().Format("2006-01-02")

	// Full parse of the first rollout file.
	fold(t, ix, "codex", source.Delta{ID: "s1", Key: "rollout-a.jsonl",
		Activity: source.Histogram{Replace: true, Counts: map[string]int{"2026-01-02": 3}},
		Hours:    source.Histogram{Replace: true, Counts: map[string]int{today + " 09": 3}},
		Tools:    source.Histogram{Replace: true, Counts: map[string]int{"shell": 2}}})
	// Full parse of the second file of the same session: additive across src.
	fold(t, ix, "codex", source.Delta{ID: "s1", Key: "rollout-b.jsonl",
		Activity: source.Histogram{Replace: true, Counts: map[string]int{"2026-01-02": 4}},
		Hours:    source.Histogram{Replace: true, Counts: map[string]int{today + " 09": 1, today + " 14": 3, "2000-01-01 10": 9}},
		Tools:    source.Histogram{Replace: true, Counts: map[string]int{"shell": 1}}})
	// An incremental delta (newly appended bytes only) merges into its src.
	fold(t, ix, "codex", source.Delta{ID: "s1", Key: "rollout-a.jsonl",
		Activity: source.Histogram{Counts: map[string]int{"2026-01-03": 2}},
		Hours:    source.Histogram{Counts: map[string]int{today + " 09": 2, "garbage": 7, today + " 25": 5}},
		Tools:    source.Histogram{Counts: map[string]int{"edit": 1}}})
	// A full re-parse of file A replaces only A's own rows.
	fold(t, ix, "codex", source.Delta{ID: "s1", Key: "rollout-a.jsonl",
		Activity: source.Histogram{Replace: true, Counts: map[string]int{"2026-01-02": 1}},
		Hours:    source.Histogram{Replace: true, Counts: map[string]int{today + " 09": 2}},
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

	// Hours are per day: only today's buckets come back, all 24 of them.
	// "garbage" and hour 25 were rejected, and the 2000-01-01 row is excluded.
	hours, err := ix.Hours("codex", "/tmp/p")
	if err != nil {
		t.Fatalf("hours: %v", err)
	}
	if len(hours) != 24 {
		t.Fatalf("len(hours) = %d, want 24", len(hours))
	}
	if hours[9].Msgs != 3 || hours[9].Sessions != 1 || hours[14].Msgs != 3 || hours[14].Sessions != 1 {
		t.Fatalf("hours 9/14 = %+v, %+v, want 3 msgs each from 1 session", hours[9], hours[14])
	}
	if hours[10].Msgs != 0 || hours[0].Msgs != 0 || hours[23].Msgs != 0 {
		t.Fatalf("non-today/idle hours = %+v %+v %+v, want zero", hours[10], hours[0], hours[23])
	}

	// Scoping by another agent or directory must find nothing.
	other, err := ix.Activity("claude", "")
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("claude activity = %+v, want empty", other)
	}
	otherHours, err := ix.Hours("claude", "")
	if err != nil {
		t.Fatalf("hours: %v", err)
	}
	for _, h := range otherHours {
		if h.Msgs != 0 {
			t.Fatalf("claude hours = %+v, want all zero", otherHours)
		}
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

// A token/cost trend must apportion a session's lifetime usage across all of
// its active days, not only the days inside the requested window. Otherwise a
// session older than the window inflates the recent days.
func TestTrendApportionsAcrossAllDays(t *testing.T) {
	ix := openTest(t)
	today := time.Now().Format("2006-01-02")
	old := "2000-01-01"
	if _, err := ix.db.Exec(`INSERT INTO session(agent,id,tokens_in,cost,updated_at) VALUES('claude','s1',1000,10,1)`); err != nil {
		t.Fatal(err)
	}
	for _, day := range []string{old, today} {
		if _, err := ix.db.Exec(`INSERT INTO activity(agent,id,src,day,msgs) VALUES('claude','s1','f',?,1)`, day); err != nil {
			t.Fatal(err)
		}
	}
	pts, err := ix.Trend("", "", 30)
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if len(pts) != 1 || pts[0].Day != today {
		t.Fatalf("trend = %+v, want only today", pts)
	}
	if pts[0].Tokens != 500 || pts[0].Cost != 5 {
		t.Fatalf("today = %+v, want half the lifetime usage (500 tokens / $5)", pts[0])
	}
}

func TestSessionPageAndCostSort(t *testing.T) {
	ix := openTest(t)
	insertSession(t, ix, "claude", "a", "/tmp/p", "", "")
	insertSession(t, ix, "claude", "b", "/tmp/p", "", "")
	insertSession(t, ix, "claude", "c", "/tmp/p", "", "")
	for id, cost := range map[string]float64{"a": 1, "b": 30, "c": 10} {
		if _, err := ix.db.Exec(`UPDATE session SET cost = ? WHERE id = ?`, cost, id); err != nil {
			t.Fatal(err)
		}
	}
	page, err := ix.SessionPage(Filter{Sort: "cost", Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if page.Total != 3 || len(page.Sessions) != 2 {
		t.Fatalf("page = %+v, want total 3 and two rows", page)
	}
	if page.Sessions[0].ID != "c" || page.Sessions[1].ID != "a" {
		t.Fatalf("order = %s,%s, want c,a", page.Sessions[0].ID, page.Sessions[1].ID)
	}
}

func TestSubagentNotCountedAsSession(t *testing.T) {
	ix := openTest(t)
	insertSession(t, ix, "claude", "root", "/tmp/p", "", "")
	insertSession(t, ix, "claude", "agent-kid", "/tmp/p", "root", "subagent")

	st, err := ix.Stats("", "", "")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Total.Sessions != 1 || len(st.Agents) != 1 || st.Agents[0].Sessions != 1 {
		t.Fatalf("stats = %+v, want 1 main session", st)
	}

	projects, err := ix.Projects("claude")
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Count != 1 || projects[0].Agents["claude"] != 1 {
		t.Fatalf("projects = %+v, want 1 session in /tmp/p", projects)
	}

	page, err := ix.SessionPage(Filter{})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if page.Total != 1 || len(page.Sessions) != 1 || page.Sessions[0].ID != "root" {
		t.Fatalf("page = %+v, want only the main session", page)
	}

	withSubs, err := ix.SessionPage(Filter{Subagents: true})
	if err != nil {
		t.Fatalf("page with subagents: %v", err)
	}
	if withSubs.Total != 2 || len(withSubs.Sessions) != 2 {
		t.Fatalf("page with subagents = %+v, want both rows", withSubs)
	}
}

func TestSessionPageIncludesToolCount(t *testing.T) {
	ix := openTest(t)
	insertSession(t, ix, "claude", "s1", "/tmp/p", "", "")
	_, err := ix.db.Exec(`INSERT INTO tool_use(agent,id,src,tool,cnt) VALUES
		('claude','s1','f','shell',3),
		('claude','s1','f','read',2),
		('claude','s1','g','shell',1)`)
	if err != nil {
		t.Fatal(err)
	}
	page, err := ix.SessionPage(Filter{})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].ToolCount != 6 {
		t.Fatalf("page = %+v, want one session with 6 tool calls", page.Sessions)
	}
}

func TestSearchEscapesLikeWildcards(t *testing.T) {
	ix := openTest(t)
	_, err := ix.db.Exec(`INSERT INTO session(agent,id,title,cwd) VALUES
		('claude','pct','100% done','/p'),
		('claude','us','a_b','/p'),
		('claude','plain','nothing','/p')`)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		q     string
		want  string
		empty bool
	}{
		{"100%", "pct", false},
		{"100_", "", true}, // "_" must be literal, so it cannot match "%"
		{"a_b", "us", false},
		{"a%b", "", true}, // "%" must be literal
	}
	for _, c := range cases {
		rows, err := ix.Sessions(Filter{Query: c.q})
		if err != nil {
			t.Fatalf("query %q: %v", c.q, err)
		}
		if c.empty {
			if len(rows) != 0 {
				t.Fatalf("query %q returned %d rows, want none", c.q, len(rows))
			}
			continue
		}
		if len(rows) != 1 || rows[0].ID != c.want {
			t.Fatalf("query %q = %+v, want %s", c.q, rows, c.want)
		}
	}
}

// fakeSource lets the scan tests exercise error isolation without touching
// the filesystem.
type fakeSource struct {
	name  string
	err   error
	delta *source.Delta
	block chan struct{}
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Transcript(string, []string) ([]model.Message, error) {
	return nil, nil
}
func (f *fakeSource) Scan(prev map[string]source.Cursor, emit func(source.Delta)) (map[string]source.Cursor, error) {
	if f.block != nil {
		<-f.block
	}
	if f.delta != nil {
		emit(*f.delta)
		return map[string]source.Cursor{"f": {ID: f.delta.ID}}, nil
	}
	if f.err != nil {
		return nil, f.err
	}
	return map[string]source.Cursor{}, nil
}

func TestScanContinuesAfterSourceError(t *testing.T) {
	bad := &fakeSource{name: "bad", err: errors.New("boom")}
	good := &fakeSource{name: "good", delta: &source.Delta{
		Key: "f", ID: "s1", Msgs: source.Counter{Replace: true, Value: 1},
	}}
	ix, err := Open(Options{Memory: true}, bad, good)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer ix.Close()

	if err := ix.Scan(); err == nil {
		t.Fatal("scan error = nil, want the failing source to be reported")
	}
	st := ix.Status()
	if st.LastError == "" || st.Scanning {
		t.Fatalf("status = %+v, want a recorded error and no scan in flight", st)
	}
	if !st.LastScan.IsZero() {
		t.Fatalf("lastScan = %v, want zero after a failed scan", st.LastScan)
	}
	stats, err := ix.Stats("", "", "")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total.Sessions != 1 {
		t.Fatalf("sessions = %d, want the good source's session to survive", stats.Total.Sessions)
	}
}

func TestStartScanStatusIsImmediate(t *testing.T) {
	block := make(chan struct{})
	ix, err := Open(Options{Memory: true}, &fakeSource{name: "slow", block: block})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer ix.Close()

	done := ix.StartScan()
	if !ix.Status().Scanning {
		t.Fatal("scanning = false immediately after StartScan, want true")
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatalf("scan: %v", err)
	}
	if st := ix.Status(); st.Scanning || st.LastScan.IsZero() {
		t.Fatalf("status = %+v, want a completed successful scan", st)
	}
}

func TestScanRecordsLastScanOnSuccess(t *testing.T) {
	ix, err := Open(Options{Memory: true}, &fakeSource{name: "ok"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer ix.Close()
	if err := ix.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if st := ix.Status(); st.LastScan.IsZero() || st.LastError != "" {
		t.Fatalf("status = %+v, want a successful last scan", st)
	}
}
