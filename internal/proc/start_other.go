//go:build !darwin

package proc

import (
	"errors"
	"time"
)

// StartTime is only implemented on macOS; callers fall back to Alive alone.
//
// shortcut: Linux could derive it from /proc/<pid>/stat plus btime, but btime
// shifts when the wall clock is stepped, and a false mismatch would hide a
// running session. Add it if pid reuse shows up in practice.
func StartTime(pid int) (time.Time, error) {
	return time.Time{}, errors.ErrUnsupported
}
