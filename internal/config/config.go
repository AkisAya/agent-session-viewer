// Package config resolves runtime paths and options.
package config

import (
	"os"
	"path/filepath"
)

// Config holds runtime options. Zero-value fields are filled with defaults
// by Resolve.
type Config struct {
	Port         int
	DBPath       string
	ClaudeDir    string
	CodexDir     string
	CodexIndex   string
	OpenCodeDB   string
	WatchSeconds int  // 0 = no periodic scanning
	Memory       bool // keep the index in memory instead of a SQLite file
}

// Home returns the user's home directory.
func Home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// Resolve fills empty fields with sensible defaults.
func (c *Config) Resolve() {
	home := Home()
	if c.Port == 0 {
		c.Port = 8787
	}
	if c.DBPath == "" {
		c.DBPath = filepath.Join(home, ".local", "share", "agent-session-viewer", "index.db")
	}
	if c.ClaudeDir == "" {
		c.ClaudeDir = filepath.Join(home, ".claude", "projects")
	}
	if c.CodexDir == "" {
		c.CodexDir = filepath.Join(home, ".codex", "sessions")
	}
	if c.CodexIndex == "" {
		c.CodexIndex = filepath.Join(home, ".codex", "session_index.jsonl")
	}
	if c.OpenCodeDB == "" {
		c.OpenCodeDB = filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	}
}
