// Package server provides the HTTP + WebSocket server.
package server

import (
	"bufio"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-web/internal/fsbrowse"
	"agent-web/internal/hub"
	"agent-web/internal/jsonl"
	"agent-web/internal/llm"
	"agent-web/internal/rpc"
	"agent-web/internal/watcher"

	"github.com/gorilla/websocket"
)

//go:embed static/dist/*
var staticFS embed.FS

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// rpcManager manages active RPC sessions.
type rpcManager struct {
	mu       sync.Mutex
	sessions map[string]*rpc.Session // sessionID -> session
}

func newRPCManager() *rpcManager {
	return &rpcManager{
		sessions: make(map[string]*rpc.Session),
	}
}

func (m *rpcManager) Get(id string) *rpc.Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *rpcManager) Set(id string, s *rpc.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[id] = s
}

func (m *rpcManager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

// Server ties together the HTTP server, WebSocket hub, and file watchers.
type Server struct {
	hub               *hub.Hub
	watcher           *watcher.Watcher       // pi-agent watcher
	claudeWatcher     *watcher.ClaudeWatcher // Claude Code watcher (may be nil)
	codexWatcher      *watcher.CodexWatcher  // Codex watcher (may be nil)
	rpcMgr            *rpcManager
	sessionsDir       string
	claudeProjectsDir string
	codexSessionsDir  string
	fsbrowse          *fsbrowse.Service   // filesystem browsing service (may be nil)
	llmClient         *llm.LMStudioClient // local LLM client for translation
}

// New creates a new Server.
func New(sessionsDir, claudeProjectsDir, codexSessionsDir, allowedRootsCSV string) (*Server, error) {
	w, err := watcher.New(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}

	h := hub.New()

	s := &Server{
		hub:               h,
		watcher:           w,
		rpcMgr:            newRPCManager(),
		sessionsDir:       sessionsDir,
		claudeProjectsDir: claudeProjectsDir,
		codexSessionsDir:  codexSessionsDir,
		llmClient:         llm.NewLMStudioClient(),
	}

	// Initialize filesystem browsing service if roots are configured
	if allowedRootsCSV != "" {
		s.fsbrowse = fsbrowse.New(allowedRootsCSV)
		log.Printf("[server] filesystem browsing enabled with roots: %s", allowedRootsCSV)
	}

	// Try to create Claude watcher (optional — skip if dir doesn't exist)
	if claudeProjectsDir != "" {
		if info, err := os.Stat(claudeProjectsDir); err == nil && info.IsDir() {
			cw, err := watcher.NewClaudeWatcher(claudeProjectsDir)
			if err != nil {
				log.Printf("[server] warning: could not create Claude watcher: %v", err)
			} else {
				s.claudeWatcher = cw
				log.Printf("[server] Claude Code watcher enabled: %s", claudeProjectsDir)
			}
		} else {
			log.Printf("[server] Claude projects dir not found, skipping: %s", claudeProjectsDir)
		}
	}

	// Try to create Codex watcher (optional — skip if dir doesn't exist)
	if codexSessionsDir != "" {
		if info, err := os.Stat(codexSessionsDir); err == nil && info.IsDir() {
			cw, err := watcher.NewCodexWatcher(codexSessionsDir)
			if err != nil {
				log.Printf("[server] warning: could not create Codex watcher: %v", err)
			} else {
				s.codexWatcher = cw
				log.Printf("[server] Codex watcher enabled: %s", codexSessionsDir)
			}
		} else {
			log.Printf("[server] Codex sessions dir not found, skipping: %s", codexSessionsDir)
		}
	}

	return s, nil
}

// Start launches the HTTP server on the given address.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()

	// WebSocket endpoint
	mux.HandleFunc("/ws", s.handleWS)

	// REST API
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/create", s.handleSessionCreate)
	mux.HandleFunc("/api/sessions/", s.handleSessionByID)

	// RPC endpoints
	mux.HandleFunc("/api/rpc/start", s.handleRPCStart)
	mux.HandleFunc("/api/rpc/stop", s.handleRPCStop)
	mux.HandleFunc("/api/rpc/send", s.handleRPCSend)
	mux.HandleFunc("/api/rpc/get_state", s.handleRPCGetState)
	mux.HandleFunc("/api/rpc/get_commands", s.handleRPCCOmmands)
	mux.HandleFunc("/api/rpc/get_models", s.handleRPCGetModels)
	mux.HandleFunc("/api/rpc/set_model", s.handleRPCSetModel)
	mux.HandleFunc("/api/rpc/cycle_model", s.handleRPCCycleModel)
	mux.HandleFunc("/api/rpc/status", s.handleRPCStatus)

	// Image upload
	mux.HandleFunc("/api/images/upload", s.handleImageUpload)
	mux.HandleFunc("/api/images/view", s.handleImageView)

	// Filesystem browsing (requires allowed roots)
	if s.fsbrowse != nil {
		mux.HandleFunc("/api/fs/browse", s.handleFSBrowse)
		mux.HandleFunc("/api/fs/search", s.handleFSSearch)
		mux.HandleFunc("/api/fs/read", s.handleFSRead)
	}

	// Translation
	mux.HandleFunc("/api/translate", s.handleTranslate)

	// Static files (Svelte SPA with fallback)
	staticSub, err := fs.Sub(staticFS, "static/dist")
	if err == nil {
		mux.Handle("/", spaHandler(staticSub))
	} else {
		mux.Handle("/", http.FileServer(http.Dir("internal/server/static/dist")))
	}

	log.Printf("[server] listening on %s", addr)
	log.Printf("[server] WebSocket: ws://localhost%s/ws", addr[strings.Index(addr, ":"):])
	log.Printf("[server] Sessions dir: %s", s.sessionsDir)

	s.hub.SetSubscribeCallback(s.onSubscribe)
	go s.hub.Run()
	go s.hub.SubscribeWatcher(s.watcher)
	s.watcher.Start()

	if s.claudeWatcher != nil {
		go s.hub.SubscribeClaudeWatcher(s.claudeWatcher)
		s.claudeWatcher.Start()
	}
	if s.codexWatcher != nil {
		go s.hub.SubscribeCodexWatcher(s.codexWatcher)
		s.codexWatcher.Start()
	}

	return http.ListenAndServe(addr, mux)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() {
	// Stop all RPC sessions
	s.rpcMgr.mu.Lock()
	for id, sess := range s.rpcMgr.sessions {
		if sess.IsRunning() {
			sess.Stop()
		}
		delete(s.rpcMgr.sessions, id)
	}
	s.rpcMgr.mu.Unlock()

	s.watcher.Stop()
	if s.claudeWatcher != nil {
		s.claudeWatcher.Stop()
	}
	if s.codexWatcher != nil {
		s.codexWatcher.Stop()
	}
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

// ===== REST API =====

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/sessions" {
		return
	}

	// Parse page parameter (default: 1)
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
		if page < 1 {
			page = 1
		}
	}

	sessions := s.listSessions()

	// Parse parameters
	groupBy := r.URL.Query().Get("group_by")
	sortBy := r.URL.Query().Get("sort")

	var total int

	if groupBy == "project" {
		// Group sessions by project
		groups := make(map[string][]SessionInfo)
		for _, sess := range sessions {
			key := sess.Project
			if key == "" {
				key = sess.CWD
			}
			if key == "" {
				key = "unknown"
			}
			groups[key] = append(groups[key], sess)
		}

		// Always sort sessions within each project group by timestamp descending
		for key := range groups {
			sort.Slice(groups[key], func(i, j int) bool {
				return groups[key][i].Timestamp.After(groups[key][j].Timestamp)
			})
		}

		// Create a list of project keys
		type projectMeta struct {
			key             string
			newestTimestamp time.Time
		}
		var projects []projectMeta
		for key, list := range groups {
			newest := time.Time{}
			if len(list) > 0 {
				newest = list[0].Timestamp
			}
			projects = append(projects, projectMeta{
				key:             key,
				newestTimestamp: newest,
			})
		}

		// Sort the projects
		if sortBy == "alphabetical" {
			sort.Slice(projects, func(i, j int) bool {
				return strings.ToLower(projects[i].key) < strings.ToLower(projects[j].key)
			})
		} else {
			// default to last_updated
			sort.Slice(projects, func(i, j int) bool {
				return projects[i].newestTimestamp.After(projects[j].newestTimestamp)
			})
		}

		total = len(projects)

		// Paginate projects: 50 projects per page
		const projectPageSize = 50
		offset := (page - 1) * projectPageSize
		var paginatedProjects []projectMeta
		if offset < total {
			end := offset + projectPageSize
			if end > total {
				end = total
			}
			paginatedProjects = projects[offset:end]
		}

		// Collect all sessions for the paginated projects
		var resultSessions []SessionInfo
		for _, p := range paginatedProjects {
			resultSessions = append(resultSessions, groups[p.key]...)
		}
		sessions = resultSessions

	} else {
		// Flat pagination
		// Sort based on "sort" parameter
		if sortBy == "alphabetical" {
			sort.Slice(sessions, func(i, j int) bool {
				pI := sessions[i].Project
				if pI == "" {
					pI = sessions[i].CWD
				}
				pI = strings.ToLower(pI)

				pJ := sessions[j].Project
				if pJ == "" {
					pJ = sessions[j].CWD
				}
				pJ = strings.ToLower(pJ)

				if pI != pJ {
					return pI < pJ
				}
				return sessions[i].Timestamp.After(sessions[j].Timestamp)
			})
		} else {
			// Default to last_updated
			sort.Slice(sessions, func(i, j int) bool {
				return sessions[i].Timestamp.After(sessions[j].Timestamp)
			})
		}

		total = len(sessions)

		// Paginate: 100 sessions per page
		const pageSize = 100
		offset := (page - 1) * pageSize
		if offset >= total {
			sessions = []SessionInfo{}
		} else {
			end := offset + pageSize
			if end > total {
				end = total
			}
			sessions = sessions[offset:end]
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": sessions,
		"total":    total,
	})
}

func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

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

// handleSessionCreate creates a new session with a given cwd and starts RPC.
func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CWD string `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.CWD == "" {
		http.Error(w, "missing cwd", http.StatusBadRequest)
		return
	}

	// Resolve to absolute path
	cwd, err := filepath.Abs(req.CWD)
	if err != nil {
		http.Error(w, "invalid cwd path", http.StatusBadRequest)
		return
	}

	// Validate cwd exists and is a directory
	info, err := os.Stat(cwd)
	if err != nil {
		http.Error(w, fmt.Sprintf("cwd does not exist: %s", req.CWD), http.StatusBadRequest)
		return
	}
	if !info.IsDir() {
		http.Error(w, "cwd is not a directory", http.StatusBadRequest)
		return
	}

	project := filepath.Base(cwd)

	// Create session directory
	sessionDir := filepath.Join(s.sessionsDir, project)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		http.Error(w, fmt.Sprintf("failed to create session dir: %v", err), http.StatusInternalServerError)
		return
	}

	// Generate session ID and path
	sessionID := generateSessionID()
	filename := fmt.Sprintf("%d_%s.jsonl", time.Now().Unix(), sessionID)
	sessionPath := filepath.Join(sessionDir, filename)

	// Create empty session file
	f, err := os.Create(sessionPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create session file: %v", err), http.StatusInternalServerError)
		return
	}
	f.Close()

	// Start RPC session
	sess := rpc.NewSessionWithCWD(sessionID, sessionPath, cwd, nil)

	if err := sess.Start(); err != nil {
		os.Remove(sessionPath)
		http.Error(w, fmt.Sprintf("failed to start rpc: %v", err), http.StatusInternalServerError)
		return
	}

	s.rpcMgr.Set(sessionID, sess)

	log.Printf("[server] created new session: %s (cwd=%s)", sessionID, cwd)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"session_id":  sessionID,
		"rpc_started": true,
	})
}

// generateSessionID creates a short random hex ID.
func generateSessionID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x", b)
}

// ===== Filesystem API =====

// handleFSBrowse lists the contents of a directory.
// GET /api/fs/browse?path=/Users/dt/code/project
func (s *Server) handleFSBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		http.Error(w, "missing path parameter", http.StatusBadRequest)
		return
	}

	// If path is empty or ".", return allowed roots as suggestions
	if dirPath == "." || dirPath == "" {
		var roots []fsbrowse.Entry
		for _, root := range s.fsbrowse.AllowedRoots() {
			roots = append(roots, fsbrowse.Entry{
				Name:  filepath.Base(root),
				Path:  root,
				IsDir: true,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"entries": roots,
		})
		return
	}

	entries, err := s.fsbrowse.Browse(dirPath, 200)
	if err != nil {
		log.Printf("[server] fs browse error: %v", err)
		if _, ok := err.(*fsbrowse.NotAllowedError); ok {
			http.Error(w, "access denied: path outside allowed roots", http.StatusForbidden)
			return
		}
		http.Error(w, fmt.Sprintf("browse error: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"entries": entries,
	})
}

// handleFSSearch searches for files/dirs under a root matching a query.
// GET /api/fs/search?root=/Users/dt/code&query=server
func (s *Server) handleFSSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := r.URL.Query().Get("root")
	query := r.URL.Query().Get("query")
	if root == "" {
		// If no root specified, search across all allowed roots
		var allResults []fsbrowse.Entry
		seen := make(map[string]bool)
		for _, allowedRoot := range s.fsbrowse.AllowedRoots() {
			results, err := s.fsbrowse.Search(allowedRoot, query, 30)
			if err != nil {
				continue
			}
			for _, e := range results {
				if !seen[e.Path] {
					seen[e.Path] = true
					allResults = append(allResults, e)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"entries": allResults,
		})
		return
	}

	if query == "" {
		http.Error(w, "missing query parameter", http.StatusBadRequest)
		return
	}

	results, err := s.fsbrowse.Search(root, query, 50)
	if err != nil {
		log.Printf("[server] fs search error: %v", err)
		if _, ok := err.(*fsbrowse.NotAllowedError); ok {
			http.Error(w, "access denied: path outside allowed roots", http.StatusForbidden)
			return
		}
		http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"entries": results,
	})
}

// handleFSRead reads a small file for @ mention preview.
// GET /api/fs/read?path=/Users/dt/code/project/file.go
func (s *Server) handleFSRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "missing path parameter", http.StatusBadRequest)
		return
	}

	content, err := s.fsbrowse.ReadFile(filePath, 32*1024)
	if err != nil {
		log.Printf("[server] fs read error: %v", err)
		if _, ok := err.(*fsbrowse.NotAllowedError); ok {
			http.Error(w, "access denied: path outside allowed roots", http.StatusForbidden)
			return
		}
		http.Error(w, fmt.Sprintf("read error: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"content":   content,
		"truncated": len(content) >= 32*1024,
	})
}

// ===== RPC API =====

// handleRPCStart starts an RPC session for a given session ID.
func (s *Server) handleRPCStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	// Check if already running
	if existing := s.rpcMgr.Get(req.SessionID); existing != nil && existing.IsRunning() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "already running",
		})
		return
	}

	// Find session file
	sessionFile := s.findSessionFile(req.SessionID)
	if sessionFile == "" {
		http.Error(w, "session file not found", http.StatusNotFound)
		return
	}

	// Create RPC session
	sess := rpc.NewSessionWithCWD(req.SessionID, sessionFile, "", nil)

	if err := sess.Start(); err != nil {
		log.Printf("[server] rpc start error: %v", err)
		http.Error(w, fmt.Sprintf("failed to start rpc: %v", err), http.StatusInternalServerError)
		return
	}

	s.rpcMgr.Set(req.SessionID, sess)

	log.Printf("[server] rpc session started: %s", req.SessionID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"session_id": req.SessionID,
	})
}

// handleRPCStop stops an RPC session.
func (s *Server) handleRPCStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	sess := s.rpcMgr.Get(req.SessionID)
	if sess == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "not running",
		})
		return
	}

	sess.Stop()
	s.rpcMgr.Delete(req.SessionID)

	log.Printf("[server] rpc session stopped: %s", req.SessionID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// handleRPCSend sends a command to an RPC session.
func (s *Server) handleRPCSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string                 `json:"session_id"`
		Command   map[string]interface{} `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" || req.Command == nil {
		http.Error(w, "missing session_id or command", http.StatusBadRequest)
		return
	}

	// Log the command for debugging (redact image data to avoid huge logs)
	cmdType, _ := req.Command["type"].(string)
	if cmdType == "prompt" {
		log.Printf("[server] rpc send: session=%s type=%s message_len=%d", req.SessionID, cmdType, len(fmt.Sprintf("%v", req.Command["message"])))
		if images, ok := req.Command["images"].([]interface{}); ok {
			log.Printf("[server] rpc send: session=%s images=%d", req.SessionID, len(images))
			for i, img := range images {
				if imgMap, ok := img.(map[string]interface{}); ok {
					data, _ := imgMap["data"].(string)
					mimeType, _ := imgMap["mimeType"].(string)
					log.Printf("[server] rpc send: image[%d] mimeType=%s data_len=%d", i, mimeType, len(data))
				}
			}
		}
	}

	sess := s.rpcMgr.Get(req.SessionID)
	if sess == nil || !sess.IsRunning() {
		http.Error(w, "rpc session not running", http.StatusNotFound)
		return
	}

	if err := sess.SendCommand(req.Command); err != nil {
		log.Printf("[server] rpc send error: %v", err)
		http.Error(w, fmt.Sprintf("send failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// handleRPCStatus returns the status of all RPC sessions.
func (s *Server) handleRPCStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.rpcMgr.mu.Lock()
	status := make(map[string]bool)
	for id, sess := range s.rpcMgr.sessions {
		status[id] = sess.IsRunning()
	}
	s.rpcMgr.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": status,
	})
}

// handleRPCCOmmands sends a get_commands command to an RPC session and returns the available commands.
func (s *Server) handleRPCCOmmands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	sess := s.rpcMgr.Get(req.SessionID)
	if sess == nil || !sess.IsRunning() {
		http.Error(w, "rpc session not running", http.StatusNotFound)
		return
	}

	resp, err := sess.SendCommandAndWait(map[string]interface{}{
		"type": "get_commands",
	}, 5*time.Second)
	if err != nil {
		log.Printf("[server] rpc get_commands error: %v", err)
		http.Error(w, fmt.Sprintf("get_commands failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

// handleRPCGetState sends a get_state command to an RPC session and returns the response.
func (s *Server) handleRPCGetState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	sess := s.rpcMgr.Get(req.SessionID)
	if sess == nil || !sess.IsRunning() {
		http.Error(w, "rpc session not running", http.StatusNotFound)
		return
	}

	resp, err := sess.SendCommandAndWait(map[string]interface{}{
		"type": "get_state",
	}, 5*time.Second)
	if err != nil {
		log.Printf("[server] rpc get_state error: %v", err)
		http.Error(w, fmt.Sprintf("get_state failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
	_ = resp
}

// handleRPCGetModels sends a get_available_models command and returns the model list.
func (s *Server) handleRPCGetModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	sess := s.rpcMgr.Get(req.SessionID)
	if sess == nil || !sess.IsRunning() {
		http.Error(w, "rpc session not running", http.StatusNotFound)
		return
	}

	resp, err := sess.SendCommandAndWait(map[string]interface{}{
		"type": "get_available_models",
	}, 10*time.Second)
	if err != nil {
		log.Printf("[server] rpc get_available_models error: %v", err)
		http.Error(w, fmt.Sprintf("get_available_models failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

// handleRPCSetModel sends a set_model command to switch the active model.
func (s *Server) handleRPCSetModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Provider  string `json:"provider"`
		ModelID   string `json:"model_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" || req.Provider == "" || req.ModelID == "" {
		http.Error(w, "missing session_id, provider, or model_id", http.StatusBadRequest)
		return
	}

	sess := s.rpcMgr.Get(req.SessionID)
	if sess == nil || !sess.IsRunning() {
		http.Error(w, "rpc session not running", http.StatusNotFound)
		return
	}

	resp, err := sess.SendCommandAndWait(map[string]interface{}{
		"type":     "set_model",
		"provider": req.Provider,
		"modelId":  req.ModelID,
	}, 10*time.Second)
	if err != nil {
		log.Printf("[server] rpc set_model error: %v", err)
		http.Error(w, fmt.Sprintf("set_model failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

// handleRPCCycleModel sends a cycle_model command to switch to the next model.
func (s *Server) handleRPCCycleModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	sess := s.rpcMgr.Get(req.SessionID)
	if sess == nil || !sess.IsRunning() {
		http.Error(w, "rpc session not running", http.StatusNotFound)
		return
	}

	resp, err := sess.SendCommandAndWait(map[string]interface{}{
		"type": "cycle_model",
	}, 10*time.Second)
	if err != nil {
		log.Printf("[server] rpc cycle_model error: %v", err)
		http.Error(w, fmt.Sprintf("cycle_model failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

// handleImageUpload handles image file uploads for RPC.
// Saves images to ~/.pi/images/ and returns the absolute path.
func (s *Server) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit to 10MB
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, fmt.Sprintf("file too large or invalid form: %v", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "missing image file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate MIME type
	mimeType := header.Header.Get("Content-Type")
	if !strings.HasPrefix(mimeType, "image/") {
		http.Error(w, "not an image file", http.StatusBadRequest)
		return
	}

	// Determine extension
	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = imageExtFromMime(mimeType)
	}

	// Create ~/.pi/images/ directory
	imagesDir, err := resolvePiImagesDir()
	if err != nil {
		log.Printf("[server] image upload: failed to resolve images dir: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		log.Printf("[server] image upload: failed to create images dir: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Generate unique filename
	b := make([]byte, 4)
	rand.Read(b)
	filename := fmt.Sprintf("%d_%08x%s", time.Now().UnixMilli(), b, ext)
	outputPath := filepath.Join(imagesDir, filename)

	// Write file
	out, err := os.Create(outputPath)
	if err != nil {
		log.Printf("[server] image upload: failed to create file: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		os.Remove(outputPath)
		log.Printf("[server] image upload: write error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Printf("[server] image uploaded: %s (%s, %d bytes)", outputPath, mimeType, header.Size)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"path":    outputPath,
	})
}

// resolvePiImagesDir resolves ~/.pi/images/ to an absolute path.
func resolvePiImagesDir() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(usr.HomeDir, ".pi", "images"), nil
}

// imageExtFromMime maps MIME types to file extensions.
func imageExtFromMime(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/svg+xml":
		return ".svg"
	case "image/tiff":
		return ".tiff"
	default:
		return ".png"
	}
}

// handleImageView serves images from ~/.pi/images/ or clipboard temp paths.
// The path is passed as a base64url-encoded query parameter: /api/images/view?p=<base64url>
func (s *Server) handleImageView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	encoded := r.URL.Query().Get("p")
	if encoded == "" {
		http.Error(w, "missing path parameter", http.StatusBadRequest)
		return
	}

	// Decode base64url (frontend strips padding, so use RawURLEncoding)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		// Fall back to padded URLEncoding for backwards compatibility
		decoded, err = base64.URLEncoding.DecodeString(encoded)
		if err != nil {
			http.Error(w, "invalid path encoding", http.StatusBadRequest)
			return
		}
	}
	imagePath := string(decoded)

	absPath, err := filepath.Abs(imagePath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// Security: verify the path is under ~/.pi/images/ OR is a clipboard temp file
	imagesDir, err := resolvePiImagesDir()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	isPiImage := strings.HasPrefix(absPath, imagesDir+string(filepath.Separator))
	isClipboardTemp := strings.HasPrefix(absPath, "/var/folders/") && strings.Contains(filepath.Base(absPath), "pi-clipboard-")

	if !isPiImage && !isClipboardTemp {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	// Validate file extension is an image
	ext := strings.ToLower(filepath.Ext(absPath))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".tiff":
		// okay
	default:
		http.Error(w, "not an image", http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "image not found", http.StatusNotFound)
		} else {
			http.Error(w, "read error", http.StatusInternalServerError)
		}
		return
	}

	// Set cache headers (images are immutable once uploaded)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", imageMimeFromExt(ext))
	w.Write(data)
}

// imageMimeFromExt maps file extensions to MIME types.
func imageMimeFromExt(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	case ".tiff":
		return "image/tiff"
	default:
		return "application/octet-stream"
	}
}

// ===== Translation =====

// handleTranslate translates text to Vietnamese using local LLM (LM Studio).
// POST /api/translate { "text": "...", "target_lang": "vi" }
func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text       string `json:"text"`
		TargetLang string `json:"target_lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.Text == "" {
		http.Error(w, "missing text", http.StatusBadRequest)
		return
	}

	if req.TargetLang == "" {
		req.TargetLang = "vi" // default to Vietnamese
	}

	log.Printf("[server] translate request: text_len=%d target=%s", len(req.Text), req.TargetLang)

	translated, err := s.llmClient.Translate(req.Text, req.TargetLang)
	if err != nil {
		log.Printf("[server] translate error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	log.Printf("[server] translate success: result_len=%d", len(translated))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"translated": translated,
	})
}

// ===== WebSocket =====

// onSubscribe is called when a WebSocket client subscribes to a session.
func (s *Server) onSubscribe(sessionID string, client *hub.Client) {
	sessions := s.listSessions()
	var sessionFile string
	var sessionAgent string
	for i := range sessions {
		if sessions[i].ID == sessionID {
			sessionFile = sessions[i].File
			sessionAgent = sessions[i].Agent
			break
		}
	}

	if sessionFile == "" {
		log.Printf("[server] session file not found for %s", sessionID)
		return
	}

	log.Printf("[server] replaying session %s (agent=%s) from %s", sessionID, sessionAgent, sessionFile)

	if sessionAgent == "codex" {
		dec, err := jsonl.NewCodexDecoder(sessionFile, 0)
		if err != nil {
			log.Printf("[server] open codex decoder: %v", err)
			return
		}
		defer dec.Close()

		for {
			event, err := dec.Next()
			if err != nil {
				break
			}
			if event == nil {
				continue
			}

			msg := hub.WSMessage{
				Type:      "event",
				SessionID: sessionID,
				Data:      event.Raw,
				Time:      time.Now(),
			}
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}

			select {
			case <-client.Closed():
				return
			default:
			}
			select {
			case client.Send() <- data:
			default:
			}
		}
	} else if sessionAgent == "claude" {
		// Use Claude decoder to normalize events
		dec, err := jsonl.NewClaudeDecoder(sessionFile, 0)
		if err != nil {
			log.Printf("[server] open claude decoder: %v", err)
			return
		}
		defer dec.Close()

		for {
			event, err := dec.Next()
			if err != nil {
				break
			}
			if event == nil {
				continue
			}

			msg := hub.WSMessage{
				Type:      "event",
				SessionID: sessionID,
				Data:      event.Raw,
				Time:      time.Now(),
			}
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}

			select {
			case <-client.Closed():
				return
			default:
			}
			select {
			case client.Send() <- data:
			default:
			}
		}
	} else {
		// pi-agent: use existing scanner approach
		f, err := os.Open(sessionFile)
		if err != nil {
			log.Printf("[server] open session file: %v", err)
			return
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

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

			select {
			case <-client.Closed():
				return
			default:
			}
			select {
			case client.Send() <- data:
			default:
			}
		}

		if err := scanner.Err(); err != nil && err != io.EOF {
			log.Printf("[server] scan error: %v", err)
		}
	}

	log.Printf("[server] finished replaying session %s", sessionID)
}

// ===== Helpers =====

// findSessionFile finds the JSONL file for a given session ID.
func (s *Server) findSessionFile(sessionID string) string {
	sessions := s.listSessions()
	for i := range sessions {
		if sessions[i].ID == sessionID {
			return sessions[i].File
		}
	}
	return ""
}

// SessionInfo is returned by the /api/sessions endpoint.
type SessionInfo struct {
	ID               string    `json:"id"`
	Project          string    `json:"project"`
	CWD              string    `json:"cwd"`
	Model            string    `json:"model"`
	ContextWindow    int64     `json:"context_window"`
	Agent            string    `json:"agent"`
	Timestamp        time.Time `json:"timestamp"`
	FirstUserMessage string    `json:"first_user_message"`
	LastMessageTime  string    `json:"last_message_time"`
	File             string    `json:"file"`
	LineCount        int       `json:"line_count"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	TotalTokens      int64     `json:"total_tokens"`
	TotalCost        float64   `json:"total_cost"`
}

// listSessions scans pi-agent, Claude Code, and Codex session directories.
func (s *Server) listSessions() []SessionInfo {
	var sessions []SessionInfo

	// Scan pi-agent sessions
	filepath.WalkDir(s.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		info := SessionInfo{File: path, Agent: "pi"}

		dir := filepath.Dir(path)
		info.Project = filepath.Base(dir)

		base := filepath.Base(path)
		for i := len(base) - 1; i >= 0; i-- {
			if base[i] == '_' {
				info.ID = base[i+1 : len(base)-len(".jsonl")]
				break
			}
		}

		info.LineCount, info.CWD, info.Model, info.InputTokens, info.OutputTokens, info.TotalTokens, info.TotalCost, info.ContextWindow = aggregateSessionData(path, "pi")
		info.FirstUserMessage = getFirstUserMessage(path, "pi")
		info.LastMessageTime = getLastMessageTime(path)

		if fi, err := d.Info(); err == nil {
			info.Timestamp = fi.ModTime()
		}

		sessions = append(sessions, info)
		return nil
	})

	// Scan Claude Code sessions
	if s.claudeProjectsDir != "" {
		filepath.WalkDir(s.claudeProjectsDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			// Skip subagents
			if strings.Contains(path, "/subagents/") {
				return nil
			}

			info := SessionInfo{File: path, Agent: "claude"}

			base := filepath.Base(path)
			info.ID = strings.TrimSuffix(base, ".jsonl")

			info.LineCount, info.CWD, info.Model, info.InputTokens, info.OutputTokens, info.TotalTokens, info.TotalCost, info.ContextWindow = aggregateSessionData(path, "claude")
			info.FirstUserMessage = getFirstUserMessage(path, "claude")
			info.LastMessageTime = getLastMessageTime(path)

			if fi, err := d.Info(); err == nil {
				info.Timestamp = fi.ModTime()
			}

			// For Claude, project name comes from cwd
			if info.CWD != "" {
				info.Project = filepath.Base(info.CWD)
			} else {
				info.Project = filepath.Base(filepath.Dir(path))
			}

			sessions = append(sessions, info)
			return nil
		})
	}

	// Scan Codex sessions
	if s.codexSessionsDir != "" {
		filepath.WalkDir(s.codexSessionsDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}

			meta, ok := readCodexSessionInfo(path)
			if !ok {
				return nil
			}

			info := SessionInfo{
				ID:      meta.ID,
				File:    path,
				Agent:   "codex",
				CWD:     meta.CWD,
				Project: filepath.Base(meta.CWD),
				Model:   meta.Model,
			}

			info.LineCount, info.CWD, info.Model, info.InputTokens, info.OutputTokens, info.TotalTokens, info.TotalCost, info.ContextWindow = aggregateSessionData(path, "codex")
			if info.CWD == "" {
				info.CWD = meta.CWD
			}
			if info.Model == "" {
				info.Model = meta.Model
			}
			if info.Project == "." || info.Project == "" {
				if info.CWD != "" {
					info.Project = filepath.Base(info.CWD)
				} else {
					info.Project = filepath.Base(filepath.Dir(path))
				}
			}
			info.FirstUserMessage = getFirstUserMessage(path, "codex")
			info.LastMessageTime = getLastMessageTime(path)

			if fi, err := d.Info(); err == nil {
				info.Timestamp = fi.ModTime()
			}

			sessions = append(sessions, info)
			return nil
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Timestamp.After(sessions[j].Timestamp)
	})

	return sessions
}

// countLinesAndCWD reads the JSONL file to get CWD, model, and counts total lines.
func countLinesAndCWD(path string) (int, string, string) {
	lineCount, cwd, model, _, _, _, _, _ := aggregateSessionData(path, "pi")
	return lineCount, cwd, model
}

func readCodexSessionInfo(path string) (jsonl.CodexSessionMeta, bool) {
	f, err := os.Open(path)
	if err != nil {
		return jsonl.CodexSessionMeta{}, false
	}
	defer f.Close()

	scanner := NewLineScanner(f, make([]byte, 32*1024))
	for scanner.Scan() {
		meta, ok := jsonl.ParseCodexSessionMeta(scanner.Bytes())
		if !ok {
			continue
		}
		if jsonl.IsCodexUserSession(meta) {
			return meta, true
		}
		return jsonl.CodexSessionMeta{}, false
	}

	return jsonl.CodexSessionMeta{}, false
}

// getContextWindow returns the context window size for a given model ID.
// Returns 0 if the model is unknown.
func getContextWindow(model string) int64 {
	if model == "" {
		return 0
	}
	// Strip provider prefix (e.g., "anthropic.", "us.anthropic.", "bedrock.")
	cleanModel := model
	for _, prefix := range []string{"us.anthropic.", "eu.anthropic.", "au.anthropic.", "global.anthropic.", "anthropic.", "bedrock.", "openai.", "google.", "meta.", "mistral.", "deepseek.", "qwen.", "zai.", "minimax.", "nvidia.", "moonshot.", "moonshotai.", "writer.", "amazon."} {
		if strings.HasPrefix(cleanModel, prefix) {
			cleanModel = strings.TrimPrefix(cleanModel, prefix)
			break
		}
	}
	// Strip Bedrock version suffix (e.g., "-v1:0")
	cleanModel = strings.TrimSuffix(cleanModel, "-v1:0")

	switch cleanModel {
	// Claude models
	case "claude-opus-4-7", "claude-opus-4.7":
		return 200000
	case "claude-opus-4-6", "claude-opus-4.6":
		return 200000
	case "claude-opus-4-5", "claude-opus-4.5", "claude-opus-4-20251101":
		return 200000
	case "claude-opus-4-1", "claude-opus-4.1", "claude-opus-4-20250805":
		return 200000
	case "claude-opus-4", "claude-opus-4-20250514":
		return 200000
	case "claude-sonnet-4-6", "claude-sonnet-4.6":
		return 200000
	case "claude-sonnet-4-5", "claude-sonnet-4.5", "claude-sonnet-4-20250929":
		return 200000
	case "claude-sonnet-4", "claude-sonnet-4-20250514":
		return 200000
	case "claude-haiku-4-5", "claude-haiku-4.5", "claude-haiku-4-20251001":
		return 200000
	case "claude-3-7-sonnet", "claude-3-7-sonnet-20250219":
		return 200000
	case "claude-3-5-sonnet", "claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20240620":
		return 200000
	case "claude-3-5-haiku", "claude-3-5-haiku-20241022":
		return 200000
	case "claude-3-haiku", "claude-3-haiku-20240307":
		return 200000
	// GPT models
	case "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4o", "gpt-4o-mini", "gpt-4-turbo":
		return 128000
	case "gpt-4":
		return 8192
	case "gpt-3.5-turbo":
		return 16385
	case "o1", "o1-mini", "o1-preview":
		return 128000
	case "o3", "o3-mini", "o4-mini":
		return 200000
	// Gemini models
	case "gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.0-flash":
		return 1048576
	case "gemini-1.5-pro", "gemini-1.5-flash":
		return 2097152
	// Claude extended thinking variants
	case "claude-sonnet-4-20250514-extended-thinking", "claude-sonnet-4-20250514-thinking":
		return 200000
	case "claude-opus-4-20250514-extended-thinking", "claude-opus-4-20250514-thinking":
		return 200000
	default:
		return 0
	}
}

// aggregateSessionData reads the JSONL file and aggregates all session metadata.
// The agent parameter distinguishes between "pi" and "claude" formats.
func aggregateSessionData(path string, agent string) (lineCount int, cwd string, model string, inputTokens, outputTokens, totalTokens int64, totalCost float64, contextWindow int64) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", "", 0, 0, 0, 0, 0
	}
	defer f.Close()

	count := 0
	buf := make([]byte, 32*1024)
	scanner := NewLineScanner(f, buf)

	for scanner.Scan() {
		count++
		line := scanner.Bytes()

		if agent == "codex" {
			if meta, ok := jsonl.ParseCodexSessionMeta(line); ok {
				if cwd == "" {
					cwd = meta.CWD
				}
				if model == "" {
					model = meta.Model
				}
				continue
			}
			if model == "" {
				var ctx struct {
					Type    string `json:"type"`
					Model   string `json:"model"`
					Payload struct {
						Type  string `json:"type"`
						Model string `json:"model"`
					} `json:"payload"`
				}
				if json.Unmarshal(line, &ctx) == nil {
					if ctx.Type == "turn_context" {
						if ctx.Model != "" {
							model = ctx.Model
						} else if ctx.Payload.Model != "" {
							model = ctx.Payload.Model
						}
					} else if ctx.Type == "response_item" && ctx.Payload.Type == "turn_context" && ctx.Payload.Model != "" {
						model = ctx.Payload.Model
					}
				}
			}
			continue
		}

		if agent == "pi" {
			// pi-agent: cwd from first-line session event
			if count == 1 {
				var first struct {
					Type string `json:"type"`
					CWD  string `json:"cwd"`
				}
				json.Unmarshal(line, &first)
				if first.Type == "session" {
					cwd = first.CWD
				}
			}
			// Look for model_change events
			if model == "" {
				var mc struct {
					Type    string `json:"type"`
					ModelID string `json:"modelId"`
				}
				if json.Unmarshal(line, &mc) == nil && mc.Type == "model_change" && mc.ModelID != "" {
					model = mc.ModelID
				}
			}
			// Look for assistant messages with model field
			if model == "" {
				var me struct {
					Type    string `json:"type"`
					Message struct {
						Role  string `json:"role"`
						Model string `json:"model"`
					} `json:"message"`
				}
				if json.Unmarshal(line, &me) == nil && me.Type == "message" && me.Message.Role == "assistant" && me.Message.Model != "" {
					model = me.Message.Model
				}
			}
			// Aggregate usage from assistant messages (pi format)
			var usageCheck struct {
				Type    string `json:"type"`
				Message struct {
					Role  string `json:"role"`
					Usage *struct {
						Input       int64 `json:"input"`
						Output      int64 `json:"output"`
						TotalTokens int64 `json:"totalTokens"`
						Cost        struct {
							Total float64 `json:"total"`
						} `json:"cost"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &usageCheck) == nil && usageCheck.Type == "message" && usageCheck.Message.Role == "assistant" && usageCheck.Message.Usage != nil {
				u := usageCheck.Message.Usage
				inputTokens += u.Input
				outputTokens += u.Output
				totalTokens += u.TotalTokens
				totalCost += u.Cost.Total
			}
		} else {
			// Claude Code: cwd from any event's top-level field
			if cwd == "" {
				var cwCheck struct {
					CWD string `json:"cwd"`
				}
				json.Unmarshal(line, &cwCheck)
				if cwCheck.CWD != "" {
					cwd = cwCheck.CWD
				}
			}
			// Claude Code: assistant messages with model and snake_case usage
			var claudeCheck struct {
				Type    string `json:"type"`
				Message struct {
					Role  string `json:"role"`
					Model string `json:"model"`
					Usage *struct {
						InputTokens  int64 `json:"input_tokens"`
						OutputTokens int64 `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &claudeCheck) == nil && claudeCheck.Type == "assistant" && claudeCheck.Message.Role == "assistant" {
				if model == "" && claudeCheck.Message.Model != "" {
					model = claudeCheck.Message.Model
				}
				if claudeCheck.Message.Usage != nil {
					inputTokens += claudeCheck.Message.Usage.InputTokens
					outputTokens += claudeCheck.Message.Usage.OutputTokens
					totalTokens += claudeCheck.Message.Usage.InputTokens + claudeCheck.Message.Usage.OutputTokens
				}
			}
		}
	}

	contextWindow = getContextWindow(model)
	return count, cwd, model, inputTokens, outputTokens, totalTokens, totalCost, contextWindow
}

// getFirstUserMessage reads the JSONL file and returns the text content of the first user message (truncated to 200 chars).
func getFirstUserMessage(path string, agent string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, 32*1024)
	scanner := NewLineScanner(f, buf)

	for scanner.Scan() {
		line := scanner.Bytes()

		if agent == "pi" {
			var evt struct {
				Type    string `json:"type"`
				Message *struct {
					Role    string `json:"role"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &evt) == nil && evt.Type == "message" && evt.Message != nil && evt.Message.Role == "user" {
				for _, block := range evt.Message.Content {
					if block.Type == "text" && block.Text != "" {
						return truncateMessage(block.Text)
					}
				}
			}
		} else if agent == "codex" {
			var env jsonl.CodexEnvelope
			if json.Unmarshal(line, &env) == nil && env.Type == "response_item" {
				var msg jsonl.CodexMessage
				if json.Unmarshal(env.Payload, &msg) == nil && msg.Type == "message" && msg.Role == "user" {
					for _, block := range msg.Content {
						if (block.Type == "input_text" || block.Type == "text") && block.Text != "" {
							return truncateMessage(block.Text)
						}
					}
				}
			}
		} else {
			// Claude: content can be string or array
			var evtStr struct {
				Type    string `json:"type"`
				Message *struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &evtStr) == nil && evtStr.Type == "user" && evtStr.Message != nil && evtStr.Message.Role == "user" && evtStr.Message.Content != "" {
				return truncateMessage(evtStr.Message.Content)
			}
			var evtArr struct {
				Type    string `json:"type"`
				Message *struct {
					Role    string `json:"role"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &evtArr) == nil && evtArr.Type == "user" && evtArr.Message != nil && evtArr.Message.Role == "user" {
				for _, block := range evtArr.Message.Content {
					if block.Type == "text" && block.Text != "" {
						return truncateMessage(block.Text)
					}
				}
			}
		}
	}
	return ""
}

func truncateMessage(s string) string {
	const maxLen = 200
	if len(s) > maxLen {
		return s[:maxLen] + "…"
	}
	return s
}

// getLastMessageTime reads the last line of the JSONL file and returns a formatted timestamp.
func getLastMessageTime(path string) string {
	allBuf, err := os.ReadFile(path)
	if err != nil || len(allBuf) == 0 {
		return ""
	}

	// Find the last non-empty line (skip trailing newlines)
	end := len(allBuf) - 1
	for end >= 0 && allBuf[end] == '\n' {
		end--
	}
	if end < 0 {
		return ""
	}

	start := end
	for start > 0 && allBuf[start-1] != '\n' {
		start--
	}

	lastLine := allBuf[start : end+1]
	if len(lastLine) == 0 {
		return ""
	}

	var lineData struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(lastLine, &lineData); err != nil || lineData.Timestamp == "" {
		return ""
	}

	t, err := time.Parse(time.RFC3339Nano, lineData.Timestamp)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.000Z", lineData.Timestamp)
		if err != nil {
			return ""
		}
	}

	return formatRelativeTime(t)
}

// formatRelativeTime returns a human-readable relative time string.
func formatRelativeTime(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", mins)
	}
	if diff < 24*time.Hour {
		hours := int(diff.Hours())
		if hours == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", hours)
	}
	if diff < 7*24*time.Hour {
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}

	return t.Format("Jan 2")
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
		s.line = s.buf[s.pos:s.n]
		s.pos = s.n
		return false
	}
}

func (s *LineScanner) Bytes() []byte { return s.line }

// spaHandler serves the Svelte SPA with index.html fallback for client-side routing
func spaHandler(fileSystem fs.FS) http.Handler {
	index, err := fs.ReadFile(fileSystem, "index.html")
	if err != nil {
		log.Fatalf("failed to read index.html: %v", err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the requested file
		path := filepath.Join(".", r.URL.Path)
		f, err := fileSystem.Open(path)
		if err == nil {
			f.Close()
			http.FileServer(http.FS(fileSystem)).ServeHTTP(w, r)
			return
		}

		// Fallback to index.html for SPA routing
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(index)
	})
}
