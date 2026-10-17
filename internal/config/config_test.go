package config

import (
	"path/filepath"
	"testing"
)

func TestResolveDefaults(t *testing.T) {
	var c Config
	c.Resolve()

	if c.Host != "127.0.0.1" {
		t.Fatalf("default host = %q, want loopback", c.Host)
	}
	if c.Port != 8787 {
		t.Fatalf("default port = %d, want 8787", c.Port)
	}
	home := Home()
	for _, tc := range []struct {
		name, got, want string
	}{
		{"db", c.DBPath, filepath.Join(home, ".local", "share", "agent-session-viewer", "index.db")},
		{"claude", c.ClaudeDir, filepath.Join(home, ".claude", "projects")},
		{"codex", c.CodexDir, filepath.Join(home, ".codex", "sessions")},
		{"opencode", c.OpenCodeDB, filepath.Join(home, ".local", "share", "opencode", "opencode.db")},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s dir = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestResolveKeepsExplicitValues(t *testing.T) {
	c := Config{Host: "0.0.0.0", Port: 9000}
	c.Resolve()
	if c.Host != "0.0.0.0" || c.Port != 9000 {
		t.Fatalf("explicit values overwritten: host=%q port=%d", c.Host, c.Port)
	}
}
