// Command asv serves a local web UI over Claude Code, Codex, OpenCode and
// DeepSeek Harness conversation history.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/akisaya/agent-session-viewer/internal/api"
	"github.com/akisaya/agent-session-viewer/internal/config"
	"github.com/akisaya/agent-session-viewer/internal/index"
	"github.com/akisaya/agent-session-viewer/internal/source"
	"github.com/akisaya/agent-session-viewer/internal/source/claude"
	"github.com/akisaya/agent-session-viewer/internal/source/codex"
	"github.com/akisaya/agent-session-viewer/internal/source/dsh"
	"github.com/akisaya/agent-session-viewer/internal/source/opencode"
	"github.com/akisaya/agent-session-viewer/web"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		cfg         config.Config
		showVersion bool
	)
	flag.StringVar(&cfg.Host, "host", "", "HTTP bind address (default 127.0.0.1; use 0.0.0.0 to expose to the network)")
	flag.IntVar(&cfg.Port, "port", 0, "HTTP port (default 8787)")
	flag.StringVar(&cfg.DBPath, "db", "", "cache database path")
	flag.StringVar(&cfg.ClaudeDir, "claude-dir", "", "Claude Code projects directory")
	flag.StringVar(&cfg.CodexDir, "codex-dir", "", "Codex sessions directory")
	flag.StringVar(&cfg.CodexIndex, "codex-index", "", "Codex session index file")
	flag.StringVar(&cfg.OpenCodeDB, "opencode-db", "", "OpenCode SQLite database")
	flag.StringVar(&cfg.DSHDir, "dsh-dir", "", "DeepSeek Harness sessions directory")
	flag.IntVar(&cfg.WatchSeconds, "watch-interval", 0, "rescan every N seconds (0 = only on demand)")
	flag.BoolVar(&cfg.Memory, "memory", false, "keep the index in memory instead of a SQLite file (full rescan on every start)")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()
	if showVersion {
		fmt.Println("asv " + version)
		return
	}
	cfg.Resolve()

	sources := []source.Source{
		&claude.Source{Root: cfg.ClaudeDir},
		&codex.Source{Root: cfg.CodexDir, Index: cfg.CodexIndex},
		&opencode.Source{Path: cfg.OpenCodeDB},
		&dsh.Source{Root: cfg.DSHDir},
	}

	ix, err := index.Open(index.Options{Path: cfg.DBPath, Memory: cfg.Memory}, sources...)
	if err != nil {
		log.Fatalf("open index: %v", err)
	}
	defer ix.Close()

	if cfg.Memory {
		log.Printf("cache: memory (nothing written to disk, full rescan on every start)")
	} else {
		log.Printf("cache: %s", cfg.DBPath)
	}
	start := time.Now()
	if err := ix.Scan(); err != nil {
		log.Printf("initial scan error: %v", err)
	}
	if st, err := ix.Stats("", "", ""); err == nil {
		log.Printf("indexed %d sessions in %s", st.Total.Sessions, time.Since(start).Round(time.Millisecond))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.WatchSeconds > 0 {
		go func() {
			t := time.NewTicker(time.Duration(cfg.WatchSeconds) * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					_ = ix.Scan()
				}
			}
		}()
	}

	srv := &http.Server{Addr: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), Handler: api.New(ix, web.FS)}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on http://%s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
