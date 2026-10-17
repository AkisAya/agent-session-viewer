// Package wasmtest is the executable specification for running the scanner and
// the cache on js/wasm.
//
// It builds a small fixture tree covering all three agents, runs the real
// pipeline over it, and renders the result as stable text. The same code runs
// natively and under a wasm runtime, so the two can be compared:
//
//	go test ./internal/wasmtest/
//	GOOS=js GOARCH=wasm go test -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./internal/wasmtest/
//
// cmd/wasmcheck prints the same text, for use against a real data directory or
// from a browser host page.
package wasmtest

import (
	_ "embed"
	"os"
	"path/filepath"
)

// opencodeDB is a real, minimal OpenCode database with one session (schema and
// rows mirroring internal/source/opencode's own fixtures). It is committed
// rather than built at run time because building a database file is exactly the
// thing that does not work under wasm.
//
//go:embed testdata/opencode.db
var opencodeDB []byte

// Paths for a fixture tree rooted at home. The layouts mirror the real agents.
const (
	ClaudeRoot   = "/.claude/projects"
	CodexRoot    = "/.codex/sessions"
	CodexIndex   = "/.codex/session_index.jsonl"
	OpenCodePath = "/.local/share/opencode/opencode.db"
)

// codexID is one Codex session spread over two rollout files, so the fixture
// exercises the cross-file merge and the resume relation.
const codexID = "11111111-1111-1111-1111-111111111111"

// WriteFixtureTree writes the fixture tree under home and returns it.
func WriteFixtureTree(home string) (string, error) {
	join := func(p string) string { return filepath.Join(home, filepath.FromSlash(p)) }

	writeFile := func(p, content string) error {
		path := join(p)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), 0o644)
	}

	files := map[string]string{
		ClaudeRoot + "/-tmp-demo/sess-a.jsonl": lines(
			`{"type":"user","timestamp":"2026-02-01T10:00:00Z","sessionId":"sess-a","cwd":"/tmp/demo","message":{"role":"user","content":"hello claude"}}`,
			`{"type":"assistant","timestamp":"2026-02-01T10:00:05Z","sessionId":"sess-a","cwd":"/tmp/demo","message":{"role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"hi"},{"type":"thinking","thinking":"think"},{"type":"tool_use","name":"Bash"}]}}`,
			`{"type":"user","timestamp":"2026-02-02T10:00:00Z","sessionId":"sess-a","cwd":"/tmp/demo","aiTitle":"Demo A","message":{"role":"user","content":"second day"}}`,
		),
		ClaudeRoot + "/-tmp-demo/sess-b.jsonl": lines(
			`{"type":"user","timestamp":"2026-02-03T10:00:00Z","sessionId":"sess-b","cwd":"/tmp/demo","customTitle":"Demo B","message":{"role":"user","content":"hello b"}}`,
			`{"type":"assistant","timestamp":"2026-02-03T10:00:05Z","sessionId":"sess-b","cwd":"/tmp/demo","message":{"role":"assistant","model":"claude-opus-4","content":[{"type":"text","text":"ok"},{"type":"tool_use","name":"mcp__github__create_issue"}]}}`,
		),
		// One Codex session across two rollouts: a resume.
		CodexRoot + "/2026/02/01/rollout-2026-02-01T11-00-00-" + codexID + ".jsonl": lines(
			`{"type":"session_meta","timestamp":"2026-02-01T11:00:00Z","payload":{"session_id":"`+codexID+`","cwd":"/tmp/demo"}}`,
			`{"type":"turn_context","timestamp":"2026-02-01T11:00:01Z","payload":{"model":"gpt-5-codex"}}`,
			`{"type":"event_msg","timestamp":"2026-02-01T11:00:05Z","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"text":"hello codex"}]}}}`,
		),
		CodexRoot + "/2026/02/02/rollout-2026-02-02T11-00-00-" + codexID + ".jsonl": lines(
			`{"type":"session_meta","timestamp":"2026-02-02T11:00:00Z","payload":{"session_id":"`+codexID+`","cwd":"/tmp/demo","parent_thread_id":"22222222-2222-2222-2222-222222222222"}}`,
			`{"type":"event_msg","timestamp":"2026-02-02T11:00:05Z","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"text":"hello back"}]}}}`,
		),
		CodexIndex: `{"id":"` + codexID + `","thread_name":"Demo Codex"}` + "\n",
	}
	for p, content := range files {
		if err := writeFile(p, content); err != nil {
			return "", err
		}
	}

	dbPath := join(OpenCodePath)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dbPath, opencodeDB, 0o644); err != nil {
		return "", err
	}
	return home, nil
}

func lines(ls ...string) string {
	out := ""
	for _, l := range ls {
		out += l + "\n"
	}
	return out
}
