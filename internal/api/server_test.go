package api

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestUnknownSessionIs404(t *testing.T) {
	rec := get(t, newTestServer(t), "/api/sessions/claude/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
