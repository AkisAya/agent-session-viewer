// Command agent-session serves a local web UI over Claude Code, Codex and
// OpenCode conversation history.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/akis/agent-session/internal/api"
	"github.com/akis/agent-session/internal/config"
	"github.com/akis/agent-session/internal/index"
	"github.com/akis/agent-session/internal/source"
	"github.com/akis/agent-session/internal/source/claude"
	"github.com/akis/agent-session/internal/source/codex"
	"github.com/akis/agent-session/internal/source/opencode"
	"github.com/akis/agent-session/web"
)

func main() {
	var cfg config.Config
	flag.IntVar(&cfg.Port, "port", 0, "HTTP port (default 8787)")
	flag.StringVar(&cfg.DBPath, "db", "", "cache database path")
	flag.StringVar(&cfg.ClaudeDir, "claude-dir", "", "Claude Code projects directory")
	flag.StringVar(&cfg.CodexDir, "codex-dir", "", "Codex sessions directory")
	flag.StringVar(&cfg.CodexIndex, "codex-index", "", "Codex session index file")
	flag.StringVar(&cfg.OpenCodeDB, "opencode-db", "", "OpenCode SQLite database")
	flag.IntVar(&cfg.WatchSeconds, "watch-interval", 0, "rescan every N seconds (0 = only on demand)")
	flag.Parse()
	cfg.Resolve()

	sources := []source.Source{
		&claude.Source{Root: cfg.ClaudeDir},
		&codex.Source{Root: cfg.CodexDir, Index: cfg.CodexIndex},
		&opencode.Source{Path: cfg.OpenCodeDB},
	}

	ix, err := index.Open(cfg.DBPath, sources...)
	if err != nil {
		log.Fatalf("open index: %v", err)
	}
	defer ix.Close()

	log.Printf("cache: %s", cfg.DBPath)
	start := time.Now()
	if err := ix.Scan(); err != nil {
		log.Printf("initial scan error: %v", err)
	}
	if st, err := ix.Stats(); err == nil {
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

	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: api.New(ix, web.FS)}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on http://localhost:%d", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
