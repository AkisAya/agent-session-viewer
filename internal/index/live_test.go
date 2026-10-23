package index

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/model"
	"github.com/akisaya/agent-session-viewer/internal/source"
)

// historyOnly is a source with no history and no live status.
type historyOnly struct{ name string }

func (s *historyOnly) Name() string { return s.name }
func (s *historyOnly) Scan(map[string]source.Cursor, func(source.Delta)) (map[string]source.Cursor, error) {
	return nil, nil
}
func (s *historyOnly) Transcript(string, []string) ([]model.Message, error) { return nil, nil }

// liveSource adds canned live rows.
type liveSource struct {
	historyOnly
	rows []model.LiveSession
	err  error
}

func (s *liveSource) Live() ([]model.LiveSession, error) { return s.rows, s.err }

func TestLiveOrdersAndJoins(t *testing.T) {
	at := func(min int) time.Time { return time.Date(2026, 10, 23, 15, min, 0, 0, time.UTC) }
	claude := &liveSource{historyOnly: historyOnly{"claude"}, rows: []model.LiveSession{
		{Agent: "claude", ID: "idle-old", Status: "idle", Since: at(1)},
		{Agent: "claude", ID: "busy", Status: "busy", Since: at(2)},
		{Agent: "claude", ID: "idle-new", Status: "idle", Since: at(9)},
		{Agent: "claude", ID: "odd", Status: "compacting", Since: at(9)},
		{Agent: "claude", ID: "shell", Status: "shell", Since: at(5)},
		{Agent: "claude", ID: "waiting", Status: "waiting", WaitingFor: "dialog open", Since: at(0)},
	}}
	ix, err := Open(Options{Path: filepath.Join(t.TempDir(), "index.db")}, claude, &historyOnly{"codex"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	insertSession(t, ix, "claude", "busy", "/p", "", "")
	insertSession(t, ix, "codex", "waiting", "/p", "", "") // same id, other agent: must not join

	rows, err := ix.Live()
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	var order []string
	for _, r := range rows {
		order = append(order, r.ID)
	}
	// waiting first, then working (newest change first), then idle, then unknown.
	if got, want := strings.Join(order, ","), "waiting,shell,busy,idle-new,idle-old,odd"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	for _, r := range rows {
		switch {
		case r.ID == "busy" && (r.Session == nil || r.Session.Title != "session busy"):
			t.Errorf("busy row not joined with its cached summary: %+v", r.Session)
		case r.ID != "busy" && r.Session != nil:
			t.Errorf("%s joined %+v, want no summary", r.ID, r.Session)
		}
	}
}

// One unreadable source must not hide the rows of another.
func TestLiveKeepsRowsWhenASourceFails(t *testing.T) {
	ok := &liveSource{historyOnly: historyOnly{"claude"}, rows: []model.LiveSession{{Agent: "claude", ID: "a", Status: "busy"}}}
	bad := &liveSource{historyOnly: historyOnly{"other"}, err: errors.New("permission denied")}
	ix, err := Open(Options{Path: filepath.Join(t.TempDir(), "index.db")}, bad, ok)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = ix.Close() })

	rows, err := ix.Live()
	if err == nil || !strings.Contains(err.Error(), "other: permission denied") {
		t.Fatalf("err = %v, want the failing source named", err)
	}
	if len(rows) != 1 || rows[0].ID != "a" {
		t.Fatalf("rows = %+v, want the healthy source's row", rows)
	}
}
