package codex

import (
	"os"
	"path/filepath"
	"testing"
)

const testID = "11111111-1111-1111-1111-111111111111"

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
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

// rollout paths end with the session UUID; find() resolves a session by that
// filename suffix.
func rollout(dir, stamp string) string {
	return filepath.Join(dir, "2026", "02", "01", "rollout-"+stamp+"-"+testID+".jsonl")
}

// A resumed Codex session spans several rollout files; Transcript must merge
// them in timestamp order rather than returning only the first one.
func TestTranscriptMergesRolloutFiles(t *testing.T) {
	dir := t.TempDir()
	writeLines(t, rollout(dir, "2026-02-01T11-00-00"),
		`{"type":"session_meta","timestamp":"2026-02-01T11:00:00Z","payload":{"session_id":"`+testID+`","cwd":"/tmp/q"}}`,
		`{"type":"event_msg","timestamp":"2026-02-01T11:00:05Z","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"text":"first"}]}}}`)
	writeLines(t, rollout(dir, "2026-02-02T11-00-00"),
		`{"type":"session_meta","timestamp":"2026-02-02T11:00:00Z","payload":{"session_id":"`+testID+`","cwd":"/tmp/q"}}`,
		`{"type":"event_msg","timestamp":"2026-02-02T11:00:05Z","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"text":"second"}]}}}`)

	msgs, err := (&Source{Root: dir}).Transcript(testID, nil)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Text != "first" || msgs[1].Text != "second" {
		t.Fatalf("merged transcript = %+v, want first then second", msgs)
	}
}

// Older rollouts carry no event_msg conversation items; Transcript must fall
// back to response_item messages and drop synthetic context blocks.
func TestTranscriptFallsBackToResponseItems(t *testing.T) {
	dir := t.TempDir()
	writeLines(t, rollout(dir, "2026-02-01T11-00-00"),
		`{"type":"session_meta","timestamp":"2026-02-01T11:00:00Z","payload":{"session_id":"`+testID+`","cwd":"/tmp/q"}}`,
		`{"type":"response_item","timestamp":"2026-02-01T11:00:01Z","payload":{"type":"message","role":"user","content":[{"type":"text","text":"<environment_context>ignored"}]}}`,
		`{"type":"response_item","timestamp":"2026-02-01T11:00:02Z","payload":{"type":"message","role":"user","content":[{"type":"text","text":"real question"}]}}`,
		`{"type":"response_item","timestamp":"2026-02-01T11:00:03Z","payload":{"type":"message","role":"assistant","content":[{"type":"text","text":"answer"}]}}`)

	msgs, err := (&Source{Root: dir}).Transcript(testID, nil)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Text != "real question" || msgs[1].Text != "answer" {
		t.Fatalf("transcript = %+v, want the synthetic block dropped", msgs)
	}
}
