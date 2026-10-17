package source

import "testing"

func TestToolName(t *testing.T) {
	cases := map[string]string{
		"":             "unknown",
		"  bash  ":     "shell",
		"Bash":         "shell",
		"exec_command": "shell",
		"Shell":        "shell",
		"Read":         "read",
		"MultiEdit":    "edit",
		"apply_patch":  "apply_patch",
		"glob":         "glob",
		"LS":           "glob",
		"WebFetch":     "webfetch",
		"Update_plan":  "plan",
		"Task":         "subagent",
		// MCP tools keep their server and tool name.
		"mcp__github__create_issue": "mcp:github.create_issue",
		"mcp__server":               "mcp:server",
		// Unknown names are lowercased so agents line up.
		"SomeCustomTool": "somecustomtool",
	}
	for raw, want := range cases {
		if got := ToolName(raw); got != want {
			t.Errorf("ToolName(%q) = %q, want %q", raw, got, want)
		}
	}
}
