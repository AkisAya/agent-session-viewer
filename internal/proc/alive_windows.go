package proc

import (
	"errors"
	"syscall"
)

// PROCESS_QUERY_LIMITED_INFORMATION is the narrowest access right that still
// allows GetExitCodeProcess; STILL_ACTIVE is its exit code while running.
const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

// Alive reports whether a process with this pid exists and has not exited. A
// handle to an exited process stays valid while someone holds it open, so the
// exit code decides, not OpenProcess alone.
func Alive(pid int) bool {
	if !validPID(pid) {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// The process exists but is protected from us.
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
