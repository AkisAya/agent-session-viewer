package wasmtest

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/golden.txt from the current pipeline output")

// TestGolden is the native half of the contract. Its wasm half is the same test
// binary run under a wasm runtime:
//
//	GOOS=js GOARCH=wasm go test -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./internal/wasmtest/
//
// Both must produce testdata/golden.txt byte for byte.
func TestGolden(t *testing.T) {
	home := t.TempDir()
	if _, err := WriteFixtureTree(home); err != nil {
		t.Fatalf("write fixture tree: %v", err)
	}

	got, err := Run(home)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden.txt")
	if *update {
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v\n\n--- actual output ---\n%s", err, got)
	}
	if got != string(want) {
		t.Errorf("pipeline output does not match %s\n%s", goldenPath, diff(string(want), got))
	}
}

// diff reports the first differing line, plus a little context.
func diff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	n := len(w)
	if len(g) > n {
		n = len(g)
	}
	shown := 0
	for i := 0; i < n && shown < 10; i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			b.WriteString("line ")
			b.WriteString(strconv.Itoa(i + 1))
			b.WriteString(":\n  want: ")
			b.WriteString(wl)
			b.WriteString("\n  got:  ")
			b.WriteString(gl)
			b.WriteString("\n")
			shown++
		}
	}
	if shown == 0 {
		return "trailing whitespace or line-ending difference only"
	}
	return b.String()
}
