// Package api exposes the local HTTP API and embedded frontend.
package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/akisaya/agent-session-viewer/internal/index"
	"github.com/akisaya/agent-session-viewer/internal/model"
)

// Server wires the index to HTTP handlers.
type Server struct {
	ix  *index.Index
	web fs.FS
}

// New builds the HTTP handler.
func New(ix *index.Index, web fs.FS) http.Handler {
	s := &Server{ix: ix, web: web}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("GET /api/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/activity", s.handleActivity)
	mux.HandleFunc("GET /api/trend", s.handleTrend)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/tools", s.handleTools)
	mux.HandleFunc("GET /api/relations", s.handleRelations)
	mux.HandleFunc("POST /api/rescan", s.handleRescan)
	mux.HandleFunc("GET /api/sessions/{agent}/{id}", s.handleTranscript)
	mux.Handle("/", s.static())
	return mux
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st, err := s.ix.Stats(q.Get("agent"), q.Get("cwd"), q.Get("day"))
	if err != nil {
		writeErr(w, err)
		return
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
	sessions, err := s.ix.Sessions(index.Filter{
		Agent: q.Get("agent"),
		CWD:   q.Get("cwd"),
		Query: q.Get("q"),
		Day:   q.Get("day"),
		Sort:  q.Get("sort"),
		Limit: limit,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if sessions == nil {
		sessions = []model.Session{} // ensure JSON [] not null
	}
	writeJSON(w, sessions)
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	id := r.PathValue("id")
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
	if err := s.ix.Scan(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "lastScan": s.ix.LastScan()})
}

func (s *Server) static() http.Handler {
	fileServer := http.FileServerFS(s.web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFileFS(w, r, s.web, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
