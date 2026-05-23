# Window-Level Tmux Attach Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow users to select a specific window within a multi-window tmux session before attaching.

**Architecture:** Add `ListWindows` to Go tmux package, add `?window=N` query param to WebSocket handler, add `TmuxWindowPicker` Svelte component, update store type for `tmuxTerminalTarget`.

**Tech Stack:** Go (net/http, gorilla/websocket), Svelte 5, xterm.js

---

### Task 1: Add `ListWindows` function and window-aware `SessionAttach`

**Files:**
- Modify: `internal/tmux/tmux.go`
- Modify: `internal/tmux/tmux_test.go`

- [ ] **Step 1: Add `Window` struct and `ListWindows` function**

Add to `internal/tmux/tmux.go` after the `Session` struct (after line 21):

```go
// Window represents a tmux window within a session.
type Window struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`   // may be empty
	Active bool   `json:"active"`
	Panes  int    `json:"panes"`
}

// ListWindows returns all windows in a tmux session.
func ListWindows(sessionName string) ([]Window, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "list-windows", "-t", sessionName, "-F", "#{window_index}|#{window_name}|#{window_active}|#{window_panes}")
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
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}

		index, _ := strconv.Atoi(parts[0])
		panes, _ := strconv.Atoi(parts[3])

		windows = append(windows, Window{
			Index:  index,
			Name:   parts[1],
			Active: parts[2] == "1",
			Panes:  panes,
		})
	}

	return windows, scanner.Err()
}
```

- [ ] **Step 2: Add `windowIndex` field to `SessionAttach` and update `target()` helper**

Modify the `SessionAttach` struct (line 72) to add a `windowIndex *int` field:

```go
// SessionAttach manages live streaming to a tmux session's active pane.
type SessionAttach struct {
	sessionName string
	windowIndex *int          // nil = active window, explicit = target specific window
	stopOnce    sync.Once     // guards single close of stopCh
	stopCh      chan struct{} // closed by Stop() to signal Start() to exit
	doneCh      chan struct{} // closed when Start()'s goroutine exits
	mu          sync.RWMutex
	subscribers map[chan string]bool
	lastContent string
	started     atomic.Bool // true once Start() has begun running
}
```

Update `NewAttach` to accept an optional window index:

```go
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
```

- [ ] **Step 3: Add `target()` helper and update `capturePane`, `SendKeys`, `SendKey`**

Add a `target()` helper method after the struct definition:

```go
// target returns the tmux target string, including window index if set.
func (a *SessionAttach) target() string {
	if a.windowIndex != nil {
		return fmt.Sprintf("%s:%d", a.sessionName, *a.windowIndex)
	}
	return a.sessionName
}
```

Note: need to add `"fmt"` to imports.

Update `capturePane` (line 209) to use `a.target()`:

```go
cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-e", "-t", a.target())
```

Update `SendKeys` (line 177):

```go
cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", a.target(), "-l", "--", text)
```

Update `SendKey` (line 186):

```go
cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", a.target(), key)
```

- [ ] **Step 4: Write tests for `ListWindows` parsing**

Add to `internal/tmux/tmux_test.go`:

```go
func TestListWindows_Parsing(t *testing.T) {
	t.Parallel()

	input := `0|bash|1|1
1|nvim|0|1
2||0|2
`

	var windows []Window
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			t.Fatalf("expected 4 parts, got %d for line: %s", len(parts), line)
		}

		index, _ := strconv.Atoi(parts[0])
		panes, _ := strconv.Atoi(parts[3])

		windows = append(windows, Window{
			Index:  index,
			Name:   parts[1],
			Active: parts[2] == "1",
			Panes:  panes,
		})
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(windows) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(windows))
	}

	if windows[0].Index != 0 || windows[0].Name != "bash" || !windows[0].Active {
		t.Errorf("window 0: %+v", windows[0])
	}
	if windows[1].Index != 1 || windows[1].Name != "nvim" || windows[1].Active {
		t.Errorf("window 1: %+v", windows[1])
	}
	if windows[2].Index != 2 || windows[2].Name != "" || windows[2].Active || windows[2].Panes != 2 {
		t.Errorf("window 2: %+v", windows[2])
	}
}
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/dt/code/agent-reader && go test ./internal/tmux/... -v`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/tmux/tmux.go internal/tmux/tmux_test.go
git commit -m "feat(tmux): add ListWindows and window-aware SessionAttach"
```

---

### Task 2: Add `/api/tmux/sessions/:session/windows` REST endpoint and update WebSocket handler

**Files:**
- Modify: `internal/server/server.go`

- [ ] **Step 1: Add `handleTmuxWindows` handler**

Add after `handleTmuxSessions` (after line 1536):

```go
func (s *Server) handleTmuxWindows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionName := strings.TrimPrefix(r.URL.Path, "/api/tmux/sessions/")
	sessionName = strings.TrimSuffix(sessionName, "/windows")
	if sessionName == "" {
		http.Error(w, "missing session name", http.StatusBadRequest)
		return
	}

	if !tmux.IsAvailable() {
		http.Error(w, "tmux not available", http.StatusServiceUnavailable)
		return
	}

	windows, err := tmux.ListWindows(sessionName)
	if err != nil {
		if strings.Contains(err.Error(), "no server") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]tmux.Window{})
			return
		}
		log.Printf("[server] tmux list windows error: %v", err)
		http.Error(w, fmt.Sprintf("tmux error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(windows)
}
```

- [ ] **Step 2: Register the new route**

Add after line 200 in the route registration:

```go
mux.HandleFunc("/api/tmux/sessions/", s.handleTmuxWindows)
```

Note: This must be registered BEFORE any more specific route under `/api/tmux/sessions/`. Since `handleTmuxSessions` uses the exact path `/api/tmux/sessions` (no trailing slash), the Go mux will correctly route `/api/tmux/sessions` to `handleTmuxSessions` and `/api/tmux/sessions/foo/windows` to `handleTmuxWindows`.

- [ ] **Step 3: Update `handleTmuxWS` to accept `?window=N` query param**

Modify `handleTmuxWS` (line 1538):

After extracting `sessionName` (line 1539), add:

```go
windowIndex := -1
if w := r.URL.Query().Get("window"); w != "" {
	if n, err := strconv.Atoi(w); err == nil {
		windowIndex = n
	}
}

attachKey := sessionName
if windowIndex >= 0 {
	attachKey = fmt.Sprintf("%s:%d", sessionName, windowIndex)
}
```

Replace the attacher lookup block (lines 1554-1565) with:

```go
s.tmuxAttachMu.Lock()
attach, exists := s.tmuxAttachers[attachKey]
if !exists {
	attach = tmux.NewAttach(sessionName, windowIndex)
	s.tmuxAttachers[attachKey] = attach
	go func() {
		attach.Start()
		s.tmuxAttachMu.Lock()
		delete(s.tmuxAttachers, attachKey)
		s.tmuxAttachMu.Unlock()
	}()
}
s.tmuxAttachMu.Unlock()
```

Update the log line (line 1551):

```go
if windowIndex >= 0 {
	log.Printf("[server] tmux ws: attaching to session %q window %d", sessionName, windowIndex)
} else {
	log.Printf("[server] tmux ws: attaching to session %q", sessionName)
}
```

Update the detach log (line 1597):

```go
if windowIndex >= 0 {
	log.Printf("[server] tmux ws: detached from session %q window %d", sessionName, windowIndex)
} else {
	log.Printf("[server] tmux ws: detached from session %q", sessionName)
}
```

- [ ] **Step 4: Skip resize handling in WS handler for window-attached sessions**

Modify the switch block (lines 1611-1625). Only call `attach.Resize` when `windowIndex < 0` (session-level attach):

```go
switch msg.Type {
case "data":
	if msg.Content != "" {
		if err := attach.SendKeys(msg.Content); err != nil {
			sendJSON(map[string]interface{}{
				"type":  "error",
				"error": fmt.Sprintf("send-keys: %v", err),
			})
		}
	}
case "resize":
	if windowIndex < 0 && msg.Cols > 0 && msg.Rows > 0 {
		attach.Resize(msg.Cols, msg.Rows)
	}
}
```

- [ ] **Step 5: Verify build**

Run: `cd /Users/dt/code/agent-reader && go build ./...`
Expected: No errors

- [ ] **Step 6: Commit**

```bash
git add internal/server/server.go
git commit -m "feat(server): add tmux windows endpoint and window-aware WS handler"
```

---

### Task 3: Add `fetchTmuxWindows` API client and update stores

**Files:**
- Modify: `frontend/src/lib/api/tmux.js`
- Modify: `frontend/src/lib/stores/tmux.svelte.js`

- [ ] **Step 1: Add `fetchTmuxWindows` function**

Update `frontend/src/lib/api/tmux.js`:

```javascript
export async function fetchTmuxSessions() {
  const res = await fetch('/api/tmux/sessions');
  if (!res.ok) {
    throw new Error(`Failed to fetch tmux sessions: ${res.status}`);
  }
  return res.json();
}

export async function fetchTmuxWindows(sessionName) {
  const res = await fetch(`/api/tmux/sessions/${encodeURIComponent(sessionName)}/windows`);
  if (!res.ok) {
    throw new Error(`Failed to fetch tmux windows: ${res.status}`);
  }
  return res.json();
}
```

- [ ] **Step 2: Update stores**

Update `frontend/src/lib/stores/tmux.svelte.js`:

```javascript
import { writable } from 'svelte/store';

export const tmuxSessionPickerOpen = writable(false);
export const tmuxWindowPickerOpen = writable(false);
export const tmuxTerminalTarget = writable(null); // { session: string, window: number } | null
```

- [ ] **Step 3: Verify frontend builds**

Run: `cd /Users/dt/code/agent-reader/frontend && npm run build`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/api/tmux.js frontend/src/lib/stores/tmux.svelte.js
git commit -m "feat(frontend): add fetchTmuxWindows API and update tmux stores"
```

---

### Task 4: Create `TmuxWindowPicker` component and wire up flow

**Files:**
- Create: `frontend/src/lib/components/TmuxWindowPicker.svelte`
- Modify: `frontend/src/lib/components/TmuxSessionPicker.svelte`
- Modify: `frontend/src/lib/components/TmuxTerminalModal.svelte`
- Modify: `frontend/src/App.svelte`

- [ ] **Step 1: Create `TmuxWindowPicker.svelte`**

Create `frontend/src/lib/components/TmuxWindowPicker.svelte`:

```svelte
<script>
  import { tmuxWindowPickerOpen, tmuxTerminalTarget, tmuxSessionPickerOpen } from '$lib/stores/tmux.svelte.js';
  import { fetchTmuxWindows } from '$lib/api/tmux.js';
  import { Terminal, X, ArrowLeft } from '@lucide/svelte';

  let sessionName = $state('');
  let windows = $state([]);
  let loading = $state(false);
  let error = $state('');

  async function loadWindows() {
    loading = true;
    error = '';
    try {
      windows = await fetchTmuxWindows(sessionName);
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  function close() {
    tmuxWindowPickerOpen.set(false);
    tmuxSessionPickerOpen.set(true);
  }

  function connect(windowIndex) {
    tmuxWindowPickerOpen.set(false);
    tmuxTerminalTarget.set({ session: sessionName, window: windowIndex });
  }

  $effect(() => {
    if ($tmuxWindowPickerOpen) {
      sessionName = $tmuxWindowPickerOpen;
      loadWindows();
    }
  });
</script>

{#if $tmuxWindowPickerOpen}
  <div class="fixed inset-0 z-50 flex items-center justify-center">
    <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" onclick={close}></div>
    <div class="relative bg-ctp-mantle border border-ctp-surface0 rounded-2xl shadow-2xl w-[480px] max-w-[90vw] max-h-[70vh] animate-fadeIn overflow-hidden flex flex-col">
      <!-- Header -->
      <div class="px-6 pt-5 pb-4 border-b border-ctp-surface0">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-3">
            <button
              class="text-ctp-overlay0 hover:text-ctp-text transition-colors p-1 rounded-md hover:bg-ctp-surface0 flex items-center justify-center cursor-pointer"
              onclick={close}
            >
              <ArrowLeft size={16} />
            </button>
            <div class="w-8 h-8 rounded-lg bg-ctp-green/20 flex items-center justify-center text-ctp-green">
              <Terminal size={16} />
            </div>
            <div>
              <h3 class="text-sm font-semibold text-ctp-text">Choose window</h3>
              <p class="text-[11px] text-ctp-overlay0 mt-0.5 font-mono">{sessionName}</p>
            </div>
          </div>
          <button
            class="text-ctp-overlay0 hover:text-ctp-text transition-colors p-1 rounded-md hover:bg-ctp-surface0 flex items-center justify-center cursor-pointer"
            onclick={close}
          >
            <X class="h-4 w-4" />
          </button>
        </div>
      </div>

      <!-- Body -->
      <div class="px-6 py-4 flex-1 overflow-y-auto">
        {#if loading}
          <div class="flex items-center justify-center py-8 text-ctp-overlay0 text-sm">
            Loading windows...
          </div>
        {:else if error}
          <div class="flex items-center gap-2 px-3 py-3 rounded-lg text-xs text-ctp-red"
               style="background:color-mix(in srgb, #e95f59 10%, #ffffff)">
            <span>{error}</span>
          </div>
        {:else if windows.length === 0}
          <div class="text-center py-8 text-ctp-overlay0 text-sm">
            No windows found
          </div>
        {:else}
          <div class="space-y-2">
            {#each windows as win (win.index)}
              <button
                class="w-full flex items-center justify-between px-4 py-3 bg-ctp-crust border border-ctp-surface0 rounded-lg hover:border-ctp-surface1 transition-colors cursor-pointer text-left"
                onclick={() => connect(win.index)}
              >
                <div class="flex items-center gap-3">
                  <span class="w-[28px] h-[28px] rounded-md bg-ctp-green/20 flex items-center justify-center text-ctp-green font-mono text-sm font-bold">
                    {win.index}
                  </span>
                  <div>
                    <div class="text-sm font-medium text-ctp-text">
                      {win.name || 'window ' + win.index}
                    </div>
                    <div class="text-[11px] text-ctp-overlay0">
                      {win.panes}p
                      {#if win.active}<span class="text-ctp-green ml-1"> active</span>{/if}
                    </div>
                  </div>
                </div>
                <ArrowRight size={14} class="text-ctp-overlay0" />
              </button>
            {/each}
          </div>
        {/if}
      </div>

      <!-- Footer -->
      <div class="px-6 py-3 border-t border-ctp-surface0 flex justify-between items-center">
        <span class="text-[11px] text-ctp-overlay0">{windows.length} window{windows.length !== 1 ? 's' : ''}</span>
        <button
          class="flex items-center gap-1 px-3 py-1.5 rounded-md text-xs font-medium text-ctp-overlay0 hover:text-ctp-text hover:bg-ctp-surface0 transition-colors cursor-pointer"
          onclick={loadWindows}
          disabled={loading}
        >
          Refresh
        </button>
      </div>
    </div>
  </div>
{/if}
```

- [ ] **Step 2: Update `TmuxSessionPicker.svelte` — change `connect` to route multi-window sessions**

Modify the `connect` function (line 29) to accept the full session object:

```javascript
function connect(session) {
  tmuxSessionPickerOpen.set(false);
  if (session.windows <= 1) {
    // Single window: connect directly
    tmuxTerminalTarget.set({ session: session.name, window: 0 });
  } else {
    // Multi-window: open window picker
    tmuxWindowPickerOpen.set(session.name);
  }
}
```

Update the onclick in the template (line 102) from `onclick={() => connect(session.name)}` to `onclick={() => connect(session)}`.

Add the import for `tmuxWindowPickerOpen`:

```javascript
import { tmuxSessionPickerOpen, tmuxTerminalTarget, tmuxWindowPickerOpen } from '$lib/stores/tmux.svelte.js';
```

- [ ] **Step 3: Update `TmuxTerminalModal.svelte` — handle `{session, window}` target, add `?window=N` to WS URL, skip resize, no fitAddon**

Replace the entire `<script>` block:

```svelte
<script>
  import { tmuxTerminalTarget } from '$lib/stores/tmux.svelte.js';
  import { X, AlertTriangle } from '@lucide/svelte';
  import { Terminal } from 'xterm';
  import { FitAddon } from 'xterm-addon-fit';
  import 'xterm/css/xterm.css';

  let terminalRef;

  let terminal = $state(null);
  let fitAddon = $state(null);
  let ws = $state(null);
  let status = $state('disconnected'); // 'connecting' | 'connected' | 'disconnected' | 'ended'
  let sessionName = $state('');
  let windowIndex = $state(null);
  let reconnectAttempt = $state(0);
  let reconnectTimer = $state(null);

  function computeBackoff() {
    const delay = Math.min(1000 * Math.pow(2, reconnectAttempt), 16000);
    reconnectAttempt++;
    return delay;
  }

  function disconnect() {
    if (ws) {
      ws.onclose = null;
      ws.close();
      ws = null;
    }
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    status = 'disconnected';
  }

  function closeTerminal() {
    disconnect();
    tmuxTerminalTarget.set(null);
    reconnectAttempt = 0;
    if (terminal) {
      terminal.dispose();
      terminal = null;
      fitAddon = null;
    }
  }

  function connect() {
    if (!sessionName) return;
    status = 'connecting';

    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    let url = `${proto}//${location.host}/ws/tmux/${encodeURIComponent(sessionName)}`;
    if (windowIndex !== null) {
      url += `?window=${windowIndex}`;
    }
    const socket = new WebSocket(url);

    socket.onopen = () => {
      status = 'connected';
      reconnectAttempt = 0;
      // Only send resize for session-level attach (windowIndex === null)
      if (windowIndex === null && terminal) {
        socket.send(JSON.stringify({
          type: 'resize',
          cols: terminal.cols,
          rows: terminal.rows,
        }));
      }
    };

    socket.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data);
        if (msg.type === 'data' && terminal) {
          terminal.write('\x1b[2J\x1b[H' + msg.content);
        } else if (msg.type === 'session_end') {
          status = 'ended';
          socket.close();
        } else if (msg.type === 'error') {
          console.error('[tmux] server error:', msg.error);
        }
      } catch (e) {
        console.error('[tmux] ws parse error:', e);
      }
    };

    socket.onclose = () => {
      if (status === 'ended') return;
      status = 'disconnected';
      const delay = computeBackoff();
      reconnectTimer = setTimeout(() => {
        const current = tmuxTerminalTarget.get();
        if (current && current.session === sessionName) {
          connect();
        }
      }, delay);
    };

    socket.onerror = () => {
      socket.close();
    };

    ws = socket;
  }

  $effect(() => {
    const target = $tmuxTerminalTarget;
    if (target) {
      sessionName = target.session;
      windowIndex = target.window !== undefined ? target.window : null;
      reconnectAttempt = 0;

      if (!terminal) {
        terminal = new Terminal({
          cursorBlink: true,
          fontSize: 13,
          fontFamily: '"JetBrains Mono", "Fira Code", "Cascadia Code", Menlo, monospace',
          theme: {
            background: '#1e1e2e',
            foreground: '#cdd6f4',
            cursor: '#f5e0dc',
            selectionBackground: '#585b7066',
            black: '#45475a',
            red: '#f38ba8',
            green: '#a6e3a1',
            yellow: '#f9e2af',
            blue: '#89b4fa',
            magenta: '#f5c2e7',
            cyan: '#94e2d5',
            white: '#bac2de',
            brightBlack: '#585b70',
            brightRed: '#f38ba8',
            brightGreen: '#a6e3a1',
            brightYellow: '#f9e2af',
            brightBlue: '#89b4fa',
            brightMagenta: '#f5c2e7',
            brightCyan: '#94e2d5',
            brightWhite: '#a6adc8',
          },
        });

        fitAddon = new FitAddon();
        terminal.loadAddon(fitAddon);
        terminal.open(terminalRef);

        terminal.onData((data) => {
          if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: 'data', content: data }));
          }
        });

        // Only attach resize listener for session-level attach
        if (windowIndex === null) {
          terminal.onResize(({ cols, rows }) => {
            if (ws && ws.readyState === WebSocket.OPEN) {
              ws.send(JSON.stringify({ type: 'resize', cols, rows }));
            }
          });

          requestAnimationFrame(() => {
            if (fitAddon) fitAddon.fit();
          });
        }
      }

      connect();
    } else {
      closeTerminal();
    }
  });

  // Only observe resize for session-level attach
  $effect(() => {
    if (windowIndex !== null) return; // skip for window-level attach
    if (!fitAddon || !terminal) return;
    const observer = new ResizeObserver(() => {
      fitAddon.fit();
    });
    if (terminalRef) observer.observe(terminalRef);
    return () => observer.disconnect();
  });
</script>
```

Update the header title (line 182) to show window index:

```svelte
<span class="text-sm font-semibold text-ctp-text font-mono">
  {sessionName}{windowIndex !== null ? ':' + windowIndex : ''}
</span>
```

Update the reconnect check in `onclose` handler (line 90) to compare by session name since target is now an object:

Already handled in the rewrite above — `current && current.session === sessionName`.

- [ ] **Step 4: Mount `TmuxWindowPicker` in `App.svelte`**

Add import after line 17:

```javascript
import TmuxWindowPicker from '$lib/components/TmuxWindowPicker.svelte';
```

Add after line 161 (after `TmuxTerminalModal`):

```svelte
<!-- tmux Window Picker -->
<TmuxWindowPicker />
```

- [ ] **Step 5: Verify frontend builds**

Run: `cd /Users/dt/code/agent-reader/frontend && npm run build`
Expected: No errors

- [ ] **Step 6: Verify full build**

Run: `cd /Users/dt/code/agent-reader && go build ./...`
Expected: No errors

- [ ] **Step 7: Run all tests**

Run: `cd /Users/dt/code/agent-reader && go test ./internal/tmux/... -v`
Expected: All tests pass

- [ ] **Step 8: Commit**

```bash
git add frontend/src/lib/components/TmuxWindowPicker.svelte frontend/src/lib/components/TmuxSessionPicker.svelte frontend/src/lib/components/TmuxTerminalModal.svelte frontend/src/App.svelte
git commit -m "feat(frontend): add TmuxWindowPicker and wire up window-level attach flow"
```

---

### Task 5: Smoke test end-to-end

**Files:** (no changes)

- [ ] **Step 1: Start the dev server**

```bash
cd /Users/dt/code/agent-reader && bin/server
```

- [ ] **Step 2: Verify flow manually**

1. Create multiple tmux windows: `tmux new -s test-session` then `C-b c` twice
2. Click Terminal icon in sidebar
3. Connect to `test-session`
4. Verify window picker modal appears with 3 windows
5. Click a window → terminal modal opens showing `test-session:N`
6. Verify typing in terminal sends keys to the correct window
7. Close terminal, reopen with a single-window session → should skip window picker and open terminal directly

- [ ] **Step 3: Clean up test tmux session**

```bash
tmux kill-session -t test-session
```
