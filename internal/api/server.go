// Package api exposes the local HTTP API and embedded frontend.
package api

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/akisaya/agent-session-viewer/internal/index"
	"github.com/akisaya/agent-session-viewer/internal/model"
)

// Server wires the index to HTTP handlers.
type Server struct {
	ix      *index.Index
	web     fs.FS
	version string
}

// New builds the HTTP handler.
func New(ix *index.Index, web fs.FS, version string) http.Handler {
	s := &Server{ix: ix, web: web, version: version}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/scan", s.handleScan)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("GET /api/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/activity", s.handleActivity)
	mux.HandleFunc("GET /api/hours", s.handleHours)
	mux.HandleFunc("GET /api/trend", s.handleTrend)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/tools", s.handleTools)
	mux.HandleFunc("GET /api/relations", s.handleRelations)
	mux.HandleFunc("POST /api/rescan", s.handleRescan)
	mux.HandleFunc("GET /api/sessions/{agent}/{id}/meta", s.handleSessionMeta)
	mux.HandleFunc("GET /api/sessions/{agent}/{id}", s.handleTranscript)
	mux.Handle("/", s.static())
	return withGzip(mux)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"version": s.version})
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.ix.Status())
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st, err := s.ix.Stats(q.Get("agent"), q.Get("cwd"), q.Get("day"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if st.Agents == nil {
		st.Agents = []model.AgentStat{}
	}
	writeJSON(w, map[string]any{
		"agents":   st.Agents,
		"total":    st.Total,
		"lastScan": s.ix.LastScan(),
	})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	page, err := s.ix.SessionPage(index.Filter{
		Agent:  q.Get("agent"),
		CWD:    q.Get("cwd"),
		Query:  q.Get("q"),
		Day:    q.Get("day"),
		Sort:   q.Get("sort"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, page)
}

// sessionIDRe is the accepted shape of a session id. Ids come from agent logs
// and include UUIDs, "ses_..." tokens and rollout names; none need path
// separators, so rejecting them also closes a path-traversal route into the
// source fallback lookups.
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

func validSessionID(id string) bool {
	return sessionIDRe.MatchString(id) && !strings.Contains(id, "..")
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	id := r.PathValue("id")
	if !validSessionID(id) {
		writeStatus(w, http.StatusBadRequest, "invalid session id")
		return
	}
	if !s.ix.HasSource(agent) {
		writeStatus(w, http.StatusNotFound, "unknown agent")
		return
	}
	msgs, err := s.ix.Transcript(agent, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if msgs == nil {
		msgs = []model.Message{}
	}
	writeJSON(w, msgs)
}

// handleSessionMeta returns one cached session summary, which lets a deep link
// open a session that is outside the current page or filter.
func (s *Server) handleSessionMeta(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	id := r.PathValue("id")
	if !validSessionID(id) {
		writeStatus(w, http.StatusBadRequest, "invalid session id")
		return
	}
	if !s.ix.HasSource(agent) {
		writeStatus(w, http.StatusNotFound, "unknown agent")
		return
	}
	sess, err := s.ix.Session(agent, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, sess)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.ix.Projects(r.URL.Query().Get("agent"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if projects == nil {
		projects = []model.Project{}
	}
	writeJSON(w, projects)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, err := s.ix.Activity(q.Get("agent"), q.Get("cwd"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if days == nil {
		days = []model.ActivityDay{}
	}
	writeJSON(w, days)
}

// handleHours returns today's by-hour histogram (msgs/sessions/tokens/cost)
// for the scope.
func (s *Server) handleHours(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hours, err := s.ix.Hours(q.Get("agent"), q.Get("cwd"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if hours == nil {
		hours = []model.HourPoint{}
	}
	writeJSON(w, hours)
}

func (s *Server) handleTrend(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, _ := strconv.Atoi(q.Get("days"))
	pts, err := s.ix.Trend(q.Get("agent"), q.Get("cwd"), days)
	if err != nil {
		writeErr(w, err)
		return
	}
	if pts == nil {
		pts = []model.TrendPoint{}
	}
	writeJSON(w, pts)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	models, err := s.ix.Models(q.Get("agent"), q.Get("cwd"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if models == nil {
		models = []model.ModelStat{}
	}
	writeJSON(w, models)
}

func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	st, err := s.ix.Tools(q.Get("agent"), q.Get("cwd"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	if st.Tools == nil {
		st.Tools = []model.ToolStat{}
	}
	writeJSON(w, st)
}

func (s *Server) handleRelations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sessions, err := s.ix.Relations(q.Get("agent"), q.Get("cwd"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if sessions == nil {
		sessions = []model.Session{}
	}
	writeJSON(w, sessions)
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	// Requiring JSON makes a cross-site HTML form (which can only send
	// text/plain or form encodings without a preflight) unable to trigger a scan.
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		writeStatus(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}
	started := s.ix.StartScan()
	writeJSON(w, map[string]any{"ok": true, "started": started})
}

func (s *Server) static() http.Handler {
	fileServer := http.FileServerFS(s.web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Revalidate on every load. A rebuilt binary ships new JS/CSS under the
		// same URLs, and a blind cache would pair the new index.html with stale
		// modules (e.g. a renderer looking for an element that no longer exists).
		w.Header().Set("Cache-Control", "no-cache")
		if r.URL.Path == "/" {
			http.ServeFileFS(w, r, s.web, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// gzipResponse compresses the handler output when the client accepts it. Range
// requests are skipped in withGzip so static file ranges stay correct.
type gzipResponse struct {
	http.ResponseWriter
	gz    *gzip.Writer
	wrote bool
}

func (g *gzipResponse) WriteHeader(code int) {
	if g.wrote {
		return
	}
	g.wrote = true
	g.Header().Del("Content-Length")
	if code < 200 || code == http.StatusNoContent || code == http.StatusNotModified {
		// No body: drop the compressor so Close does not emit a gzip stream.
		g.gz = nil
		g.ResponseWriter.WriteHeader(code)
		return
	}
	g.Header().Set("Content-Encoding", "gzip")
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponse) Write(b []byte) (int, error) {
	if !g.wrote {
		g.WriteHeader(http.StatusOK)
	}
	return g.gz.Write(b)
}

func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" ||
			!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gz := gzip.NewWriter(w)
		gr := &gzipResponse{ResponseWriter: w, gz: gz}
		defer func() {
			if gr.wrote && gr.gz != nil {
				_ = gr.gz.Close()
			}
		}()
		next.ServeHTTP(gr, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeStatus(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, fs.ErrNotExist) {
		status = http.StatusNotFound
	}
	writeStatus(w, status, err.Error())
}
