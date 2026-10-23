package claude

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// fakeProcs makes the given pids alive with the given start times (zero means
// StartTime is unsupported), restoring the real checks afterwards.
func fakeProcs(t *testing.T, alive map[int]time.Time) {
	t.Helper()
	oldAlive, oldStart := procAlive, procStart
	t.Cleanup(func() { procAlive, procStart = oldAlive, oldStart })
	procAlive = func(pid int) bool {
		_, ok := alive[pid]
		return ok
	}
	procStart = func(pid int) (time.Time, error) {
		if st := alive[pid]; !st.IsZero() {
			return st, nil
		}
		return time.Time{}, errors.ErrUnsupported
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLive(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 10, 3, 7, 20, 46, 0, time.UTC)
	fakeProcs(t, map[int]time.Time{
		101: started,
		102: {}, // alive, start time unknown
		104: started,
		105: started,
		106: started.Add(time.Hour), // pid reused by a later process
		107: started,
		108: started,
	})

	// ps pads a one-digit day with a space; both spellings must match.
	writeFile(t, dir, "101.json", `{"pid":101,"sessionId":"s-busy","cwd":"/p/a","procStart":"Sat Oct  3 07:20:46 2026","status":"busy","waitingFor":"stale","statusUpdatedAt":1792740117076}`)
	writeFile(t, dir, "102.json", `{"pid":102,"sessionId":"s-wait","cwd":"/p/b","status":"waiting","waitingFor":"dialog open","updatedAt":1792740000000}`)
	writeFile(t, dir, "103.json", `{"pid":103,"sessionId":"s-dead","status":"idle"}`)                                                 // process gone
	writeFile(t, dir, "104.json", `{"pid":999,"sessionId":"s-mismatch","status":"idle"}`)                                             // pid disagrees with the name
	writeFile(t, dir, "105.json", `{"pid":105,"status":"idle"}`)                                                                      // no session id
	writeFile(t, dir, "106.json", `{"pid":106,"sessionId":"s-reused","procStart":"Sat Oct 3 07:20:46 2026","status":"busy"}`)         // reused pid
	writeFile(t, dir, "107.json", `{"pid":107,"sessionId":`)                                                                          // caught mid-write
	writeFile(t, dir, "108.json", `{"pid":108,"sessionId":"s-idle","procStart":"not a date","status":"idle","statusUpdatedAt":1000}`) // unparsable procStart trusts the pid
	writeFile(t, dir, "101.d2226bb5276149a16bef196b40309839b39753af56b05b4afad84b5688b835e8.key", "secret")
	writeFile(t, dir, "notes.json", `{"pid":101,"sessionId":"s-notes"}`)
	if err := os.Mkdir(filepath.Join(dir, "109.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	rows, err := (&Source{SessionsDir: dir}).Live()
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if got, want := len(rows), 3; got != want {
		t.Fatalf("rows = %v, want s-busy, s-idle, s-wait", ids)
	}

	busy, idle, wait := rows[0], rows[1], rows[2]
	if busy.ID != "s-busy" || busy.Agent != "claude" || busy.PID != 101 || busy.CWD != "/p/a" || busy.Status != "busy" {
		t.Errorf("busy row = %+v", busy)
	}
	if busy.WaitingFor != "" {
		t.Errorf("busy row keeps waitingFor %q; only a waiting session waits for something", busy.WaitingFor)
	}
	if !busy.Since.Equal(time.UnixMilli(1792740117076)) {
		t.Errorf("busy since = %v, want statusUpdatedAt", busy.Since)
	}
	if wait.ID != "s-wait" || wait.Status != "waiting" || wait.WaitingFor != "dialog open" {
		t.Errorf("waiting row = %+v", wait)
	}
	if !wait.Since.Equal(time.UnixMilli(1792740000000)) {
		t.Errorf("waiting since = %v, want the updatedAt fallback", wait.Since)
	}
	if idle.ID != "s-idle" || idle.Status != "idle" {
		t.Errorf("idle row = %+v", idle)
	}
}

func TestLiveWithoutSessionsDir(t *testing.T) {
	for _, dir := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		rows, err := (&Source{SessionsDir: dir}).Live()
		if err != nil || rows != nil {
			t.Fatalf("Live(%q) = %v, %v; want nothing and no error", dir, rows, err)
		}
	}
}

func TestPidFileName(t *testing.T) {
	for name, want := range map[string]int{"94808.json": 94808, "1.json": 1} {
		if got, ok := pidFileName(name); !ok || got != want {
			t.Errorf("pidFileName(%q) = %d, %v", name, got, ok)
		}
	}
	for _, name := range []string{".json", "-1.json", "12a.json", "12.json.tmp", "12.key", "12.d22.key", "99999999999999999999.json"} {
		if _, ok := pidFileName(name); ok {
			t.Errorf("pidFileName(%q) accepted", name)
		}
	}
}
