// Package source defines the agent-agnostic scanning interface and the
// incremental delta produced while parsing a data source.
package source

import (
	"time"

	"github.com/akisaya/agent-session-viewer/internal/model"
)

// Cursor tracks how far a source-specific resource (usually a file) has been
// parsed, so subsequent scans only read newly appended data.
type Cursor struct {
	ID     string `json:"id"` // resolved session id, persisted so incremental scans keep a stable id
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Offset int64  `json:"offset"`
}

// Counter is a count that a source either adds to the stored value or, when
// Replace is set, declares authoritative for the resource it describes.
type Counter struct {
	Replace bool
	Value   int
}

// Usage is token accounting with the same additive/replace semantics as
// Counter. A source reports all three fields together.
type Usage struct {
	Replace        bool
	In, Out, Cache int64
}

// Amount is a money value with the same additive/replace semantics.
type Amount struct {
	Replace bool
	Value   float64
}

// Histogram is a count per key (a local day for activity, a normalized tool
// name for tool use). Replace means the map is the complete picture for the
// resource; otherwise the counts are merged into it.
type Histogram struct {
	Replace bool
	Counts  map[string]int
}

// Delta is the incremental contribution of one session from one source
// resource (usually a file).
//
// Every numeric field is expressed relative to that single resource: a
// whole-file parse reports Replace, an append reports the delta of the newly
// read bytes. The index stores each resource's contribution on its own and
// sums them into the session, so a session that spans several files (Codex
// resumes) accumulates them instead of overwriting one another.
type Delta struct {
	Key       string // stable identity of the resource (file path or session id)
	ID        string // session id
	Title     string
	Preview   string // first user message snippet, for card display
	CWD       string
	Model     string
	CreatedAt time.Time
	UpdatedAt time.Time

	Msgs     Counter
	Tokens   Usage
	Cost     Amount
	Activity Histogram
	Tools    Histogram

	// ParentID links this session to the session it continues (resume/fork).
	// Relation labels the link: "subagent" | "fork" | "resume". Empty means
	// root.
	ParentID string
	Relation string

	Cursor Cursor
}

// Source is implemented by every agent-specific parser.
type Source interface {
	// Name is the stable agent identifier: "claude" | "codex" | "opencode".
	Name() string
	// Scan walks the source and emits a Delta for every session that changed
	// since the provided cursors. It returns the full set of cursors it saw
	// (including unchanged ones) so the index can prune deleted sessions.
	Scan(prev map[string]Cursor, emit func(Delta)) (map[string]Cursor, error)
	// Transcript returns the full conversation for a session id.
	Transcript(id string) ([]model.Message, error)
}
