# Agent Web — Go Project Plan

## Goal
Watch `.pi/agent/sessions/` for JSONL file changes and stream events in real-time to browser clients via WebSocket.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Browser Client                       │
│  (React/Vite — dashboard showing sessions & messages)   │
└──────────────────────┬──────────────────────────────────┘
                       │ WebSocket (ws://localhost:8080/ws)
                       ▼
┌─────────────────────────────────────────────────────────┐
│                    Go Server                             │
│                                                          │
│  ┌──────────────┐    ┌──────────────┐    ┌────────────┐ │
│  │  File Watcher│───►│  JSONL Parser│───►│  WS Hub    │ │
│  │  (fsnotify)  │    │  (decoder)   │    │  (broadcast│ │
│  │              │    │              │    │   clients) │ │
│  └──────────────┘    └──────────────┘    └────────────┘ │
│         │                                         ▲     │
│         ▼                                         │     │
│  ~/.pi/agent/sessions/                            │     │
│  └─ <project>/                                    │     │
│     └─ *.jsonl ───────────────────────────────────┘     │
└─────────────────────────────────────────────────────────┘
```

## Project Structure

```
agent-web/
├── cmd/
│   └── server/
│       └── main.go              # Entry point
├── internal/
│   ├── watcher/
│   │   └── watcher.go           # fsnotify file watching
│   ├── jsonl/
│   │   ├── types.go             # Go structs for JSONL events
│   │   └── decoder.go           # JSONL line-by-line decoder
│   ├── hub/
│   │   └── hub.go               # WebSocket hub (broadcast, subscribe)
│   └── server/
│       ├── server.go            # HTTP + WebSocket server
│       └── static.go            # Serve embedded static files
├── web/
│   └── static/
│       └── index.html           # Simple dashboard (initial)
├── go.mod
├── go.sum
└── PLAN.md
```

## Data Flow

1. **Watcher** scans `~/.pi/agent/sessions/` recursively
2. On new/modified `.jsonl` files → reads new lines (tracks offset per file)
3. **JSONL Decoder** parses each line into typed events (`SessionEvent`, `ModelChangeEvent`, `ThinkingLevelChangeEvent`, `MessageEvent`)
4. **Hub** broadcasts events to all connected WebSocket clients
5. Clients subscribe to:
   - All sessions (global stream)
   - Specific session by ID
   - Specific project/cwd

## WebSocket Protocol

### Server → Client (events)
```json
{"type":"event","session":"<id>","project":"<cwd>","data":{...jsonl-event...}}
{"type":"session_list","sessions":[{...}]}
```

### Client → Server
```json
{"type":"subscribe","session_id":"<optional>","project":"<optional>"}
{"type":"unsubscribe","session_id":"<optional>"}
{"type":"ping"}
```

## Dependencies

- `github.com/fsnotify/fsnotify` — file system notifications
- `github.com/gorilla/websocket` — WebSocket support
- Standard library: `encoding/json`, `net/http`, `os`, `path/filepath`

## Phases

### Phase 1: Core Go Server (this step)
- [x] JSONL type definitions
- [ ] JSONL decoder with offset tracking
- [ ] File watcher (fsnotify)
- [ ] WebSocket hub
- [ ] HTTP + WS server
- [ ] Basic main.go entry point

### Phase 2: Dashboard UI
- Simple HTML/JS dashboard
- Session list sidebar
- Real-time message stream view
- Filter by session/project

### Phase 3: Features
- Replay existing session history
- Search/filter messages
- Session metadata display
