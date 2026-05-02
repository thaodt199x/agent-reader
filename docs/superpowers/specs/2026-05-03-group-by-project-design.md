# Session List — Group By Project Toggle

## Overview

Add a toggle in the sidebar to switch between flat session list and sessions grouped by project. No backend changes required — the `/api/sessions` endpoint already returns `project` on each session.

## Current State

- `GET /api/sessions` returns a flat array of `SessionInfo` objects, each with a `project` field
- `Sidebar.svelte` renders sessions as a flat list
- Sessions are already organized on disk by project directory

## Design

### 1. State

Add `groupByProject` writable store to `$lib/stores/ui.svelte.js`:

```js
export const groupByProject = writable(false);
```

Persist to `localStorage` (same pattern as `activeSession`) so the user's preference survives page reloads.

### 2. Toggle Button

Place a toggle button in the sidebar header, between the "⚡ Sessions" title and the `＋` button. Uses a simple icon (e.g., `📁` for grouped, `≡` for flat).

### 3. Grouped Rendering

When `groupByProject` is `true`:
- Sessions are grouped by the `project` field
- Each group has a collapsible header showing project name and session count (e.g., `agent-web (3)`)
- All groups expanded by default; collapse state is ephemeral (not persisted)
- The group containing the active session auto-expands
- Groups sorted alphabetically by project name; sessions within each group sorted by timestamp (newest first, matching the existing flat list sort)

When `groupByProject` is `false`:
- Existing flat list behavior (no change)

### 4. Component Changes

- `Sidebar.svelte` — add toggle button and conditional rendering logic
- No new components; grouped rendering uses inline `{#each}` over grouped data

## Data Flow

```
/api/sessions (flat list)
  → fetchSessions() → sessions store
  → Sidebar reads $sessions + $groupByProject
  → if grouped: group by project, render collapsible groups
  → if flat: render as before
```

## Testing

- Toggle button switches between modes
- Grouped mode shows correct project headers with counts
- Collapsible groups expand/collapse correctly
- Active session's group auto-expands
- Flat mode unchanged
- Toggle preference persists across page reloads (localStorage)
