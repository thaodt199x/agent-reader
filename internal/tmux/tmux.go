package tmux

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Session represents a tmux session.
type Session struct {
	Name     string    `json:"name"`
	Windows  int       `json:"windows"`
	Panes    int       `json:"panes"`
	Created  time.Time `json:"created"`
	Attached bool      `json:"attached"`
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

	cmd := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}|#{session_windows}|#{session_panes}|#{session_created}|#{session_attached}")
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
		parts := strings.SplitN(line, "|", 5)
		if len(parts) != 5 {
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
		})
	}

	return sessions, scanner.Err()
}

// SessionAttach manages live streaming to a tmux session's active pane.
type SessionAttach struct {
	sessionName string
	stopOnce    sync.Once     // guards single close of stopCh
	stopCh      chan struct{} // closed by Stop() to signal Start() to exit
	doneCh      chan struct{} // closed when Start()'s goroutine exits
	mu          sync.RWMutex
	subscribers map[chan string]bool
	lastContent string
	started     atomic.Bool // true once Start() has begun running
}

// NewAttach creates a new SessionAttach for the given session.
func NewAttach(sessionName string) *SessionAttach {
	return &SessionAttach{
		sessionName: sessionName,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		subscribers: make(map[chan string]bool),
	}
}

// Start begins the polling loop and blocks until Stop is called or the session dies.
func (a *SessionAttach) Start() {
	defer close(a.doneCh)
	a.started.Store(true)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	// Prime lastContent; empty is OK — we keep polling.
	a.mu.Lock()
	a.lastContent = a.capturePane()
	if a.lastContent != "" {
		content := a.lastContent
		a.mu.Unlock()
		a.broadcast(content)
	} else {
		a.mu.Unlock()
	}

	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			content := a.capturePane()
			// Empty capture is transient, not fatal — keep polling.
			if content == "" {
				continue
			}
			a.mu.Lock()
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

	cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", a.sessionName, "-l", "--", text)
	return cmd.Run()
}

// SendKey sends a special key to the tmux session.
func (a *SessionAttach) SendKey(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", a.sessionName, key)
	return cmd.Run()
}

// Resize resizes the tmux pane to the given dimensions.
func (a *SessionAttach) Resize(cols, rows int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "resize-pane", "-t", a.sessionName, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows))
	return cmd.Run()
}

// Done returns a channel that is closed when polling ends.
func (a *SessionAttach) Done() <-chan struct{} {
	return a.doneCh
}

// capturePane captures the current pane content.
func (a *SessionAttach) capturePane() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-e", "-t", a.sessionName)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(output)
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
