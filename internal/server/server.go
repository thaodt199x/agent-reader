// Package server provides the HTTP + WebSocket server.
package server

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agent-web/internal/hub"
	"agent-web/internal/watcher"

	"github.com/gorilla/websocket"
)

//go:embed static/*
var staticFS embed.FS

var sessionsTemplate = template.Must(template.New("sessions").Parse(`{{range .}}
<div class="session-item px-4 py-2.5 border-b border-ctp-surface0 cursor-pointer transition-colors duration-150 hover:bg-ctp-surface1" onclick="selectSession('{{.ID}}')">
  <div class="text-xs text-ctp-text mt-0.5">{{.Project}}</div>
  <div class="text-[11px] text-ctp-overlay1 break-all">{{.ID}}</div>
  <div class="text-[10px] text-ctp-overlay0 mt-0.5">{{.CWD}}</div>
</div>
{{end}}`))

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for local dev
	},
}

// Server ties together the HTTP server, WebSocket hub, and file watcher.
type Server struct {
	hub         *hub.Hub
	watcher     *watcher.Watcher
	sessionsDir string
}

// New creates a new Server.
func New(sessionsDir string) (*Server, error) {
	w, err := watcher.New(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}

	return &Server{
		hub:         hub.New(),
		watcher:     w,
		sessionsDir: sessionsDir,
	}, nil
}

// Start launches the HTTP server on the given address.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()

	// WebSocket endpoint
	mux.HandleFunc("/ws", s.handleWS)

	// REST API
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/", s.handleSessionByID)

	// HTMX HTML fragment for session list
	mux.HandleFunc("/sessions", s.handleSessionsHTML)

	// Static files (dashboard)
	staticSub, err := fs.Sub(staticFS, "static")
	if err == nil {
		mux.Handle("/", http.FileServer(http.FS(staticSub)))
	} else {
		// Fallback: serve from web/static directory on disk
		mux.Handle("/", http.FileServer(http.Dir("web/static")))
	}

	log.Printf("[server] listening on %s", addr)
	log.Printf("[server] WebSocket: ws://localhost%s/ws", addr[strings.Index(addr, ":"):])
	log.Printf("[server] Sessions dir: %s", s.sessionsDir)

	// Start hub and watcher
	s.hub.SetSubscribeCallback(s.onSubscribe)
	go s.hub.Run()
	go s.hub.SubscribeWatcher(s.watcher)
	s.watcher.Start()

	return http.ListenAndServe(addr, mux)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() {
	s.watcher.Stop()
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[server] upgrade error: %v", err)
		return
	}

	client := hub.NewClient(s.hub, conn)
	go client.Serve()
}

// handleSessions returns a list of all known sessions as JSON.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/sessions" || r.URL.RawQuery != "" {
		return
	}

	sessions := s.listSessions()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	// /api/sessions/<id>
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	// Find the session file
	sessions := s.listSessions()
	var found *SessionInfo
	for i := range sessions {
		if sessions[i].ID == id {
			found = &sessions[i]
			break
		}
	}

	if found == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(found)
}

// handleSessionsHTML returns an HTML fragment of all sessions for the sidebar (HTMX).
func (s *Server) handleSessionsHTML(w http.ResponseWriter, r *http.Request) {
	sessions := s.listSessions()
	w.Header().Set("Content-Type", "text/html")
	sessionsTemplate.Execute(w, sessions)
}

// onSubscribe is called when a WebSocket client subscribes to a session.
// It replays existing events from the session's JSONL file.
func (s *Server) onSubscribe(sessionID string, client *hub.Client) {
	// Find the session file
	sessions := s.listSessions()
	var sessionFile string
	for i := range sessions {
		if sessions[i].ID == sessionID {
			sessionFile = sessions[i].File
			break
		}
	}

	if sessionFile == "" {
		log.Printf("[server] session file not found for %s", sessionID)
		return
	}

	log.Printf("[server] replaying session %s from %s", sessionID, sessionFile)

	f, err := os.Open(sessionFile)
	if err != nil {
		log.Printf("[server] open session file: %v", err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer for large lines

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		msg := hub.WSMessage{
			Type:      "event",
			SessionID: sessionID,
			Data:      json.RawMessage(line),
			Time:      time.Now(),
		}
		data, err := json.Marshal(msg)
		if err != nil {
			continue
		}

		// Send to client's send channel (non-blocking)
		select {
		case client.Send() <- data:
		default:
			// Client buffer full, skip
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		log.Printf("[server] scan error: %v", err)
	}

	log.Printf("[server] finished replaying session %s", sessionID)
}

// SessionInfo is returned by the /api/sessions endpoint.
type SessionInfo struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	CWD       string    `json:"cwd"`
	Timestamp time.Time `json:"timestamp"`
	File      string    `json:"file"`
	LineCount int       `json:"line_count"`
}

// listSessions scans the sessions directory and returns metadata for each file,
// sorted by most recent modification time (newest first).
func (s *Server) listSessions() []SessionInfo {
	var sessions []SessionInfo

	filepath.WalkDir(s.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		info := SessionInfo{
			File: path,
		}

		// Extract project from directory name
		dir := filepath.Dir(path)
		info.Project = filepath.Base(dir)

		// Extract session ID from filename
		base := filepath.Base(path)
		for i := len(base) - 1; i >= 0; i-- {
			if base[i] == '_' {
				info.ID = base[i+1 : len(base)-len(".jsonl")]
				break
			}
		}

		// Count lines and extract CWD from first line
		info.LineCount, info.CWD = countLinesAndCWD(path)

		// Get file modification time
		if fi, err := d.Info(); err == nil {
			info.Timestamp = fi.ModTime()
		}

		sessions = append(sessions, info)
		return nil
	})

	// Sort by modification time, newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Timestamp.After(sessions[j].Timestamp)
	})

	return sessions
}

// countLinesAndCWD reads the first line to get CWD and counts total lines.
func countLinesAndCWD(path string) (int, string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer f.Close()

	count := 0
	cwd := ""
	buf := make([]byte, 32*1024)
	scanner := NewLineScanner(f, buf)

	for scanner.Scan() {
		count++
		line := scanner.Bytes()
		if count == 1 {
			// Parse first line for CWD
			var first struct {
				Type string `json:"type"`
				CWD  string `json:"cwd"`
			}
			json.Unmarshal(line, &first)
			if first.Type == "session" {
				cwd = first.CWD
			}
		}
	}

	return count, cwd
}

// LineScanner is a simple line-by-line scanner.
type LineScanner struct {
	buf  []byte
	line []byte
	err  error
	pos  int
	n    int
	r    *os.File
}

func NewLineScanner(r *os.File, buf []byte) *LineScanner {
	return &LineScanner{r: r, buf: buf}
}

func (s *LineScanner) Scan() bool {
	if s.err != nil {
		return false
	}
	for {
		if s.pos >= s.n {
			s.pos = 0
			s.n, s.err = s.r.Read(s.buf)
			if s.n == 0 {
				return false
			}
		}
		for i := s.pos; i < s.n; i++ {
			if s.buf[i] == '\n' {
				s.line = s.buf[s.pos:i]
				s.pos = i + 1
				return true
			}
		}
		// Line spans buffer boundary — not handled for simplicity
		s.line = s.buf[s.pos:s.n]
		s.pos = s.n
		return false
	}
}

func (s *LineScanner) Bytes() []byte { return s.line }
