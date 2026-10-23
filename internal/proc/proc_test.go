package proc

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// skipUnsupported skips where Alive has no implementation (wasm, plan9).
func skipUnsupported(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "js", "wasip1", "plan9":
		t.Skip("Alive is not implemented on " + runtime.GOOS)
	}
}

func TestAlive(t *testing.T) {
	skipUnsupported(t)
	if !Alive(os.Getpid()) {
		t.Fatal("the test process itself is not alive")
	}
	// 0 / -1 / 1 would address a process group, every process or init.
	bad := []int{0, -1, 1}
	if strconv.IntSize == 64 {
		over := int64(math.MaxInt32) + 1 // a variable, so 32-bit builds still compile
		bad = append(bad, int(over))
	}
	for _, pid := range bad {
		if Alive(pid) {
			t.Errorf("Alive(%d) = true, want false", pid)
		}
	}
}

// A child that exited and was reaped no longer exists.
func TestAliveAfterExit(t *testing.T) {
	skipUnsupported(t)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait child: %v", err)
	}
	if Alive(pid) {
		t.Fatalf("Alive(%d) = true after the child exited", pid)
	}
}

func TestStartTime(t *testing.T) {
	start, err := StartTime(os.Getpid())
	if runtime.GOOS != "darwin" {
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("StartTime on %s: err = %v, want ErrUnsupported", runtime.GOOS, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("StartTime: %v", err)
	}
	// The test binary was started moments ago.
	if age := time.Since(start); age < 0 || age > 10*time.Minute {
		t.Fatalf("start time %v is %v ago, want within the test run", start, age)
	}
	if _, err := StartTime(0); err == nil {
		t.Fatal("StartTime(0) succeeded")
	}
}
