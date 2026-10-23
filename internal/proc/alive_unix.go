//go:build unix

package proc

import (
	"errors"
	"syscall"
)

// Alive reports whether a process with this pid exists. Signal 0 performs the
// existence and permission checks without delivering anything; EPERM means the
// process exists but belongs to another user.
func Alive(pid int) bool {
	if !validPID(pid) {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
