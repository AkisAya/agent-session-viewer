package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/akisaya/agent-session-viewer/internal/index"
	"github.com/akisaya/agent-session-viewer/internal/source/claude"
)

func newTestServer(t *testing.T) http.Handler {
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

	ix, err := index.Open(index.Options{Path: filepath.Join(t.TempDir(), "index.db")}, &claude.Source{Root: root})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	if err := ix.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>ok")}}
	return New(ix, web)
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
	var sessions []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "sess-1" {
		t.Fatalf("sessions = %+v, want sess-1", sessions)
	}

	if rec := get(t, h, "/api/sessions/claude/sess-1"); rec.Code != http.StatusOK {
		t.Fatalf("transcript status = %d", rec.Code)
	}
}

func TestUnknownSessionIs404(t *testing.T) {
	rec := get(t, newTestServer(t), "/api/sessions/claude/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
