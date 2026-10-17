// Command wasmcheck runs the real scanner and cache over a data directory and
// prints everything the frontend would show.
//
// The point is to be run twice, natively and under a wasm runtime, and diffed:
//
//	go run ./cmd/wasmcheck
//	GOOS=js GOARCH=wasm go build -o /tmp/wasmcheck.wasm ./cmd/wasmcheck
//	node "$(go env GOROOT)/lib/wasm/wasm_exec_node.js" /tmp/wasmcheck.wasm
//
// By default it builds its own fixture tree, so it needs no external data. Pass
// -home to point it at a real ~ instead, which is how the timings in
// docs/wasm.md were taken.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/wasmtest"
)

func main() {
	home := flag.String("home", "", "data directory to scan (default: a built-in fixture tree)")
	repeat := flag.Int("repeat", 1, "scan the tree this many times, printing timings")
	flag.Parse()

	if err := run(*home, *repeat); err != nil {
		fmt.Fprintln(os.Stderr, "wasmcheck:", err)
		os.Exit(1)
	}
}

func run(home string, repeat int) error {
	if home == "" {
		dir, err := os.MkdirTemp("", "wasmcheck-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		if _, err := wasmtest.WriteFixtureTree(dir); err != nil {
			return err
		}
		home = dir
	}

	for i := 0; i < repeat; i++ {
		start := time.Now()
		report, err := wasmtest.Run(home)
		if err != nil {
			return err
		}
		if i == 0 {
			fmt.Print(report)
		}
		fmt.Fprintf(os.Stderr, "scan of %s took %s\n", home, time.Since(start).Round(time.Millisecond))
	}
	return nil
}
