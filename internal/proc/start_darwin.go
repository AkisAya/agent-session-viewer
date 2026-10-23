package proc

import (
	"time"

	"golang.org/x/sys/unix"
)

// StartTime returns when the process started, read from the kernel with one
// sysctl. `ps -o lstart` prints the same kernel field, so the two agree to the
// second.
func StartTime(pid int) (time.Time, error) {
	if !validPID(pid) {
		return time.Time{}, unix.ESRCH
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, err
	}
	if kp.Proc.P_pid != int32(pid) {
		return time.Time{}, unix.ESRCH
	}
	tv := kp.Proc.P_starttime
	return time.Unix(tv.Sec, int64(tv.Usec)*1000), nil
}
