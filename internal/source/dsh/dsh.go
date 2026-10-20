// Package dsh parses DeepSeek Harness (DSH) session logs stored under
// ~/.dsh/sessions/<normalized-cwd>/<session-id>/session.vN.jsonl[.zstd].
//
// A session log is append-only: the first record is a session header, followed
// by the durable SessionEvent stream. Compressed logs are a concatenation of
// independent Zstandard frames (one per durable append batch), which lets Scan
// resume at an exact frame boundary and only parse newly appended data.
package dsh

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/akisaya/agent-session-viewer/internal/model"
	"github.com/akisaya/agent-session-viewer/internal/source"
)

// Source scans a DSH sessions directory (normally ~/.dsh/sessions).
type Source struct {
	Root string
}

func (s *Source) Name() string { return "dsh" }

// generationRE matches every canonical generation filename DSH may leave in a
// session directory: session.jsonl, session.vN.jsonl and their .zstd variants.
var generationRE = regexp.MustCompile(`^session(?:\.v([0-9]+))?\.jsonl(\.zstd)?$`)

// event is the common envelope; Data keeps the type-specific payload opaque
// until we know which event it is.
type event struct {
	Type string          `json:"type"`
	Seq  int64           `json:"seq"`
	Time int64           `json:"time"` // Unix milliseconds
	Data json.RawMessage `json:"data"`
}

// header is the first physical record of a current-format log.
type header struct {
	Type            string `json:"type"`
	Version         int    `json:"version"`
	ID              string `json:"id"`
	CreatedAt       int64  `json:"createdAt"` // Unix milliseconds
	CWD             string `json:"cwd"`
	ParentSession   string `json:"parentSession"`
	IsSeeded        bool   `json:"isSeeded"`
	Origin          string `json:"origin"`
	DelegationDepth int    `json:"delegationDepth"`
	AgentPreset     string `json:"agentPreset"`
}

type contentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Name      string `json:"name"`
	ID        string `json:"id"`
	Arguments string `json:"arguments"`
}

type userData struct {
	Content []contentBlock `json:"content"`
	Source  struct {
		Kind string `json:"kind"`
	} `json:"source"`
	Role string `json:"role"`
}

type assistantData struct {
	Message struct {
		Content []contentBlock `json:"content"`
		Source  struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"source"`
	} `json:"message"`
	Usage *usage `json:"usage"`
}

type toolCallData struct {
	CallID    string `json:"callId"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type titleData struct {
	Title string `json:"title"`
}

type requestContext struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type compactionData struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Usage    *usage `json:"usage"`
}

// usage is the per-call token accounting DSH records on assistant messages.
// inputTokens is the uncached prompt size; cacheReadTokens is the cached part.
type usage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

// Scan walks the sessions tree. Session files live exactly three levels deep:
// <root>/<project>/<session-dir>/session[.vN].jsonl[.zstd]
func (s *Source) Scan(prev map[string]source.Cursor, emit func(source.Delta)) (map[string]source.Cursor, error) {
	next := map[string]source.Cursor{}
	projects, err := os.ReadDir(s.Root)
	if err != nil {
		return next, nil // missing DSH installation is not fatal
	}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		projectDir := filepath.Join(s.Root, project.Name())
		sessions, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, session := range sessions {
			if !session.IsDir() {
				continue
			}
			path := selectGeneration(filepath.Join(projectDir, session.Name()))
			if path == "" {
				continue
			}
			s.scanFile(path, session.Name(), prev[path], emit, next)
		}
	}
	return next, nil
}

// selectGeneration returns the numerically highest canonical generation file in
// a session directory, preferring the compressed artifact when both encodings
// exist for the same version. Later versions supersede earlier ones, matching
// DSH's own runtime selection.
func selectGeneration(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	bestVersion := -1
	bestCompressed := false
	best := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := generationRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		version := 0
		if m[1] != "" {
			if v, err := strconv.Atoi(m[1]); err == nil {
				version = v
			}
		}
		compressed := m[2] != ""
		better := version > bestVersion || (version == bestVersion && compressed && !bestCompressed)
		if better {
			bestVersion = version
			bestCompressed = compressed
			best = filepath.Join(dir, e.Name())
		}
	}
	return best
}

// scanFile folds one session log into a Delta for the bytes appended since the
// stored cursor. A full parse replaces the resource's totals; an append adds
// only the newly read records.
func (s *Source) scanFile(path, dirName string, cur source.Cursor, emit func(source.Delta), next map[string]source.Cursor) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()
	if cur.Size == size && cur.Mtime == mtime {
		next[path] = cur
		return
	}

	offset := cur.Offset
	if offset > size {
		offset = 0 // rewritten or truncated
	}
	full := offset == 0

	acc := newAccumulator()
	nrecords := 0
	var newOffset int64
	if full {
		newOffset, _ = forEachRecord(path, 0, func(raw []byte) error {
			nrecords++
			acc.apply(raw, true)
			return nil
		})
	} else {
		h, err := readHeader(path)
		if err != nil {
			return
		}
		acc.header.merge(h)
		// The session's first user prompt only lives near the beginning of the
		// file. Re-reading that bounded prefix keeps the card preview accurate
		// even when the first scan saw a header-only log.
		acc.firstUser = readFirstUser(path)
		newOffset, _ = forEachRecord(path, offset, func(raw []byte) error {
			nrecords++
			acc.apply(raw, false)
			return nil
		})
		if newOffset < offset {
			newOffset = offset
		}
	}

	id := acc.header.ID
	if id == "" {
		id = dirName
	}
	if id == "" {
		return
	}
	cursor := source.Cursor{ID: id, Size: size, Mtime: mtime, Offset: newOffset}
	if !full && nrecords == 0 {
		next[path] = cursor
		return
	}

	d := source.Delta{
		Key:       path,
		ID:        id,
		Title:     acc.title,
		Preview:   source.Truncate(acc.firstUser, 200),
		CWD:       acc.header.CWD,
		Model:     acc.model,
		CreatedAt: msTime(acc.header.CreatedAt),
		UpdatedAt: msTime(acc.maxT),
		Msgs:      source.Counter{Value: acc.msgs},
		Compacts:  source.Counter{Value: acc.compacts},
		Tokens:    source.Usage{In: acc.tin, Out: acc.tout, Cache: acc.cache},
		Activity:  source.Histogram{Counts: acc.activity},
		Hours:     source.Histogram{Counts: acc.hours},
		Tools:     source.Histogram{Counts: acc.tools},
		Cursor:    cursor,
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = msTime(acc.minT)
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = d.CreatedAt
	}
	if full && d.Title == "" {
		d.Title = source.Truncate(acc.firstUser, 80)
	}
	if acc.header.ParentSession != "" || acc.header.Origin == "subagent" {
		d.ParentID = acc.header.ParentSession
		d.Relation = "subagent"
	}
	if full {
		// A whole-file parse is authoritative for this resource.
		d.Msgs.Replace = true
		d.Compacts.Replace = true
		d.Tokens.Replace = true
		d.Cost.Replace = true
		d.Activity.Replace = true
		d.Hours.Replace = true
		d.Tools.Replace = true
	}

	emit(d)
	next[path] = cursor
}

// accumulator collects the fields of one resource's contribution.
type accumulator struct {
	header    header
	title     string
	model     string
	firstUser string
	minT      int64
	maxT      int64

	msgs     int
	compacts int
	tin      int64
	tout     int64
	cache    int64
	activity map[string]int
	hours    map[string]int
	tools    map[string]int
}

func newAccumulator() *accumulator {
	return &accumulator{activity: map[string]int{}, hours: map[string]int{}, tools: map[string]int{}}
}

// apply folds one JSONL record into the accumulator. full is true on a
// whole-file parse, where the first user message is authoritative.
func (a *accumulator) apply(raw []byte, full bool) {
	var e event
	if json.Unmarshal(raw, &e) != nil {
		return
	}
	if e.Type == "session" {
		var h header
		if json.Unmarshal(raw, &h) == nil {
			a.header.merge(h)
		}
		return
	}
	if e.Time > 0 {
		if a.minT == 0 || e.Time < a.minT {
			a.minT = e.Time
		}
		if e.Time > a.maxT {
			a.maxT = e.Time
		}
	}

	switch e.Type {
	case "user/message":
		var d userData
		if json.Unmarshal(e.Data, &d) != nil || !isRealUser(d) {
			return
		}
		text := contentText(d.Content)
		if text == "" {
			return
		}
		if full && a.firstUser == "" {
			a.firstUser = text
		}
		a.msgs++
		a.addDay(e.Time)

	case "assistant/message":
		var d assistantData
		if json.Unmarshal(e.Data, &d) != nil {
			return
		}
		if model := qualifyModel(d.Message.Source.Provider, d.Message.Source.Model); model != "" {
			a.model = model
		}
		if d.Usage != nil {
			a.tin += d.Usage.InputTokens
			a.tout += d.Usage.OutputTokens
			a.cache += d.Usage.CacheReadTokens
		}
		a.msgs++
		a.addDay(e.Time)

	case "tool/call":
		var d toolCallData
		if json.Unmarshal(e.Data, &d) == nil && d.Name != "" {
			a.tools[source.ToolName(d.Name)]++
		}

	case "session/title":
		var d titleData
		if json.Unmarshal(e.Data, &d) == nil && strings.TrimSpace(d.Title) != "" {
			a.title = strings.TrimSpace(d.Title)
		}

	case "request/context":
		var d requestContext
		if json.Unmarshal(e.Data, &d) == nil {
			if model := qualifyModel(d.Provider, d.Model); model != "" {
				a.model = model
			}
		}

	case "compaction/summary":
		// Compaction is a real model call: count its usage so the session's
		// token totals match DSH's own accounting.
		a.compacts++
		var d compactionData
		if json.Unmarshal(e.Data, &d) == nil {
			if model := qualifyModel(d.Provider, d.Model); model != "" {
				a.model = model
			}
			if d.Usage != nil {
				a.tin += d.Usage.InputTokens
				a.tout += d.Usage.OutputTokens
				a.cache += d.Usage.CacheReadTokens
			}
		}
	}
}

func (a *accumulator) addDay(ms int64) {
	if ms <= 0 {
		return
	}
	t := time.UnixMilli(ms).Local()
	a.activity[t.Format("2006-01-02")]++
	a.hours[t.Format("2006-01-02 15")]++
}

func (h *header) merge(other header) {
	if other.ID != "" {
		h.ID = other.ID
	}
	if other.Version != 0 {
		h.Version = other.Version
	}
	if other.CreatedAt != 0 {
		h.CreatedAt = other.CreatedAt
	}
	if other.CWD != "" {
		h.CWD = other.CWD
	}
	if other.ParentSession != "" {
		h.ParentSession = other.ParentSession
	}
	if other.Origin != "" {
		h.Origin = other.Origin
	}
	if other.DelegationDepth != 0 {
		h.DelegationDepth = other.DelegationDepth
	}
	if other.AgentPreset != "" {
		h.AgentPreset = other.AgentPreset
	}
}

// readHeader decodes the first physical record of a log.
func readHeader(path string) (header, error) {
	var zero header
	f, err := os.Open(path)
	if err != nil {
		return zero, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)

	if !isCompressed(path) {
		for {
			line, err := br.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) == 0 {
				if err != nil {
					return zero, io.ErrUnexpectedEOF
				}
				continue
			}
			var h header
			if json.Unmarshal(bytes.TrimSpace(line), &h) == nil && h.Type == "session" {
				return h, nil
			}
			return zero, errors.New("dsh: first record is not a session header")
		}
	}

	frame, err := readFrame(br)
	if err != nil {
		return zero, err
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return zero, err
	}
	defer dec.Close()
	data, err := dec.DecodeAll(frame, nil)
	if err != nil {
		return zero, err
	}
	var h header
	err = forEachLine(data, func(line []byte) error {
		var candidate header
		if json.Unmarshal(line, &candidate) == nil && candidate.Type == "session" {
			h = candidate
			return errStop
		}
		return nil
	})
	if err != errStop {
		if err != nil {
			return zero, err
		}
		return zero, errors.New("dsh: compressed header frame is empty")
	}
	return h, nil
}

// errStop is an internal sentinel used to stop a record walk early.
var errStop = errors.New("dsh: stop")

// prelude limits keep a scan from walking an entire large log just to find a
// preview that normally lives in the first few append batches.
const (
	preludeMaxFrames = 128
	preludeMaxBytes  = 8 << 20
	preludeMaxLines  = 2000
)

// readFirstUser returns the first genuine user prompt, skipping the injected
// runtime-context / skill-catalog / time-context user messages DSH records.
func readFirstUser(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)

	if !isCompressed(path) {
		read := 0
		for i := 0; i < preludeMaxLines && read < preludeMaxBytes; i++ {
			line, err := br.ReadBytes('\n')
			read += len(line)
			if text, ok := firstUserInLine(bytes.TrimSpace(line)); ok {
				return text
			}
			if err != nil {
				break
			}
		}
		return ""
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		return ""
	}
	defer dec.Close()
	read := 0
	for i := 0; i < preludeMaxFrames && read < preludeMaxBytes; i++ {
		frame, err := readFrame(br)
		if err != nil {
			break
		}
		read += len(frame)
		data, err := dec.DecodeAll(frame, nil)
		if err != nil {
			break
		}
		found := ""
		_ = forEachLine(data, func(line []byte) error {
			if text, ok := firstUserInLine(line); ok {
				found = text
				return errStop
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

func firstUserInLine(line []byte) (string, bool) {
	var e event
	if json.Unmarshal(line, &e) != nil || e.Type != "user/message" {
		return "", false
	}
	var d userData
	if json.Unmarshal(e.Data, &d) != nil || !isRealUser(d) {
		return "", false
	}
	text := contentText(d.Content)
	return text, text != ""
}

// isRealUser reports whether a user/message was authored by a human, as opposed
// to one of DSH's injected context messages.
func isRealUser(d userData) bool {
	kind := strings.TrimSpace(d.Source.Kind)
	return kind == "" || kind == "user"
}

// contentText returns the displayable text of a content block list, with
// placeholders for attachments that have no text.
func contentText(blocks []contentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, t)
			}
		case "image":
			parts = append(parts, "[image]")
		case "file":
			parts = append(parts, "[file]")
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func qualifyModel(provider, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	provider = strings.TrimSpace(provider)
	if provider == "" || strings.Contains(model, "/") {
		return model
	}
	return provider + "/" + model
}

func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// Transcript loads the full conversation for a session id. hints are the file
// paths the index recorded; a source may use them to avoid walking the tree.
func (s *Source) Transcript(id string, hints []string) ([]model.Message, error) {
	path, err := s.locate(id, hints)
	if err != nil {
		return nil, err
	}
	var out []model.Message
	seenCalls := map[string]bool{}
	_, err = forEachRecord(path, 0, func(raw []byte) error {
		var e event
		if json.Unmarshal(raw, &e) != nil {
			return nil
		}
		ts := msTime(e.Time)
		switch e.Type {
		case "user/message":
			var d userData
			if json.Unmarshal(e.Data, &d) != nil || !isRealUser(d) {
				return nil
			}
			if text := contentText(d.Content); text != "" {
				out = append(out, model.Message{Role: "user", Text: text, Timestamp: ts})
			}

		case "assistant/message":
			var d assistantData
			if json.Unmarshal(e.Data, &d) != nil {
				return nil
			}
			for _, b := range d.Message.Content {
				switch b.Type {
				case "text":
					if t := strings.TrimSpace(b.Text); t != "" {
						out = append(out, model.Message{Role: "assistant", Text: t, Timestamp: ts})
					}
				case "reasoning":
					if t := strings.TrimSpace(b.Text); t != "" {
						out = append(out, model.Message{Role: "reasoning", Text: t, Timestamp: ts})
					}
				case "tool-call":
					if b.Name != "" && (b.ID == "" || !seenCalls[b.ID]) {
						if b.ID != "" {
							seenCalls[b.ID] = true
						}
						out = append(out, model.Message{Role: "tool", Text: "[tool: " + b.Name + "]", Timestamp: ts})
					}
				}
			}

		case "compaction/summary":
			var d struct {
				Summary []contentBlock `json:"summary"`
			}
			if json.Unmarshal(e.Data, &d) == nil {
				out = append(out, model.Message{Role: "compact", Text: contentText(d.Summary), Timestamp: ts})
			}

		case "tool/call":
			// Normally the call is already visible as an assistant content
			// block; this fallback covers calls recorded without one.
			var d toolCallData
			if json.Unmarshal(e.Data, &d) != nil || d.Name == "" || seenCalls[d.CallID] {
				return nil
			}
			if d.CallID != "" {
				seenCalls[d.CallID] = true
			}
			out = append(out, model.Message{Role: "tool", Text: "[tool: " + d.Name + "]", Timestamp: ts})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// locate resolves a session's log, preferring a path the index already
// recorded and falling back to a tree walk by session directory or header id.
func (s *Source) locate(id string, hints []string) (string, error) {
	for _, h := range hints {
		if info, err := os.Stat(h); err == nil && !info.IsDir() {
			return h, nil
		}
	}
	projects, err := os.ReadDir(s.Root)
	if err != nil {
		return "", os.ErrNotExist
	}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		projectDir := filepath.Join(s.Root, project.Name())
		sessions, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, session := range sessions {
			if !session.IsDir() {
				continue
			}
			path := selectGeneration(filepath.Join(projectDir, session.Name()))
			if path == "" {
				continue
			}
			if session.Name() == id {
				return path, nil
			}
			if h, err := readHeader(path); err == nil && h.ID == id {
				return path, nil
			}
		}
	}
	return "", os.ErrNotExist
}
