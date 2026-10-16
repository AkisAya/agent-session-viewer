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
}

// Message is one turn of a conversation. Role is one of:
// user | assistant | reasoning | tool | system.
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
	CWD       string    `json:"cwd"`
	Count     int       `json:"count"`
	Messages  int       `json:"messages"`
	Tokens    int64     `json:"tokens"`
	UpdatedAt time.Time `json:"updatedAt"`
}
