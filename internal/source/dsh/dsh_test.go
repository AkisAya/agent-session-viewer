package dsh

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/akisaya/agent-session-viewer/internal/source"
)

const (
	headerLine = `{"type":"session","version":4,"id":"session-abc","createdAt":1767225600000,"cwd":"/tmp/project","isSeeded":false,"delegationDepth":0}`
	userLine   = `{"type":"user/message","seq":8,"time":1767225601000,"data":{"content":[{"type":"text","text":"hello dsh"}],"source":{"kind":"user"},"role":"user"}}`
	ctxLine    = `{"type":"user/message","seq":9,"time":1767225601000,"data":{"content":[{"type":"text","text":"runtime context"}],"source":{"kind":"runtime-context"},"role":"user"}}`
	assistant  = `{"type":"assistant/message","seq":10,"time":1767225602000,"data":{"message":{"role":"assistant","content":[{"type":"reasoning","text":"think"},{"type":"text","text":"hi"},{"type":"tool-call","id":"c1","name":"bash","arguments":"{}"}],"source":{"provider":"deepseek-account","model":"deepseek-flash"}},"usage":{"inputTokens":10,"outputTokens":5,"cacheReadTokens":100,"cacheWriteTokens":0}}}`
	toolCall   = `{"type":"tool/call","seq":11,"time":1767225602000,"data":{"callId":"c1","name":"bash","arguments":"{}"}}`
	titleLine  = `{"type":"session/title","seq":12,"time":1767225603000,"data":{"title":"DSH title"}}`
)

func zstdFrame(t *testing.T, lines ...string) []byte {
	t.Helper()
	var raw bytes.Buffer
	for _, line := range lines {
		raw.WriteString(line)
		raw.WriteByte('\n')
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	return enc.EncodeAll(raw.Bytes(), nil)
}

func rawFrame(lines ...string) []byte {
	var out bytes.Buffer
	for _, line := range lines {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// writeSession builds a session directory under a fake project and returns the
// canonical log path. frames are append batches; the first frame normally holds
// the header.
func writeSession(t *testing.T, root, id string, compressed bool, frames ...[]byte) string {
	t.Helper()
	dir := filepath.Join(root, "--tmp-project--", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "session.v4.jsonl"
	if compressed {
		name += ".zstd"
	}
	path := filepath.Join(dir, name)
	var out []byte
	for _, frame := range frames {
		out = append(out, frame...)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendBytes(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, src *Source, prev map[string]source.Cursor) ([]source.Delta, map[string]source.Cursor) {
	t.Helper()
	var deltas []source.Delta
	next, err := src.Scan(prev, func(d source.Delta) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return deltas, next
}

func TestScan(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		name := "raw"
		frame := func(lines ...string) []byte { return rawFrame(lines...) }
		if compressed {
			name = "zstd"
			frame = func(lines ...string) []byte { return zstdFrame(t, lines...) }
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := writeSession(t, root, "session-abc", compressed,
				frame(headerLine),
				frame(userLine, ctxLine),
				frame(assistant),
				frame(toolCall),
				frame(titleLine),
			)

			deltas, next := scan(t, &Source{Root: root}, nil)
			if len(deltas) != 1 {
				t.Fatalf("got %d deltas, want 1", len(deltas))
			}
			d := deltas[0]
			if d.Key != path || d.ID != "session-abc" {
				t.Fatalf("identity = %+v", d)
			}
			if d.Title != "DSH title" {
				t.Fatalf("title = %q", d.Title)
			}
			if d.Preview != "hello dsh" {
				t.Fatalf("preview = %q", d.Preview)
			}
			if d.CWD != "/tmp/project" {
				t.Fatalf("cwd = %q", d.CWD)
			}
			if d.Model != "deepseek-account/deepseek-flash" {
				t.Fatalf("model = %q", d.Model)
			}
			// One real user prompt plus one assistant message; the injected
			// runtime-context message is not counted.
			if d.Msgs.Value != 2 || !d.Msgs.Replace {
				t.Fatalf("msgs = %+v, want 2 replace", d.Msgs)
			}
			if d.Tokens.In != 10 || d.Tokens.Out != 5 || d.Tokens.Cache != 100 || !d.Tokens.Replace {
				t.Fatalf("tokens = %+v", d.Tokens)
			}
			if d.Tools.Counts["shell"] != 1 || !d.Tools.Replace {
				t.Fatalf("tools = %+v", d.Tools)
			}
			day := time.UnixMilli(1767225601000).Local().Format("2006-01-02")
			if d.Activity.Counts[day] != 2 || !d.Activity.Replace {
				t.Fatalf("activity = %+v, want %s=2", d.Activity, day)
			}
			if got := d.CreatedAt.UnixMilli(); got != 1767225600000 {
				t.Fatalf("createdAt = %d", got)
			}
			if got := d.UpdatedAt.UnixMilli(); got != 1767225603000 {
				t.Fatalf("updatedAt = %d", got)
			}
			if next[path].Offset != int64(fileSize(t, path)) {
				t.Fatalf("cursor offset = %d, file size = %d", next[path].Offset, fileSize(t, path))
			}

			// An unchanged rescan must not emit again.
			deltas, _ = scan(t, &Source{Root: root}, next)
			if len(deltas) != 0 {
				t.Fatalf("unchanged rescan emitted %d deltas", len(deltas))
			}
		})
	}
}

func TestScanIncremental(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		name := "raw"
		frame := func(lines ...string) []byte { return rawFrame(lines...) }
		if compressed {
			name = "zstd"
			frame = func(lines ...string) []byte { return zstdFrame(t, lines...) }
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := writeSession(t, root, "session-abc", compressed,
				frame(headerLine),
				frame(userLine, ctxLine),
				frame(assistant),
			)
			src := &Source{Root: root}
			first, next := scan(t, src, nil)
			if len(first) != 1 || first[0].Title != "hello dsh" {
				t.Fatalf("first scan = %+v", first)
			}

			appendBytes(t, path, frame(
				titleLine,
				`{"type":"assistant/message","seq":20,"time":1767225604000,"data":{"message":{"role":"assistant","content":[{"type":"text","text":"more"}],"source":{"provider":"deepseek-account","model":"deepseek-flash"}},"usage":{"inputTokens":2,"outputTokens":3,"cacheReadTokens":4,"cacheWriteTokens":0}}}`,
				`{"type":"tool/call","seq":21,"time":1767225604000,"data":{"callId":"c2","name":"read","arguments":"{\"file_path\":\"/tmp/a\"}"}}`,
			))

			deltas, next2 := scan(t, src, next)
			if len(deltas) != 1 {
				t.Fatalf("got %d deltas, want 1", len(deltas))
			}
			d := deltas[0]
			if d.Msgs.Replace || d.Msgs.Value != 1 {
				t.Fatalf("incremental msgs = %+v, want additive 1", d.Msgs)
			}
			if d.Tokens.Replace || d.Tokens.In != 2 || d.Tokens.Out != 3 || d.Tokens.Cache != 4 {
				t.Fatalf("incremental tokens = %+v", d.Tokens)
			}
			if d.Tools.Replace || d.Tools.Counts["read"] != 1 {
				t.Fatalf("incremental tools = %+v", d.Tools)
			}
			if d.Title != "DSH title" {
				t.Fatalf("incremental title = %q", d.Title)
			}
			// The preview is recovered from the beginning of the log, not from
			// the newly appended prompt-less batch.
			if d.Preview != "hello dsh" {
				t.Fatalf("incremental preview = %q", d.Preview)
			}
			day := time.UnixMilli(1767225604000).Local().Format("2006-01-02")
			if d.Activity.Counts[day] != 1 {
				t.Fatalf("incremental activity = %+v", d.Activity)
			}
			if next2[path].Offset != int64(fileSize(t, path)) {
				t.Fatalf("incremental offset = %d, file size = %d", next2[path].Offset, fileSize(t, path))
			}
		})
	}
}

// A torn final frame is what a crash during a write leaves behind. The scan
// must keep the complete prefix and only advance to the last frame boundary, so
// the next scan can read the recovered frame once DSH rewrites it.
func TestScanTornFrame(t *testing.T) {
	root := t.TempDir()
	header := zstdFrame(t, headerLine)
	user := zstdFrame(t, userLine)
	assistantFrame := zstdFrame(t, assistant, toolCall)
	path := writeSession(t, root, "session-abc", true, header, user, assistantFrame[:len(assistantFrame)-5])

	src := &Source{Root: root}
	deltas, next := scan(t, src, nil)
	if len(deltas) != 1 {
		t.Fatalf("got %d deltas, want 1", len(deltas))
	}
	if deltas[0].Msgs.Value != 1 || deltas[0].Preview != "hello dsh" {
		t.Fatalf("torn prefix = %+v", deltas[0])
	}
	if want := int64(len(header) + len(user)); next[path].Offset != want {
		t.Fatalf("offset = %d, want frame boundary %d", next[path].Offset, want)
	}

	appendBytes(t, path, assistantFrame[len(assistantFrame)-5:])
	deltas, next = scan(t, src, next)
	if len(deltas) != 1 {
		t.Fatalf("recovered scan got %d deltas, want 1", len(deltas))
	}
	if deltas[0].Msgs.Value != 1 || deltas[0].Tokens.In != 10 || deltas[0].Tools.Counts["shell"] != 1 {
		t.Fatalf("recovered delta = %+v", deltas[0])
	}
	if next[path].Offset != int64(fileSize(t, path)) {
		t.Fatalf("recovered offset = %d, want %d", next[path].Offset, fileSize(t, path))
	}
}

func TestTranscript(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		name := "raw"
		frame := func(lines ...string) []byte { return rawFrame(lines...) }
		if compressed {
			name = "zstd"
			frame = func(lines ...string) []byte { return zstdFrame(t, lines...) }
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeSession(t, root, "session-abc", compressed,
				frame(headerLine),
				frame(userLine, ctxLine),
				frame(assistant),
				frame(toolCall),
				frame(`{"type":"tool/call","seq":30,"time":1767225605000,"data":{"callId":"c2","name":"read","arguments":"{}"}}`),
			)

			msgs, err := (&Source{Root: root}).Transcript("session-abc", nil)
			if err != nil {
				t.Fatalf("transcript: %v", err)
			}
			type want struct{ role, text string }
			wants := []want{
				{"user", "hello dsh"},
				{"reasoning", "think"},
				{"assistant", "hi"},
				{"tool", "[tool: bash]"},
				{"tool", "[tool: read]"},
			}
			if len(msgs) != len(wants) {
				t.Fatalf("got %d messages, want %d: %+v", len(msgs), len(wants), msgs)
			}
			for i, w := range wants {
				if msgs[i].Role != w.role || msgs[i].Text != w.text {
					t.Fatalf("msg[%d] = %+v, want %+v", i, msgs[i], w)
				}
			}
		})
	}
}

// A manual /compact appends a compaction/summary (a real model call with its
// own usage) and an injected compact-checkpoint user message. The usage counts
// toward the session; the checkpoint is neither a user turn nor transcript text.
func TestScanCompaction(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "session-abc", true,
		zstdFrame(t, headerLine),
		zstdFrame(t, userLine, assistant),
		zstdFrame(t,
			`{"type":"compaction/start","seq":20,"time":1767225603000,"data":{"compactionId":"k1"}}`,
			`{"type":"compaction/summary","seq":21,"time":1767225604000,"data":{"compactionId":"k1","summary":[{"type":"text","text":"## summary"}],"provider":"deepseek-official","model":"deepseek-pro","usage":{"inputTokens":7,"outputTokens":11,"cacheReadTokens":13}}}`,
			`{"type":"user/message","seq":22,"time":1767225604000,"data":{"content":[{"type":"text","text":"checkpoint text"}],"source":{"kind":"compact-checkpoint"}}}`,
			`{"type":"compaction/end","seq":23,"time":1767225604000,"data":{"compactionId":"k1"}}`),
	)

	deltas, _ := scan(t, &Source{Root: root}, nil)
	if len(deltas) != 1 {
		t.Fatalf("got %d deltas, want 1", len(deltas))
	}
	d := deltas[0]
	if d.Msgs.Value != 2 {
		t.Fatalf("msgs = %d, want 2 (checkpoint excluded)", d.Msgs.Value)
	}
	if d.Tokens.In != 17 || d.Tokens.Out != 16 || d.Tokens.Cache != 113 {
		t.Fatalf("tokens = %+v, want 17/16/113 (assistant + compaction)", d.Tokens)
	}
	if d.Compacts.Value != 1 || !d.Compacts.Replace {
		t.Fatalf("compacts = %+v, want 1 replace", d.Compacts)
	}

	msgs, err := (&Source{Root: root}).Transcript("session-abc", nil)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	compacts := 0
	for _, m := range msgs {
		if m.Text == "checkpoint text" {
			t.Fatalf("checkpoint leaked into the transcript: %+v", msgs)
		}
		if m.Role == "compact" {
			compacts++
			if m.Text != "## summary" {
				t.Fatalf("compact marker text = %q, want the summary", m.Text)
			}
		}
	}
	if compacts != 1 || msgs[len(msgs)-1].Role != "compact" {
		t.Fatalf("want one compact marker at the end, got %+v", msgs)
	}
}

func TestTranscriptUsesHint(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "session-abc", false, rawFrame(headerLine), rawFrame(userLine))
	// Move the log outside the root: only the hint can resolve it.
	moved := filepath.Join(t.TempDir(), "session.v4.jsonl")
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	msgs, err := (&Source{Root: filepath.Join(root, "missing")}).Transcript("session-abc", []string{moved})
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "hello dsh" {
		t.Fatalf("transcript = %+v", msgs)
	}
}

func TestScanSubagentRelation(t *testing.T) {
	root := t.TempDir()
	childHeader := `{"type":"session","version":4,"id":"session-child","createdAt":1767225600000,"cwd":"/tmp/project","parentSession":"session-parent","origin":"subagent","isSeeded":false,"delegationDepth":1}`
	writeSession(t, root, "session-child", false, rawFrame(childHeader), rawFrame(userLine))

	deltas, _ := scan(t, &Source{Root: root}, nil)
	if len(deltas) != 1 {
		t.Fatalf("got %d deltas", len(deltas))
	}
	if deltas[0].ParentID != "session-parent" || deltas[0].Relation != "subagent" {
		t.Fatalf("relation = %q/%q", deltas[0].ParentID, deltas[0].Relation)
	}
}

func TestSelectGeneration(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"session.v3.jsonl", "session.v4.jsonl", "session.v4.jsonl.zstd", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := selectGeneration(dir), filepath.Join(dir, "session.v4.jsonl.zstd"); got != want {
		t.Fatalf("selectGeneration = %q, want %q", got, want)
	}
	if err := os.Remove(filepath.Join(dir, "session.v4.jsonl.zstd")); err != nil {
		t.Fatal(err)
	}
	if got, want := selectGeneration(dir), filepath.Join(dir, "session.v4.jsonl"); got != want {
		t.Fatalf("selectGeneration after removal = %q, want %q", got, want)
	}
}

func TestReadFrame(t *testing.T) {
	one := zstdFrame(t, headerLine)
	two := zstdFrame(t, userLine)
	br := bufio.NewReader(bytes.NewReader(append(append([]byte{}, one...), two...)))
	for i, want := range [][]byte{one, two} {
		got, err := readFrame(br)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d differs", i)
		}
	}
	if _, err := readFrame(br); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}

	br = bufio.NewReader(bytes.NewReader(one[:len(one)-2]))
	if _, err := readFrame(br); err != errIncompleteFrame {
		t.Fatalf("truncated frame error = %v, want errIncompleteFrame", err)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
