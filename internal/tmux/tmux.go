package tmux

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Session represents a tmux session.
type Session struct {
	Name       string    `json:"name"`
	Windows    int       `json:"windows"`
	Panes      int       `json:"panes"`
	Created    time.Time `json:"created"`
	Attached   bool      `json:"attached"`
	Path       string    `json:"path"`
	WindowList []Window  `json:"window_list"`
	Related    bool      `json:"related"`
}

// Window represents a tmux window within a session.
type Window struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`   // may be empty
	Active bool   `json:"active"`
	Panes  int    `json:"panes"`
	Path   string `json:"path"`
}

// ListWindows returns all windows in a tmux session.
func ListWindows(sessionName string) ([]Window, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "list-windows", "-t", sessionName, "-F", "#{window_index}|#{window_name}|#{window_active}|#{window_panes}|#{pane_current_path}")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var windows []Window
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) != 5 {
			continue
		}

		index, _ := strconv.Atoi(parts[0])
		panes, _ := strconv.Atoi(parts[3])

		windows = append(windows, Window{
			Index:  index,
			Name:   parts[1],
			Active: parts[2] == "1",
			Panes:  panes,
			Path:   parts[4],
		})
	}

	return windows, scanner.Err()
}

// IsAvailable checks if the tmux binary exists on the system.
func IsAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// ListSessions returns all tmux sessions.
func ListSessions() ([]Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}|#{session_windows}|#{session_panes}|#{session_created}|#{session_attached}|#{session_path}")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var sessions []Session
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 6)
		if len(parts) != 6 {
			continue
		}

		// tmux outputs #{session_created} as a Unix timestamp
		createdUnix, _ := strconv.ParseInt(parts[3], 10, 64)
		created := time.Unix(createdUnix, 0)

		windows, _ := strconv.Atoi(parts[1])
		panes, _ := strconv.Atoi(parts[2])

		sessions = append(sessions, Session{
			Name:     parts[0],
			Windows:  windows,
			Panes:    panes,
			Created:  created,
			Attached: parts[4] == "1",
			Path:     parts[5],
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Populate windows for each session
	for i := range sessions {
		wins, err := ListWindows(sessions[i].Name)
		if err == nil {
			sessions[i].WindowList = wins
		}
	}

	return sessions, nil
}

// SearchSessionContent captures the pane content of all windows in the session and checks if it contains the query.
func SearchSessionContent(sessionName string, query string) bool {
	if query == "" {
		return false
	}
	windows, err := ListWindows(sessionName)
	if err != nil {
		return false
	}
	for _, win := range windows {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		target := fmt.Sprintf("%s:%d", sessionName, win.Index)
		cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-t", target)
		output, err := cmd.Output()
		cancel()
		if err == nil {
			if strings.Contains(strings.ToLower(string(output)), strings.ToLower(query)) {
				return true
			}
		}
	}
	return false
}

// SessionAttach manages live streaming to a tmux session's active pane.
type SessionAttach struct {
	sessionName  string
	windowIndex  *int          // nil = active window, explicit = target specific window
	stopOnce     sync.Once     // guards single close of stopCh
	stopCh       chan struct{} // closed by Stop() to signal Start() to exit
	doneCh       chan struct{} // closed when Start()'s goroutine exits
	mu           sync.RWMutex
	subscribers  map[chan string]bool
	lastContent  string
	started      atomic.Bool // true once Start() has begun running
	consecErrors int         // consecutive capture failures
}

const maxConsecErrors = 5 // stop attacher after this many consecutive capture failures

// target returns the tmux target string, including window index if set.
func (a *SessionAttach) target() string {
	if a.windowIndex != nil {
		return fmt.Sprintf("%s:%d", a.sessionName, *a.windowIndex)
	}
	return a.sessionName
}

// NewAttach creates a new SessionAttach for the given session.
// If windowIndex is >= 0, it targets that specific window; otherwise the active window.
func NewAttach(sessionName string, windowIndex int) *SessionAttach {
	a := &SessionAttach{
		sessionName: sessionName,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		subscribers: make(map[chan string]bool),
	}
	if windowIndex >= 0 {
		a.windowIndex = &windowIndex
	}
	return a
}

// Start begins the polling loop and blocks until Stop is called or the session dies.
func (a *SessionAttach) Start() {
	defer close(a.doneCh)
	a.started.Store(true)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	// Prime lastContent; empty is OK — we keep polling.
	a.mu.Lock()
	content, err := a.capturePane()
	if err != nil {
		a.consecErrors++
		a.mu.Unlock()
		if a.consecErrors >= maxConsecErrors {
			log.Printf("[tmux] capture failed %d times for %s, stopping: %v", maxConsecErrors, a.target(), err)
			return
		}
	} else {
		a.consecErrors = 0
		a.lastContent = content
		a.mu.Unlock()
		if content != "" {
			a.broadcast(content)
		}
	}

	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			content, err := a.capturePane()
			if err != nil {
				a.mu.Lock()
				a.consecErrors++
				n := a.consecErrors
				a.mu.Unlock()
				if n >= maxConsecErrors {
					log.Printf("[tmux] capture failed %d times for %s, stopping: %v", maxConsecErrors, a.target(), err)
					return
				}
				continue
			}
			// Empty capture is transient, not fatal — keep polling.
			if content == "" {
				a.mu.Lock()
				a.consecErrors = 0
				a.mu.Unlock()
				continue
			}
			a.mu.Lock()
			a.consecErrors = 0
			if content != a.lastContent {
				a.lastContent = content
				a.mu.Unlock()
				a.broadcast(content)
			} else {
				a.mu.Unlock()
			}
		}
	}
}

// Stop stops the polling loop and closes all subscriber channels.
// Safe to call multiple times and safe to call even if Start was never called.
func (a *SessionAttach) Stop() {
	a.stopOnce.Do(func() {
		close(a.stopCh)
	})
	// Only wait for doneCh if Start() was actually running.
	if a.started.Load() {
		<-a.doneCh
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for ch := range a.subscribers {
		delete(a.subscribers, ch)
		close(ch)
	}
}

// Subscribe returns a buffered channel that receives full pane content on changes.
func (a *SessionAttach) Subscribe() chan string {
	ch := make(chan string, 4)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.subscribers[ch] = true
	if a.lastContent != "" {
		ch <- a.lastContent
	}
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (a *SessionAttach) Unsubscribe(ch chan string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.subscribers[ch]; ok {
		delete(a.subscribers, ch)
		close(ch)
	}
}

// SendKeys sends literal text to the tmux session.
func (a *SessionAttach) SendKeys(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", a.target(), "-l", "--", text)
	return cmd.Run()
}

// SendKey sends a special key to the tmux session.
func (a *SessionAttach) SendKey(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", a.target(), key)
	return cmd.Run()
}

// Resize resizes the tmux pane to the given dimensions.
func (a *SessionAttach) Resize(cols, rows int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "resize-pane", "-t", a.target(), "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows))
	return cmd.Run()
}

// Done returns a channel that is closed when polling ends.
func (a *SessionAttach) Done() <-chan struct{} {
	return a.doneCh
}

// capturePane captures the current pane content.
func (a *SessionAttach) capturePane() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-e", "-t", a.target())
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("capture-pane: %w: %s", err, string(output))
	}
	return string(output), nil
}

// broadcast sends content to all subscriber channels non-blocking.
func (a *SessionAttach) broadcast(content string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for ch := range a.subscribers {
		select {
		case ch <- content:
		default:
		}
	}
}
