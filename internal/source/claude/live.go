package claude

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/model"
	"github.com/akisaya/agent-session-viewer/internal/proc"
)

// liveFile is the per-process status file Claude Code keeps at
// <config>/sessions/<pid>.json while it runs (CLI and desktop app alike).
// The format is internal to Claude Code, so every field is optional and an
// unreadable file is skipped rather than reported. The <pid>.<hash>.key files
// next to it hold secrets and are never opened.
type liveFile struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	CWD             string `json:"cwd"`
	ProcStart       string `json:"procStart"` // `ps -o lstart=` output, TZ=UTC
	Status          string `json:"status"`    // busy | shell | waiting | idle
	WaitingFor      string `json:"waitingFor"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"` // unix ms
	UpdatedAt       int64  `json:"updatedAt"`       // unix ms
}

// maxLiveFile bounds one status file read; real ones are well under 1 KiB.
const maxLiveFile = 64 << 10

// procStartLayout is `ps -o lstart=` in the C locale; Claude Code runs it with
// TZ=UTC. The _2 day also accepts ps's space padding ("Oct  3").
const procStartLayout = "Mon Jan _2 15:04:05 2006"

// procStartSlack absorbs the whole-second resolution of lstart.
const procStartSlack = 2 * time.Second

// Process checks, swappable in tests.
var (
	procAlive = proc.Alive
	procStart = proc.StartTime
)

// Live lists the sessions whose Claude Code process is running. The pid in the
// file name is checked first, so the stale file of a crashed process costs a
// directory entry, not a read.
func (s *Source) Live() ([]model.LiveSession, error) {
	if s.SessionsDir == "" {
		return nil, nil
	}
	ents, err := os.ReadDir(s.SessionsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // Claude Code not installed or never run
		}
		return nil, err
	}
	var out []model.LiveSession
	for _, e := range ents {
		pid, ok := pidFileName(e.Name())
		if !ok || e.IsDir() || !procAlive(pid) {
			continue
		}
		path := filepath.Join(s.SessionsDir, e.Name())
		f, ok := readLiveFile(path)
		if !ok || f.PID != pid || f.SessionID == "" || reused(pid, f.ProcStart) {
			continue
		}
		out = append(out, liveSession(s.Name(), f, path))
	}
	return out, nil
}

// pidFileName parses "<pid>.json"; anything else in the directory is ignored.
func pidFileName(name string) (int, bool) {
	digits, ok := strings.CutSuffix(name, ".json")
	if !ok || digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, false
	}
	pid, err := strconv.Atoi(digits)
	return pid, err == nil
}

func readLiveFile(path string) (liveFile, bool) {
	var f liveFile
	fh, err := os.Open(path)
	if err != nil {
		return f, false
	}
	defer fh.Close()
	// A file caught mid-write fails to decode and is picked up on the next poll.
	err = json.NewDecoder(io.LimitReader(fh, maxLiveFile)).Decode(&f)
	return f, err == nil
}

// reused reports whether the pid now belongs to a different process than the
// one that wrote the file: a crashed session leaves its file behind and the OS
// may hand the pid out again. Anything that cannot be compared (an older file
// without procStart, a platform without StartTime) trusts the pid.
func reused(pid int, recorded string) bool {
	if recorded == "" {
		return false
	}
	want, err := time.Parse(procStartLayout, strings.Join(strings.Fields(recorded), " "))
	if err != nil {
		return false
	}
	got, err := procStart(pid)
	if err != nil {
		return false
	}
	return got.Sub(want).Abs() > procStartSlack
}

func liveSession(agent string, f liveFile, path string) model.LiveSession {
	ls := model.LiveSession{
		Agent:  agent,
		ID:     f.SessionID,
		PID:    f.PID,
		CWD:    f.CWD,
		Status: f.Status,
	}
	// waitingFor can outlive the wait in the file; only a waiting session has
	// something it waits for.
	if f.Status == "waiting" {
		ls.WaitingFor = f.WaitingFor
	}
	switch {
	case f.StatusUpdatedAt > 0:
		ls.Since = time.UnixMilli(f.StatusUpdatedAt)
	case f.UpdatedAt > 0:
		ls.Since = time.UnixMilli(f.UpdatedAt)
	default:
		if info, err := os.Stat(path); err == nil {
			ls.Since = info.ModTime()
		}
	}
	return ls
}
