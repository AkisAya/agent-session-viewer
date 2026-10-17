package api

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/akisaya/agent-session-viewer/web"
)

// The frontend is served straight out of web.FS with no build step, so a new
// module that is missing from the //go:embed pattern (or a typo in an import)
// would only surface as a broken page in the browser. These tests catch that
// at `go test` time instead.

var localRef = regexp.MustCompile(`(?:src|href)="(/[^"]+)"`)
var esImport = regexp.MustCompile(`from\s+"(\./[^"]+)"`)

func TestIndexHTMLReferencesAreEmbedded(t *testing.T) {
	html, err := fs.ReadFile(web.FS, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	for _, m := range localRef.FindAllStringSubmatch(string(html), -1) {
		name := strings.TrimPrefix(m[1], "/")
		if name == "" {
			continue
		}
		if _, err := fs.Stat(web.FS, name); err != nil {
			t.Errorf("index.html references %q but it is not embedded: %v", m[1], err)
		}
	}
}

func TestModuleImportsResolve(t *testing.T) {
	err := fs.WalkDir(web.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		src, err := fs.ReadFile(web.FS, p)
		if err != nil {
			return err
		}
		for _, m := range esImport.FindAllStringSubmatch(string(src), -1) {
			target := path.Join(path.Dir(p), m[1])
			if _, err := fs.Stat(web.FS, target); err != nil {
				t.Errorf("%s imports %q but %s is not embedded: %v", p, m[1], target, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded fs: %v", err)
	}
}
