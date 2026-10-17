package wasmtest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/index"
	"github.com/akisaya/agent-session-viewer/internal/model"
	"github.com/akisaya/agent-session-viewer/internal/source"
	"github.com/akisaya/agent-session-viewer/internal/source/claude"
	"github.com/akisaya/agent-session-viewer/internal/source/codex"
	"github.com/akisaya/agent-session-viewer/internal/source/opencode"
)

// Sources builds the three agents rooted at home.
func Sources(home string) []source.Source {
	p := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	return []source.Source{
		&claude.Source{Root: p(ClaudeRoot)},
		&codex.Source{Root: p(CodexRoot), Index: p(CodexIndex)},
		&opencode.Source{Path: p(OpenCodePath)},
	}
}

// Run scans the tree rooted at home and renders every payload the frontend
// reads, as stable text. Two scans are performed: the second one must change
// nothing, which is what catches a non-idempotent source.
func Run(home string) (string, error) {
	ix, err := index.Open(index.Options{Memory: true}, Sources(home)...)
	if err != nil {
		return "", err
	}
	defer ix.Close()
	if err := ix.Scan(); err != nil {
		return "", err
	}

	var b strings.Builder
	stats, err := ix.Stats("", "", "")
	if err != nil {
		return "", err
	}
	writeStats(&b, "stats", stats.Total)
	for _, a := range stats.Agents {
		writeStats(&b, "stats agent "+a.Agent, a)
	}

	sessions, err := ix.Sessions(index.Filter{Limit: 500})
	if err != nil {
		return "", err
	}
	for _, s := range sessions {
		fmt.Fprintf(&b, "session %s/%s %s %s %s %s %s %s %s\n",
			s.Agent, s.ID, kv("title", s.Title), kv("preview", s.Preview), kv("cwd", s.CWD),
			kv("model", s.Model), kv("msgs", fmt.Sprint(s.MsgCount)),
			kv("tokens", fmt.Sprintf("%d/%d/%d", s.TokensIn, s.TokensOut, s.CacheRead)),
			kv("created", stamp(s.CreatedAt)))
	}

	projects, err := ix.Projects("")
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		fmt.Fprintf(&b, "project %s %s %s %s %s\n", kv("cwd", p.CWD),
			kv("count", fmt.Sprint(p.Count)), kv("messages", fmt.Sprint(p.Messages)),
			kv("tokens", fmt.Sprint(p.Tokens)), kv("cost", fmt.Sprintf("%.4f", p.Cost)))
	}

	trend, err := ix.Trend("", "", 0)
	if err != nil {
		return "", err
	}
	for _, t := range trend {
		fmt.Fprintf(&b, "trend %s %s %s %s %s\n", kv("day", t.Day),
			kv("msgs", fmt.Sprint(t.Msgs)), kv("sessions", fmt.Sprint(t.Sessions)),
			kv("tokens", fmt.Sprint(t.Tokens)), kv("cost", fmt.Sprintf("%.4f", t.Cost)))
	}

	models, err := ix.Models("", "")
	if err != nil {
		return "", err
	}
	for _, m := range models {
		fmt.Fprintf(&b, "model %s %s %s %s %s\n", kv("name", m.Model),
			kv("sessions", fmt.Sprint(m.Sessions)), kv("messages", fmt.Sprint(m.Messages)),
			kv("tokens", fmt.Sprint(m.Tokens)), kv("cost", fmt.Sprintf("%.4f", m.Cost)))
	}

	tools, err := ix.Tools("", "", 20)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "tools %s\n", kv("total", fmt.Sprint(tools.Total)))
	for _, t := range tools.Tools {
		fmt.Fprintf(&b, "tool %s %s %s\n", kv("name", t.Tool),
			kv("count", fmt.Sprint(t.Count)), kv("sessions", fmt.Sprint(t.Sessions)))
	}

	// Relations is the one query whose row order the SQL does not pin down, so
	// sort it: a golden file must not depend on which driver planned it.
	relations, err := ix.Relations("", "")
	if err != nil {
		return "", err
	}
	relLines := make([]string, 0, len(relations))
	for _, r := range relations {
		relLines = append(relLines, fmt.Sprintf("relation %s/%s %s %s",
			r.Agent, r.ID, kv("parent", r.ParentID), kv("kind", r.Relation)))
	}
	sort.Strings(relLines)
	for _, l := range relLines {
		b.WriteString(l + "\n")
	}

	// Transcripts in a fixed order.
	keys := make([]string, 0, len(sessions))
	byKey := map[string]model.Session{}
	for _, s := range sessions {
		k := s.Agent + "/" + s.ID
		keys = append(keys, k)
		byKey[k] = s
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := byKey[k]
		msgs, err := ix.Transcript(s.Agent, s.ID)
		if err != nil {
			return "", fmt.Errorf("transcript %s: %w", k, err)
		}
		fmt.Fprintf(&b, "transcript %s %s\n", k, kv("msgs", fmt.Sprint(len(msgs))))
		for _, m := range msgs {
			fmt.Fprintf(&b, "  %s %s %s\n", kv("role", m.Role), kv("ts", stamp(m.Timestamp)), kv("text", m.Text))
		}
	}

	// The pipeline must be idempotent: a rescan of unchanged inputs adds
	// nothing. A source that forgets Replace semantics shows up right here.
	if err := ix.Scan(); err != nil {
		return "", err
	}
	rescan, err := ix.Stats("", "", "")
	if err != nil {
		return "", err
	}
	writeStats(&b, "rescan", rescan.Total)
	after, err := ix.Sessions(index.Filter{Limit: 500})
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "rescan sessions=%d\n", len(after))

	return b.String(), nil
}

func writeStats(b *strings.Builder, prefix string, a model.AgentStat) {
	fmt.Fprintf(b, "%s %s %s %s %s %s %s\n", prefix,
		kv("sessions", fmt.Sprint(a.Sessions)), kv("messages", fmt.Sprint(a.Messages)),
		kv("tokens_in", fmt.Sprint(a.TokensIn)), kv("tokens_out", fmt.Sprint(a.TokensOut)),
		kv("cache_read", fmt.Sprint(a.CacheRead)), kv("cost", fmt.Sprintf("%.4f", a.Cost)))
}

// stamp renders a time in UTC, so a golden file does not depend on the local
// time zone.
func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func kv(key, value string) string { return fmt.Sprintf("%s=%s", key, value) }
