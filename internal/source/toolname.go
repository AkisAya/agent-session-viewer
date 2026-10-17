package source

import "strings"

// toolAliases maps agent-specific tool spellings onto one canonical name so
// tool statistics can be aggregated across agents. Keys are lowercase.
var toolAliases = map[string]string{
	// shells
	"bash": "shell", "shell": "shell", "exec": "shell", "exec_command": "shell",
	"custom_tool_call": "shell", "write_stdin": "shell", "run_command": "shell",
	"commandexecution": "shell",
	// file tools
	"read": "read", "write": "write", "edit": "edit",
	"multi_edit": "edit", "multiedit": "edit",
	"str_replace_editor": "edit", "apply_patch": "apply_patch", "patch": "apply_patch",
	// search tools
	"grep": "grep", "glob": "glob", "ls": "glob",
	// web tools
	"webfetch": "webfetch", "web_fetch": "webfetch",
	"websearch": "websearch", "web_search": "websearch", "web_search_call": "websearch",
	// planning / tasks
	"update_plan": "plan", "plan": "plan",
	"todo": "todo", "todowrite": "todo", "todoread": "todo",
	"task": "subagent", "agent": "subagent", "subagent": "subagent",
	"spawn_agent": "subagent",
	// misc
	"question": "question", "skill": "skill", "execute": "execute",
}

// ToolName normalizes a raw tool name into its canonical form. MCP tools keep
// their server and tool names ("mcp:server.tool"); unknown names are
// lowercased so Claude's CamelCase still lines up with OpenCode's lowercase.
func ToolName(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "unknown"
	}
	// Claude Code spells MCP tools mcp__<server>__<tool>.
	if strings.HasPrefix(name, "mcp__") {
		rest := strings.TrimPrefix(name, "mcp__")
		if i := strings.Index(rest, "__"); i > 0 {
			return "mcp:" + rest[:i] + "." + rest[i+2:]
		}
		return "mcp:" + rest
	}
	lower := strings.ToLower(name)
	if canon, ok := toolAliases[lower]; ok {
		return canon
	}
	return lower
}
