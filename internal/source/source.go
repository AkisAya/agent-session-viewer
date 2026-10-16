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

// Delta is the incremental contribution of one session since the last scan.
//
// For append-only sources (Claude, Codex) the *Delta fields are counts of what
// was parsed from the newly read bytes and the index accumulates them. For
// authoritative sources (OpenCode) Replace is true and the absolute values
// overwrite the stored record instead of being added.
type Delta struct {
	Key       string // stable identity used for cursor bookkeeping (file path or session id)
	ID        string // session id
	Title     string
	Preview   string // first user message snippet, for card display
	CWD       string
	Model     string
	CreatedAt time.Time
	UpdatedAt time.Time

	// MsgDelta is always added to the stored message count.
	MsgDelta int

	// Absolute values. Each flag switches the corresponding field from
	// additive to authoritative (overwrite) semantics.
	MsgAbsolute    bool
	MsgCount       int
	TokensAbsolute bool
	TokensIn       int64
	TokensOut      int64
	CacheRead      int64
	CostAbsolute   bool
	Cost           float64

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
