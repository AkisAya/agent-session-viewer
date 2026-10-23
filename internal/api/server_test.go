package api

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/akisaya/agent-session-viewer/internal/index"
	"github.com/akisaya/agent-session-viewer/internal/source/claude"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	return newLiveTestServer(t, "")
}

// newLiveTestServer indexes one Claude session (sess-1) and reads live status
// files from liveDir ("" disables live status).
func newLiveTestServer(t *testing.T, liveDir string) http.Handler {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess-1.jsonl"),
		[]byte(`{"type":"user","timestamp":"2026-01-01T12:00:00Z","sessionId":"sess-1","cwd":"/tmp/p","message":{"role":"user","content":"hi"}}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	ix, err := index.Open(index.Options{Path: filepath.Join(t.TempDir(), "index.db")}, &claude.Source{Root: root, SessionsDir: liveDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	if err := ix.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>ok")}}
	return New(ix, web, "test")
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestStatsAgentsIsAlwaysAnArray(t *testing.T) {
	// A cwd scope with no sessions still expects a (possibly empty) agents
	// list rather than JSON null.
	rec := get(t, newTestServer(t), "/api/stats?cwd=__none__")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Agents json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(body.Agents) != "[]" {
		t.Fatalf("agents = %s, want []", body.Agents)
	}
}

func TestSessionsAndTranscript(t *testing.T) {
	h := newTestServer(t)

	rec := get(t, h, "/api/sessions")
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions status = %d", rec.Code)
	}
	var page struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].ID != "sess-1" || page.Total != 1 {
		t.Fatalf("page = %+v, want sess-1 with total 1", page)
	}

	if rec := get(t, h, "/api/sessions/claude/sess-1"); rec.Code != http.StatusOK {
		t.Fatalf("transcript status = %d", rec.Code)
	}
	if rec := get(t, h, "/api/sessions/claude/sess-1/meta"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sess-1"`) {
		t.Fatalf("session meta = %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "/api/sessions/claude/missing/meta"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing meta status = %d, want 404", rec.Code)
	}
}

func TestSessionsPagination(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/api/sessions?limit=1&offset=1")
	var page struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
		Total  int `json:"total"`
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Total != 1 || page.Limit != 1 || page.Offset != 1 || len(page.Sessions) != 0 {
		t.Fatalf("page = %+v, want an empty second page", page)
	}
}

func TestInvalidSessionIDIsRejected(t *testing.T) {
	// An encoded path separator must not reach the source fallback lookup.
	rec := get(t, newTestServer(t), "/api/sessions/claude/..%2F..%2Fetc%2Fpasswd")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRescanRequiresJSON(t *testing.T) {
	h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/rescan", nil))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestVersionAndScanStatus(t *testing.T) {
	h := newTestServer(t)
	if rec := get(t, h, "/api/version"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"test"`) {
		t.Fatalf("version response = %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "/api/scan"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total"`) {
		t.Fatalf("scan response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestJSONIsGzipped(t *testing.T) {
	h := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", rec.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if !strings.Contains(string(body), `"test"`) {
		t.Fatalf("decoded body = %s", body)
	}
}

func TestLiveIsAnEmptyListWithoutStatusFiles(t *testing.T) {
	rec := get(t, newTestServer(t), "/api/live")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"sessions":[]}` {
		t.Fatalf("live = %d %s, want an empty sessions array", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}

// The test process itself stands in for a running Claude Code: its pid is
// alive, so its status file is reported and joined with the indexed summary.
func TestLiveJoinsTheIndexedSession(t *testing.T) {
	dir := t.TempDir()
	pid, ppid := strconv.Itoa(os.Getpid()), strconv.Itoa(os.Getppid())
	for name, body := range map[string]string{
		pid + ".json": `{"pid":` + pid + `,"sessionId":"sess-1","cwd":"/tmp/p","status":"waiting","waitingFor":"dialog open","statusUpdatedAt":1792740117076}`,
		// alive, but an id the transcript endpoints would reject
		ppid + ".json": `{"pid":` + ppid + `,"sessionId":"../escape","status":"busy"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := get(t, newLiveTestServer(t, dir), "/api/live")
	var body struct {
		Sessions []struct {
			Agent, ID, Status, WaitingFor string
			Session                       *struct{ ID, Title string }
		} `json:"sessions"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if body.Error != "" || len(body.Sessions) != 1 {
		t.Fatalf("live = %s, want one row", rec.Body.String())
	}
	got := body.Sessions[0]
	if got.Agent != "claude" || got.ID != "sess-1" || got.Status != "waiting" || got.WaitingFor != "dialog open" {
		t.Fatalf("row = %+v", got)
	}
	if got.Session == nil || got.Session.ID != "sess-1" || got.Session.Title != "hi" {
		t.Fatalf("row not joined with the indexed summary: %s", rec.Body.String())
	}
}

func TestUnknownSessionIs404(t *testing.T) {
	rec := get(t, newTestServer(t), "/api/sessions/claude/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
