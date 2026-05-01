// Package hub manages WebSocket client connections and broadcasts events.
package hub

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"agent-web/internal/watcher"

	"github.com/gorilla/websocket"
)

// Client represents a connected WebSocket client.
type Client struct {
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	subscribe map[string]bool // session IDs to filter by (empty = all)
	project   string          // project filter (empty = all)
}

// SubscribeCallback is called when a client subscribes to a session.
// The callback should replay existing session events to the client's send channel.
type SubscribeCallback func(sessionID string, client *Client)

// Hub manages all connected clients and distributes events.
type Hub struct {
	clients            map[*Client]bool
	mu                 sync.RWMutex
	register           chan *Client
	unregister         chan *Client
	broadcast          chan []byte
	subscribeCallback  SubscribeCallback
}

// New creates a new Hub.
func New() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte, 256),
	}
}

// SetSubscribeCallback sets a callback that is invoked when a client subscribes to a session.
func (h *Hub) SetSubscribeCallback(cb SubscribeCallback) {
	h.subscribeCallback = cb
}

// Run starts the hub's main loop. Blocks until the process is stopped.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("[hub] client connected (total: %d)", len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			log.Printf("[hub] client disconnected (total: %d)", len(h.clients))

		case msg := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				// Check if client wants this event
				if !client.wantsMessage(msg) {
					continue
				}
				select {
				case client.send <- msg:
				default:
					// Slow client, skip
				}
			}
			h.mu.RUnlock()
		}
	}
}

// SubscribeWatcher reads events from the watcher and broadcasts them.
func (h *Hub) SubscribeWatcher(w *watcher.Watcher) {
	for ev := range w.Events() {
		msg := WSMessage{
			Type:      "event",
			SessionID: ev.SessionID,
			Project:   ev.Project,
			Data:      ev.Data,
			Time:      ev.Timestamp,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			log.Printf("[hub] marshal error: %v", err)
			continue
		}
		h.broadcast <- data
	}
}

// WSMessage is the JSON structure sent to WebSocket clients.
type WSMessage struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Project   string          `json:"project,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Time      time.Time       `json:"time"`
	Error     string          `json:"error,omitempty"`
}

// ClientMessage is received from WebSocket clients.
type ClientMessage struct {
	Type      string `json:"type"`       // "subscribe" | "unsubscribe" | "ping"
	SessionID string `json:"session_id"` // optional filter
	Project   string `json:"project"`    // optional filter
}

// Register adds a client to the hub.
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

// NewClient creates a new Client.
func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		hub:       hub,
		conn:      conn,
		send:      make(chan []byte, 256),
		subscribe: make(map[string]bool),
	}
}

// Send returns the client's send channel for writing messages.
func (c *Client) Send() chan []byte {
	return c.send
}

// wantsMessage checks if this client should receive the given broadcast message.
func (c *Client) wantsMessage(msg []byte) bool {
	// If no subscription filter, receive everything
	if len(c.subscribe) == 0 && c.project == "" {
		return true
	}

	// Parse just the fields we need to filter
	var check struct {
		SessionID string `json:"session_id"`
		Project   string `json:"project"`
	}
	if err := json.Unmarshal(msg, &check); err != nil {
		return true // on parse error, send it
	}

	// Check session filter
	if len(c.subscribe) > 0 && !c.subscribe[check.SessionID] {
		return false
	}

	// Check project filter
	if c.project != "" && check.Project != c.project {
		return false
	}

	return true
}

// readPump reads messages from the WebSocket connection.
func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister(c)
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[hub] read error: %v", err)
			}
			break
		}

		var msg ClientMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("[hub] invalid message: %v", err)
			continue
		}

		c.handleMessage(msg)
	}
}

func (c *Client) handleMessage(msg ClientMessage) {
	switch msg.Type {
	case "ping":
		resp := WSMessage{Type: "pong", Time: time.Now()}
		data, _ := json.Marshal(resp)
		c.send <- data

	case "subscribe":
		if msg.SessionID != "" {
			c.subscribe[msg.SessionID] = true
		}
		c.project = msg.Project
		log.Printf("[hub] client subscribed: session=%s project=%s", msg.SessionID, msg.Project)

		// Replay existing session events
		if msg.SessionID != "" && c.hub.subscribeCallback != nil {
			go c.hub.subscribeCallback(msg.SessionID, c)
		}

	case "unsubscribe":
		if msg.SessionID != "" {
			delete(c.subscribe, msg.SessionID)
		}
		if msg.Project != "" && c.project == msg.Project {
			c.project = ""
		}
	}
}

// writePump writes messages to the WebSocket connection.
func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)
			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ServeHandle handles a new WebSocket connection.
func (c *Client) Serve() {
	c.hub.Register(c)
	go c.writePump()
	c.readPump()
}
