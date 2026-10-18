package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveDefaults(t *testing.T) {
	t.Setenv("DSH_HOME", "")
	var c Config
	c.Resolve()

	if c.Host != "127.0.0.1" {
		t.Fatalf("default host = %q, want loopback", c.Host)
	}
	if c.Port != 8787 {
		t.Fatalf("default port = %d, want 8787", c.Port)
	}
	home := Home()
	// Our own index follows the platform convention for local data.
	base := dataBase(runtime.GOOS, home, os.Getenv)
	for _, tc := range []struct {
		name, got, want string
	}{
		{"db", c.DBPath, filepath.Join(base, "agent-session-viewer", "index.db")},
		{"claude", c.ClaudeDir, filepath.Join(home, ".claude", "projects")},
		{"codex", c.CodexDir, filepath.Join(home, ".codex", "sessions")},
		{"codex index", c.CodexIndex, filepath.Join(home, ".codex", "session_index.jsonl")},
		{"opencode", c.OpenCodeDB, filepath.Join(home, ".local", "share", "opencode", "opencode.db")},
		{"dsh", c.DSHDir, filepath.Join(home, ".dsh", "sessions")},
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

// TestResolveDSHHome pins the DSH_HOME override: a non-default DSH installation
// keeps its profile root (and therefore its sessions) elsewhere.
func TestResolveDSHHome(t *testing.T) {
	t.Setenv("DSH_HOME", filepath.Join("/custom", "dsh"))
	var c Config
	c.Resolve()
	if want := filepath.Join("/custom", "dsh", "sessions"); c.DSHDir != want {
		t.Fatalf("dsh dir = %q, want %q", c.DSHDir, want)
	}
}

func TestDataBase(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	for _, tc := range []struct {
		name, goos, home string
		env              map[string]string
		want             string
	}{
		{
			name: "windows uses %LOCALAPPDATA%",
			goos: "windows",
			home: `C:\Users\dev`,
			env:  map[string]string{"LOCALAPPDATA": `C:\Users\dev\AppData\Local`},
			want: `C:\Users\dev\AppData\Local`,
		},
		{
			name: "windows falls back to the home directory",
			goos: "windows",
			home: `C:\Users\dev`,
			want: filepath.Join(`C:\Users\dev`, "AppData", "Local"),
		},
		{
			name: "linux",
			goos: "linux",
			home: "/home/dev",
			env:  map[string]string{"LOCALAPPDATA": "/ignored"},
			want: filepath.Join("/home/dev", ".local", "share"),
		},
		{
			name: "darwin",
			goos: "darwin",
			home: "/Users/dev",
			want: filepath.Join("/Users/dev", ".local", "share"),
		},
	} {
		if got := dataBase(tc.goos, tc.home, env(tc.env)); got != tc.want {
			t.Errorf("%s: dataBase = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestResolveWindowsKeepsAgentPathsHomeRelative pins the deliberate asymmetry:
// only our own index moves to %LOCALAPPDATA%, while the agent histories stay
// under ~/.local/share (OpenCode) and ~/.codex / ~/.claude, which is where the
// CLIs put them on Windows too.
func TestResolveWindowsKeepsAgentPathsHomeRelative(t *testing.T) {
	var c Config
	c.resolve("windows", func(k string) string {
		if k == "LOCALAPPDATA" {
			return `C:\Users\dev\AppData\Local`
		}
		return ""
	})
	home := Home()
	want := map[string]string{
		"db":       filepath.Join(`C:\Users\dev\AppData\Local`, "agent-session-viewer", "index.db"),
		"claude":   filepath.Join(home, ".claude", "projects"),
		"codex":    filepath.Join(home, ".codex", "sessions"),
		"opencode": filepath.Join(home, ".local", "share", "opencode", "opencode.db"),
		"dsh":      filepath.Join(home, ".dsh", "sessions"),
	}
	got := map[string]string{"db": c.DBPath, "claude": c.ClaudeDir, "codex": c.CodexDir, "opencode": c.OpenCodeDB, "dsh": c.DSHDir}
	for name, g := range got {
		if g != want[name] {
			t.Errorf("%s = %q, want %q", name, g, want[name])
		}
	}
}
