// Package proc answers the two questions a live-status file raises: is the
// process that wrote it still running, and is it still the same process.
//
// Agents leave per-process status files behind when they crash, and the OS
// reuses pids, so a status file is only trusted when its pid is alive and,
// where the platform can tell, that pid started when the file says it did.
// Both checks are single syscalls (no `ps` exec), cheap enough to run on every
// poll.
package proc

import "math"

// validPID rejects pids that would address a process group, every process, or
// overflow the 32-bit pid the kernel actually takes.
func validPID(pid int) bool {
	return pid > 1 && pid <= math.MaxInt32
}
