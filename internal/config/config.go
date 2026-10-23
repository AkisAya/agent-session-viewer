// Package config resolves runtime paths and options.
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// Config holds runtime options. Zero-value fields are filled with defaults
// by Resolve.
type Config struct {
	Host         string
	Port         int
	DBPath       string
	ClaudeDir    string
	ClaudeLive   string // Claude Code per-process status files; derived from ClaudeDir
	CodexDir     string
	CodexIndex   string
	OpenCodeDB   string
	DSHDir       string
	WatchSeconds int  // 0 = no periodic scanning
	Memory       bool // keep the index in memory instead of a SQLite file
	Rebuild      bool // delete the cache database before opening it
	Open         bool // open the UI in the default browser after listening
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
	c.resolve(runtime.GOOS, os.Getenv)
}

// resolve is Resolve with the platform and environment injected, so the
// per-OS defaults can be exercised on any host.
func (c *Config) resolve(goos string, getenv func(string) string) {
	home := Home()
	if c.Host == "" {
		// Loopback by default: the tool exposes the full local conversation
		// history, so it must not be reachable from the network unless the
		// user opts in with -host.
		c.Host = "127.0.0.1"
	}
	if c.Port == 0 {
		c.Port = 8787
	}
	if c.DBPath == "" {
		c.DBPath = filepath.Join(dataBase(goos, home, getenv), "agent-session-viewer", "index.db")
	}
	if c.ClaudeDir == "" {
		c.ClaudeDir = filepath.Join(home, ".claude", "projects")
	}
	if c.ClaudeLive == "" {
		// Claude Code keeps sessions/ next to projects/ in its config dir, so a
		// -claude-dir pointing at another config dir moves both together.
		c.ClaudeLive = filepath.Join(filepath.Dir(filepath.Clean(c.ClaudeDir)), "sessions")
	}
	if c.CodexDir == "" {
		c.CodexDir = filepath.Join(home, ".codex", "sessions")
	}
	if c.CodexIndex == "" {
		c.CodexIndex = filepath.Join(home, ".codex", "session_index.jsonl")
	}
	if c.OpenCodeDB == "" {
		// OpenCode stores its database under the Unix-style XDG data path on
		// every platform — Windows included — so this needs no per-OS branch.
		// (See https://github.com/sst/opencode: ~/.local/share/opencode.)
		c.OpenCodeDB = filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	}
	if c.DSHDir == "" {
		// DSH keeps every profile under $DSH_HOME (default ~/.dsh). Its
		// session logs live in the shared sessions/ subtree.
		dshHome := getenv("DSH_HOME")
		if dshHome == "" {
			dshHome = filepath.Join(home, ".dsh")
		}
		c.DSHDir = filepath.Join(dshHome, "sessions")
	}
}

// dataBase returns the base directory for this tool's own local data (the
// index database). The agent CLIs keep their history under ~/.local/share
// even on Windows, but our derived index follows the platform convention:
// %LOCALAPPDATA% on Windows, ~/.local/share everywhere else.
func dataBase(goos, home string, getenv func(string) string) string {
	if goos == "windows" {
		if dir := getenv("LOCALAPPDATA"); dir != "" {
			return dir
		}
		return filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(home, ".local", "share")
}
