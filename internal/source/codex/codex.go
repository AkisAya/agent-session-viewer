// Package codex parses Codex rollout transcripts stored under
// ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
package codex

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

// Source scans the Codex sessions tree.
type Source struct {
	Root  string // ~/.codex/sessions
	Index string // ~/.codex/session_index.jsonl
}

func (s *Source) Name() string { return "codex" }

type line struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type meta struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	ModelProvider  string `json:"model_provider"`
	Timestamp      string `json:"timestamp"`
	ParentThreadID string `json:"parent_thread_id"`
}

type usage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
}

type eventMsg struct {
	Type string `json:"type"`
	Info *struct {
		TotalTokenUsage *usage `json:"total_token_usage"`
	} `json:"info"`
	Item *item `json:"item"`
}

type item struct {
	Type        string          `json:"type"`
	Content     json.RawMessage `json:"content"`
	SummaryText []string        `json:"summary_text"`
	Command     []string        `json:"command"`
	Server      string          `json:"server"`
	Tool        string          `json:"tool"`
	Name        string          `json:"name"`
}

type tokenRecord struct {
	Usage *usage `json:"usage"`
}

// responseItem is the payload of a "response_item" line, used as a fallback
// for older/short rollouts that contain no event_msg conversation items.
type responseItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Name    string          `json:"name"`
	Content json.RawMessage `json:"content"`
}

func respText(ri *responseItem) string {
	if len(ri.Content) == 0 || ri.Content[0] != '[' {
		return ""
	}
	var arr []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(ri.Content, &arr) != nil {
		return ""
	}
	var parts []string
	for _, c := range arr {
		if t := strings.TrimSpace(c.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

// syntheticPrefixes are injected context blocks that should not be treated as
// real user prompts (used when falling back to response_item messages).
var syntheticPrefixes = []string{
	"<environment_context", "<permissions", "<user_instructions",
	"<turn_aborted", "<ENVIRONMENT", "<system", "<repo_instructions",
	"<workspace", "<command",
}

func isSynthetic(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range syntheticPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// Scan walks the sessions tree, applying the session index for titles.
func (s *Source) Scan(prev map[string]source.Cursor, emit func(source.Delta)) (map[string]source.Cursor, error) {
	next := map[string]source.Cursor{}
	titles := s.loadIndex()
	_ = filepath.WalkDir(s.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		s.scanFile(path, prev[path], titles, emit, next)
		return nil
	})
	return next, nil
}

func (s *Source) scanFile(path string, cur source.Cursor, titles map[string]string, emit func(source.Delta), next map[string]source.Cursor) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()

	offset := cur.Offset
	if size < offset || cur.ID == "" { // rewritten, or id not yet resolved
		offset = 0
	}
	if cur.Size == size && cur.Mtime == mtime && cur.ID != "" {
		next[path] = cur
		return
	}

	lines, newOffset, err := source.ReadNewLines(path, offset)
	if err != nil {
		return
	}

	d := source.Delta{
		Key: path,
		ID:  source.FirstText(cur.ID, idFromName(filepath.Base(path))),
		Cursor: source.Cursor{
			Size: size, Mtime: mtime, Offset: newOffset,
		},
	}
	full := offset == 0

	var (
		metaID, cwd, provider, modelName string
		firstUser, respUser              string
		parentThread                     string
		minT, maxT                       time.Time
		sawCumulative                    bool
		cumulative                       usage
		additive                         usage
		sawAdditive                      bool
	)
	// The two streams describe the same conversation; each is accumulated on
	// its own and the richer one is chosen after the loop to avoid double
	// counting. Message timestamps are bucketed into local days for the
	// activity heatmap.
	evAct, respAct := map[string]int{}, map[string]int{}
	evTools, respTools := map[string]int{}, map[string]int{}

	for _, raw := range lines {
		var l line
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		ts := parseTime(l.Timestamp)
		if !ts.IsZero() {
			if minT.IsZero() || ts.Before(minT) {
				minT = ts
			}
			if ts.After(maxT) {
				maxT = ts
			}
		}
		switch l.Type {
		case "session_meta":
			var m meta
			if json.Unmarshal(l.Payload, &m) == nil {
				if m.SessionID != "" {
					metaID = m.SessionID
				}
				cwd = source.FirstText(cwd, m.CWD)
				provider = source.FirstText(provider, m.ModelProvider)
				parentThread = source.FirstText(parentThread, m.ParentThreadID)
			}
		case "turn_context":
			var tc struct {
				Model string `json:"model"`
				CWD   string `json:"cwd"`
			}
			if json.Unmarshal(l.Payload, &tc) == nil {
				if tc.Model != "" {
					modelName = tc.Model
				}
				cwd = source.FirstText(cwd, tc.CWD)
			}
		case "event_msg":
			var ev eventMsg
			if json.Unmarshal(l.Payload, &ev) != nil {
				continue
			}
			switch ev.Type {
			case "token_count":
				if ev.Info != nil && ev.Info.TotalTokenUsage != nil {
					sawCumulative = true
					cumulative = *ev.Info.TotalTokenUsage
				}
			case "item_completed":
				if ev.Item == nil {
					continue
				}
				switch ev.Item.Type {
				case "UserMessage", "AgentMessage":
					d.Msgs.Value++
					if !ts.IsZero() {
						evAct[ts.Local().Format("2006-01-02")]++
					}
					if ev.Item.Type == "UserMessage" && firstUser == "" {
						firstUser = itemText(ev.Item)
					}
				case "CommandExecution":
					evTools["shell"]++
				case "WebSearch":
					evTools["websearch"]++
				case "McpToolCall":
					evTools[mcpToolName(ev.Item)]++
				case "FunctionCall", "CustomToolCall":
					evTools[source.ToolName(source.FirstText(ev.Item.Name, ev.Item.Tool))]++
				}
			}
		case "token_usage_record":
			var tr tokenRecord
			if json.Unmarshal(l.Payload, &tr) == nil && tr.Usage != nil {
				sawAdditive = true
				additive.InputTokens += tr.Usage.InputTokens
				additive.CachedInputTokens += tr.Usage.CachedInputTokens
				additive.OutputTokens += tr.Usage.OutputTokens
			}
		case "response_item":
			var ri responseItem
			if json.Unmarshal(l.Payload, &ri) != nil {
				continue
			}
			switch {
			case ri.Type == "message" && (ri.Role == "user" || ri.Role == "assistant"):
				if !ts.IsZero() {
					respAct[ts.Local().Format("2006-01-02")]++
				}
				if ri.Role == "user" {
					if t := respText(&ri); t != "" && !isSynthetic(t) && respUser == "" {
						respUser = t
					}
				}
			case ri.Type == "function_call" || ri.Type == "custom_tool_call":
				respTools[source.ToolName(ri.Name)]++
			case ri.Type == "web_search_call":
				respTools["websearch"]++
			}
		}
	}

	if metaID != "" {
		d.ID = metaID
	}
	if parentThread != "" && parentThread != d.ID {
		// The rollout continues another thread: treat it as a resumed session.
		d.ParentID = parentThread
		d.Relation = "resume"
	}
	d.CWD = cwd
	d.Model = source.FirstText(modelName, provider)

	// Prefer the event stream for messages and the response stream for tools:
	// it records every function call (shell, patch, plan) while the event
	// stream only surfaces a subset of them.
	act, tools := evAct, respTools
	if sumCounts(evAct) == 0 {
		act = respAct
	}
	if len(respTools) == 0 {
		tools = evTools
	}
	d.Activity = source.Histogram{Counts: act}
	d.Tools = source.Histogram{Counts: tools}
	if full {
		// A whole-file parse is authoritative for this resource.
		d.Activity.Replace = true
		d.Tools.Replace = true
		if sumCounts(evAct) == 0 && sumCounts(respAct) > 0 {
			// Only the response stream carries conversation items here, and
			// this parse saw the whole file, so it is authoritative.
			d.Msgs = source.Counter{Replace: true, Value: sumCounts(respAct)}
		}
	}

	switch {
	case sawCumulative:
		d.Tokens = source.Usage{Replace: true,
			In: cumulative.InputTokens, Out: cumulative.OutputTokens, Cache: cumulative.CachedInputTokens}
	case sawAdditive:
		d.Tokens.In = additive.InputTokens
		d.Tokens.Out = additive.OutputTokens
		d.Tokens.Cache = additive.CachedInputTokens
	}

	d.Title = titles[d.ID]
	prompt := source.FirstText(firstUser, respUser)
	if d.Title == "" && full {
		d.Title = source.Truncate(prompt, 80)
	}
	// Preview is the session's first user message: only a full parse sees it,
	// so an incremental delta must leave the stored preview untouched.
	if full {
		d.Preview = source.Truncate(prompt, 200)
	}
	d.CreatedAt = minT
	d.UpdatedAt = maxT
	d.Cursor.ID = d.ID

	if d.ID != "" {
		emit(d)
		next[path] = d.Cursor
	}
}

func (s *Source) loadIndex() map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(s.Index)
	if err != nil {
		return out
	}
	for _, raw := range strings.Split(string(data), "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var e struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
		}
		if json.Unmarshal([]byte(raw), &e) == nil && e.ID != "" && e.ThreadName != "" {
			out[e.ID] = e.ThreadName
		}
	}
	return out
}

// Transcript loads the full conversation for a session id. A session may span
// multiple rollout files (resumed sessions), so all matching files are merged
// in timestamp order.
func (s *Source) Transcript(id string) ([]model.Message, error) {
	paths, err := s.find(id)
	if err != nil {
		return nil, err
	}
	var evOut, respOut []model.Message
	for _, path := range paths {
		lines, err := source.ReadAllLines(path)
		if err != nil {
			continue
		}
		for _, raw := range lines {
			var l line
			if json.Unmarshal(raw, &l) != nil {
				continue
			}
			ts := parseTime(l.Timestamp)
			switch l.Type {
			case "event_msg":
				var ev eventMsg
				if json.Unmarshal(l.Payload, &ev) != nil || ev.Type != "item_completed" || ev.Item == nil {
					continue
				}
				role, text := codexItemMessage(ev.Item)
				if text != "" {
					evOut = append(evOut, model.Message{Role: role, Text: text, Timestamp: ts})
				}
			case "response_item":
				var ri responseItem
				if json.Unmarshal(l.Payload, &ri) != nil || ri.Type != "message" {
					continue
				}
				if ri.Role != "user" && ri.Role != "assistant" {
					continue
				}
				text := respText(&ri)
				if text == "" || (ri.Role == "user" && isSynthetic(text)) {
					continue
				}
				respOut = append(respOut, model.Message{Role: ri.Role, Text: text, Timestamp: ts})
			}
		}
	}
	out := evOut
	if len(out) == 0 {
		out = respOut // older rollouts only carry response_item messages
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}

// codexItemMessage maps an event item to a display role and text.
func codexItemMessage(it *item) (role, text string) {
	switch it.Type {
	case "UserMessage":
		return "user", itemText(it)
	case "AgentMessage":
		return "assistant", itemText(it)
	case "Reasoning":
		return "reasoning", strings.TrimSpace(strings.Join(it.SummaryText, "\n"))
	case "CommandExecution":
		if len(it.Command) > 0 {
			return "tool", "$ " + strings.Join(it.Command, " ")
		}
		return "tool", "[command]"
	case "McpToolCall":
		name := source.FirstText(it.Tool, "mcp")
		if it.Server != "" {
			name = it.Server + "." + name
		}
		return "tool", "[tool: " + name + "]"
	case "WebSearch":
		return "tool", "[web search]"
	}
	return "", ""
}

// mcpToolName renders an MCP tool call as "mcp:<server>.<tool>".
func mcpToolName(it *item) string {
	name := source.FirstText(it.Tool, it.Name, "tool")
	if it.Server != "" {
		return "mcp:" + it.Server + "." + name
	}
	return "mcp:" + name
}

// sumCounts totals a per-key counter map.
func sumCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (s *Source) find(id string) ([]string, error) {
	var found []string
	_ = filepath.WalkDir(s.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") && idFromName(filepath.Base(path)) == id {
			found = append(found, path)
		}
		return nil
	})
	if len(found) == 0 {
		return nil, os.ErrNotExist
	}
	sort.Strings(found)
	return found, nil
}

// itemText extracts the concatenated text of an event item.
func itemText(it *item) string {
	if len(it.Content) == 0 {
		return ""
	}
	var arr []map[string]any
	if json.Unmarshal(it.Content, &arr) != nil {
		return ""
	}
	var parts []string
	for _, m := range arr {
		for _, key := range []string{"text", "Text"} {
			if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
				parts = append(parts, v)
				break
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// idFromName returns the UUID at the end of a rollout filename.
func idFromName(name string) string {
	name = strings.TrimSuffix(name, ".jsonl")
	if len(name) >= 36 {
		return name[len(name)-36:]
	}
	return name
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
