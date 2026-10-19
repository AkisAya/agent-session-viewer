// Package model defines the normalized data types shared across the app.
package model

import "time"

// Session is the normalized summary of one conversation across any agent.
type Session struct {
	Agent     string    `json:"agent"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Preview   string    `json:"preview"`
	CWD       string    `json:"cwd"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	MsgCount  int       `json:"msgCount"`
	TokensIn  int64     `json:"tokensIn"`
	TokensOut int64     `json:"tokensOut"`
	CacheRead int64     `json:"cacheRead"`
	Cost      float64   `json:"cost"`
	Compacts  int       `json:"compacts,omitempty"` // context compactions

	// ParentID is the session this one belongs to; Relation labels the link
	// ("subagent" | "fork" | "resume"). Empty ParentID means a root session.
	// A subagent session is spawned by another session and belongs with it;
	// a fork is a user-made copy, a resume continues another thread.
	ParentID string `json:"parentId,omitempty"`
	Relation string `json:"relation,omitempty"`
}

// Message is one turn of a conversation. Role is one of:
// user | assistant | reasoning | tool | system | compact.
// A "compact" message marks a context compaction; Text is the summary the
// agent carried forward (may be empty when the source does not record it).
type Message struct {
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// AgentStat aggregates metrics for one agent (or the total).
type AgentStat struct {
	Agent     string  `json:"agent"`
	Sessions  int     `json:"sessions"`
	Messages  int     `json:"messages"`
	TokensIn  int64   `json:"tokensIn"`
	TokensOut int64   `json:"tokensOut"`
	CacheRead int64   `json:"cacheRead"`
	Cost      float64 `json:"cost"`
}

// Stats is the overview payload returned by /api/stats.
type Stats struct {
	Agents []AgentStat `json:"agents"`
	Total  AgentStat   `json:"total"`
}

// Project aggregates sessions by working directory.
type Project struct {
	CWD       string         `json:"cwd"`
	Count     int            `json:"count"`
	Messages  int            `json:"messages"`
	Tokens    int64          `json:"tokens"`
	Cost      float64        `json:"cost"`
	Agents    map[string]int `json:"agents"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

// ActivityDay is the message volume of one local calendar day.
type ActivityDay struct {
	Day      string `json:"day"`
	Msgs     int64  `json:"msgs"`
	Sessions int    `json:"sessions"`
}

// ToolStat aggregates one normalized tool across sessions.
type ToolStat struct {
	Tool     string `json:"tool"`
	Count    int64  `json:"count"`
	Sessions int    `json:"sessions"`
}

// ToolStats is the /api/tools payload; Total counts every call (not just the
// returned top-N rows) so shares can be computed correctly.
type ToolStats struct {
	Tools []ToolStat `json:"tools"`
	Total int64      `json:"total"`
}

// TrendPoint is one local calendar day of activity, with the token and cost of
// each session apportioned across the days that session was active (proportional
// to that day's message share). This makes a per-day spend/token trend possible
// without storing per-day usage.
type TrendPoint struct {
	Day      string  `json:"day"`
	Msgs     int64   `json:"msgs"`
	Sessions int     `json:"sessions"`
	Tokens   int64   `json:"tokens"`
	Cost     float64 `json:"cost"`
}

// ModelStat aggregates usage per model name.
type ModelStat struct {
	Model    string  `json:"model"`
	Sessions int     `json:"sessions"`
	Messages int     `json:"messages"`
	Tokens   int64   `json:"tokens"`
	Cost     float64 `json:"cost"`
}
