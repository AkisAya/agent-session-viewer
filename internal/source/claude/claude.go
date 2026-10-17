// Package claude parses Claude Code session transcripts stored under
// ~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl
package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/model"
	"github.com/akisaya/agent-session-viewer/internal/source"
)

// Source scans the Claude Code projects directory.
type Source struct {
	Root string // ~/.claude/projects
}

func (s *Source) Name() string { return "claude" }

type entry struct {
	Type        string          `json:"type"`
	Timestamp   string          `json:"timestamp"`
	SessionID   string          `json:"sessionId"`
	CWD         string          `json:"cwd"`
	IsSidechain bool            `json:"isSidechain"`
	Message     json.RawMessage `json:"message"`
	AiTitle     string          `json:"aiTitle"`
	CustomTitle string          `json:"customTitle"`
	LastPrompt  string          `json:"lastPrompt"`
	Summary     string          `json:"summary"`
	TotalCost   float64         `json:"totalCostUSD"`
	ModelUsage  map[string]struct {
		InputTokens              int64   `json:"inputTokens"`
		OutputTokens             int64   `json:"outputTokens"`
		CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
		CostUSD                  float64 `json:"costUSD"`
	} `json:"modelUsage"`
}

type message struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

type block struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	Name     string `json:"name"`
}

// Scan walks the projects directory. Session files live exactly two levels
// deep: <root>/<encoded-cwd>/<sessionId>.jsonl
func (s *Source) Scan(prev map[string]source.Cursor, emit func(source.Delta)) (map[string]source.Cursor, error) {
	next := map[string]source.Cursor{}
	projects, err := os.ReadDir(s.Root)
	if err != nil {
		return next, nil // missing source is not fatal
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(s.Root, p.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, f.Name())
			s.scanFile(path, prev[path], emit, next)
		}
	}
	return next, nil
}

func (s *Source) scanFile(path string, cur source.Cursor, emit func(source.Delta), next map[string]source.Cursor) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()

	offset := cur.Offset
	if size < offset { // file was rewritten/truncated
		offset = 0
	}
	if cur.Size == size && cur.Mtime == mtime {
		next[path] = cur // unchanged
		return
	}

	lines, newOffset, err := source.ReadNewLines(path, offset)
	if err != nil {
		return
	}

	d := source.Delta{
		Key:    path,
		ID:     strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Cursor: source.Cursor{Size: size, Mtime: mtime, Offset: newOffset},
	}
	full := offset == 0

	var (
		customTitle, aiTitle, lastPrompt, summary string
		firstUser                                 string
		firstCWD                                  string
		modelName                                 string
		minT, maxT                                time.Time
		ts                                        time.Time
		cost                                      float64
		sawCost                                   bool
	)
	act := map[string]int{}
	tools := map[string]int{}

	for _, raw := range lines {
		var e entry
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		if e.SessionID != "" {
			d.ID = e.SessionID
		}
		if t := parseTime(e.Timestamp); !t.IsZero() {
			ts = t
			if minT.IsZero() || t.Before(minT) {
				minT = t
			}
			if t.After(maxT) {
				maxT = t
			}
		}
		switch e.Type {
		case "custom-title":
			if e.CustomTitle != "" {
				customTitle = e.CustomTitle
			}
		case "ai-title":
			if e.AiTitle != "" {
				aiTitle = e.AiTitle
			}
		case "last-prompt":
			if e.LastPrompt != "" {
				lastPrompt = e.LastPrompt
			}
		case "summary":
			if e.Summary != "" {
				summary = e.Summary
			}
		case "cost-state":
			sawCost = true
			if e.TotalCost > 0 {
				cost = e.TotalCost
			} else {
				cost = 0
				for _, mu := range e.ModelUsage {
					cost += mu.CostUSD
				}
			}
		case "user", "assistant":
			var m message
			if json.Unmarshal(e.Message, &m) == nil {
				if m.Model != "" {
					modelName = m.Model
				}
				if m.Usage != nil {
					d.Tokens.In += m.Usage.InputTokens
					d.Tokens.Out += m.Usage.OutputTokens
					d.Tokens.Cache += m.Usage.CacheReadInputTokens
				}
				if !ts.IsZero() {
					act[ts.Local().Format("2006-01-02")]++
				}
				// Tool calls live in the assistant content blocks; tool
				// results are separate user blocks and must not be counted.
				if b := blocksOf(m.Content); b != nil {
					for _, blk := range b {
						if blk.Type == "tool_use" && blk.Name != "" {
							tools[source.ToolName(blk.Name)]++
						}
					}
				}
			}
			d.Msgs.Value++
			if e.Type == "user" && !e.IsSidechain {
				if txt := plainText(e.Message); txt != "" && firstUser == "" {
					firstUser = txt
				}
			}
		}
		if e.CWD != "" && firstCWD == "" {
			firstCWD = e.CWD
		}
	}

	d.Title = source.FirstText(customTitle, aiTitle, lastPrompt, summary)
	if d.Title == "" && full {
		d.Title = source.Truncate(firstUser, 80)
	}
	// Preview is the session's first user message, which only the beginning of
	// the file holds. An incremental parse of appended bytes must not overwrite
	// it with a later prompt.
	if full {
		d.Preview = source.Truncate(firstUser, 200)
	}
	d.CWD = firstCWD
	d.Model = modelName
	d.CreatedAt = minT
	d.UpdatedAt = maxT
	d.Activity = source.Histogram{Counts: act}
	d.Tools = source.Histogram{Counts: tools}
	if full {
		// A whole-file parse is authoritative for this resource: it may
		// overwrite values left behind by a previous run (e.g. after the file
		// was rewritten).
		d.Activity.Replace = true
		d.Tools.Replace = true
		d.Msgs.Replace = true
		d.Tokens.Replace = true
	}
	if sawCost {
		d.Cost = source.Amount{Replace: true, Value: cost}
	}

	if d.ID != "" {
		d.Cursor.ID = d.ID
		emit(d)
		next[path] = d.Cursor
	}
}

// plainText extracts human-readable text from a Claude message payload.
func plainText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m message
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	return contentText(m.Content)
}

// contentText returns the concatenated plain text of a message's content.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// Transcript loads the full conversation for a session id, emitting separate
// messages for user/assistant text, model reasoning and tool calls.
func (s *Source) Transcript(id string) ([]model.Message, error) {
	path, err := s.find(id)
	if err != nil {
		return nil, err
	}
	lines, err := source.ReadAllLines(path)
	if err != nil {
		return nil, err
	}
	var out []model.Message
	for _, raw := range lines {
		var e entry
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		if (e.Type != "user" && e.Type != "assistant") || e.IsSidechain {
			continue
		}
		var m message
		if json.Unmarshal(e.Message, &m) != nil {
			continue
		}
		ts := parseTime(e.Timestamp)
		if content := blocksOf(m.Content); content != nil {
			for _, b := range content {
				role, text := blockMessage(e.Type, b)
				if text != "" {
					out = append(out, model.Message{Role: role, Text: text, Timestamp: ts})
				}
			}
			continue
		}
		if text := contentText(m.Content); text != "" {
			out = append(out, model.Message{Role: e.Type, Text: text, Timestamp: ts})
		}
	}
	return out, nil
}

// blocksOf returns the content blocks when content is an array, else nil.
func blocksOf(raw json.RawMessage) []block {
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}

func blockMessage(role string, b block) (string, string) {
	switch b.Type {
	case "text":
		return role, strings.TrimSpace(b.Text)
	case "thinking", "redacted_thinking":
		return "reasoning", strings.TrimSpace(b.Thinking)
	case "tool_use":
		if b.Name != "" {
			return "tool", "[tool: " + b.Name + "]"
		}
	}
	return "", ""
}

func (s *Source) find(id string) (string, error) {
	projects, err := os.ReadDir(s.Root)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		path := filepath.Join(s.Root, p.Name(), id+".jsonl")
		if _, err := os.Stat(path); err == nil {
			matches = append(matches, path)
		}
	}
	if len(matches) == 0 {
		return "", os.ErrNotExist
	}
	sort.Strings(matches)
	return matches[0], nil
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
