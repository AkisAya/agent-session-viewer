//go:build !unix && !windows

package proc

// Alive cannot check process existence on this platform, so it reports false:
// a stale status file must not look like a running session.
func Alive(pid int) bool { return false }
