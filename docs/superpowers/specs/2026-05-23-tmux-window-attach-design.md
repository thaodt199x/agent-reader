# Window-Level Tmux Attach Design

## Overview

Extend the existing tmux session connect feature to allow users to attach to a specific window within a tmux session, rather than always attaching to the active window.

## User Flow

```
User clicks Terminal icon in sidebar
  → Session Picker modal opens (existing)
    → User clicks "Connect" on a session
      → If session has 1 window → open Terminal Modal directly (attach to window :0)
      → If session has 2+ windows → open Window Picker modal (new)
        → User clicks a window → open Terminal Modal (attach to that window)
```

## Backend Changes

### `internal/tmux/tmux.go`

**`Window` struct:**
```go
type Window struct {
    Index  int
    Name   string  // may be empty
    Active bool
    Panes  int
}
```

**`ListWindows(sessionName string) ([]Window, error)`** — runs:
```
tmux list-windows -t <session> -F "#{window_index}|#{window_name}|#{window_active}|#{window_panes}"
```

**`SessionAttach` struct changes:**
- Add `windowIndex *int` field (nil = use session's active window, explicit int = target that specific window index)
- When `windowIndex` is set, all tmux commands target `-t <session>:<windowIndex>` instead of `-t <session>`:
  - `capturePane()` → `tmux capture-pane -p -e -t <session>:<windowIndex>`
  - `SendKeys()` → `tmux send-keys -t <session>:<windowIndex> -l -- <text>`
  - `SendKey()` → `tmux send-keys -t <session>:<windowIndex> <key>`
- `Resize()` is **not** called for window-attached sessions. The terminal modal preserves the tmux pane's native dimensions and scrolls if content exceeds the viewport. Client-side `resize` events from xterm.js are ignored (no `tmux resize-pane` calls).

### `internal/server/server.go`

**New REST route:** `GET /api/tmux/sessions/:session/windows`
- Handler: `handleTmuxWindows()` calls `tmux.ListWindows()`, returns JSON array

**WebSocket route:** Keep existing `/ws/tmux/:session` but add optional query parameter `?window=N`
- When `window` query param is present, create attacher with that window index
- `tmuxAttachers` map key changes from `sessionName` to `sessionName:windowIndex` (e.g., `"myapp:1"`)
- During server `Stop()`, all attachers (per-window) are cleaned up as before

## Frontend Changes

### New component: `TmuxWindowPicker.svelte`
- Modal listing windows as clickable cards
- Each card shows: window index, name (or `"window <N>"` if empty), pane count, active indicator
- "Back" button returns to session picker
- Clicking a window sets `tmuxTerminalTarget` with `{ session, window }` and opens terminal modal

### Store updates (`tmux.svelte.js`)
- `tmuxTerminalTarget`: change from `string | null` to `{ session: string, window: number } | null`
- New store: `tmuxWindowPickerOpen` (boolean)

### API update (`tmux.js`)
- New function: `fetchTmuxWindows(sessionName) → GET /api/tmux/sessions/:session/windows`

### `TmuxTerminalModal.svelte` updates
- Accepts window prop from `tmuxTerminalTarget`
- Opens WS to `/ws/tmux/:session?window=N` (query param, no route change)
- Modal title shows `session:window` for multi-window, `session` for single-window
- If no window specified, connects to the session (existing behavior)
- Does **not** send `resize` events over the WebSocket. The modal preserves the tmux pane's native size and scrolls if the viewport is smaller than the terminal content. xterm.js `resize` event listener is not attached.

### `App.svelte` updates
- Mount `TmuxWindowPicker` alongside existing components

## Error Handling
- If a window no longer exists (user deleted it), the WS connection returns an error message and the terminal modal shows an error state
- `ListWindows` returning empty array → fall back to connecting to the session (active window)
- If tmux is not available, both pickers show the "tmux not found" state (existing behavior)

## Scope
- **In scope:** listing windows, attaching to a specific window via the picker
- **Out of scope:** pane-level selection, switching windows within the terminal modal, renaming windows from the UI, creating new windows, resizing the tmux pane from the modal (modal scrolls instead)
