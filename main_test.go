package main

import (
	"runtime/debug"
	"testing"
)

func TestBinaryVersion(t *testing.T) {
	stamped := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/akisaya/agent-session-viewer", Version: v}}
	}
	for _, tc := range []struct {
		name    string
		ldflags string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"release build keeps the -ldflags tag", "v0.7.0", stamped("v0.6.3+dirty"), true, "v0.7.0"},
		{"source build on a tag", "dev", stamped("v0.7.0"), true, "v0.7.0"},
		{"source build between tags", "dev", stamped("v0.7.1-0.20261023092015-1d9252ae4adf+dirty"), true, "v0.7.1-0.20261023092015-1d9252ae4adf+dirty"},
		{"go run / -buildvcs=false", "dev", stamped("(devel)"), true, "dev"},
		{"no build info", "dev", nil, false, "dev"},
	} {
		if got := binaryVersion(tc.ldflags, tc.info, tc.ok); got != tc.want {
			t.Errorf("%s: binaryVersion = %q, want %q", tc.name, got, tc.want)
		}
	}
}
