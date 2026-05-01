// Package jsonl defines Go structs for the .pi/agent/sessions JSONL event format.
package jsonl

import "encoding/json"

// --- Top-level event wrapper ---

// Event is the top-level JSONL line. The Type field determines which
// concrete event struct to unmarshal into.
type Event struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Raw       json.RawMessage `json:"-"` // preserved raw JSON for the type-specific payload
}

// --- Session event ---

// SessionEvent is the first line in every session JSONL file.
type SessionEvent struct {
	Type      string `json:"type"`               // "session"
	Version   int    `json:"version"`
	ID        string `json:"id"`                 // UUID
	Timestamp string `json:"timestamp"`          // ISO 8601
	CWD       string `json:"cwd"`                // working directory
}

// --- Model change event ---

// ModelChangeEvent records a model/provider switch.
type ModelChangeEvent struct {
	Type      string  `json:"type"`      // "model_change"
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
	Provider  string  `json:"provider"`
	ModelID   string  `json:"modelId"`
}

// --- Thinking level change event ---

// ThinkingLevelChangeEvent records a thinking-level switch.
type ThinkingLevelChangeEvent struct {
	Type          string  `json:"type"`            // "thinking_level_change"
	ID            string  `json:"id"`
	ParentID      *string `json:"parentId"`
	Timestamp     string  `json:"timestamp"`
	ThinkingLevel string  `json:"thinkingLevel"`
}

// --- Message event ---

// MessageEvent wraps the "message" type. The inner Message struct's Role
// field determines the concrete shape (user / assistant / toolResult).
type MessageEvent struct {
	Type      string   `json:"type"`      // "message"
	ID        string   `json:"id"`
	ParentID  *string  `json:"parentId"`
	Timestamp string   `json:"timestamp"`
	Message   MsgBlock `json:"message"`
}

// MsgBlock is the "message" field inside a MessageEvent.
type MsgBlock struct {
	Role      string           `json:"role"`       // "user" | "assistant" | "toolResult"
	Content   []ContentBlock   `json:"content"`
	Timestamp *float64         `json:"timestamp,omitempty"` // epoch ms

	// Assistant-only fields
	API        string          `json:"api,omitempty"`
	Provider   string          `json:"provider,omitempty"`
	Model      string          `json:"model,omitempty"`
	Usage      *Usage          `json:"usage,omitempty"`
	StopReason string          `json:"stopReason,omitempty"`
	ResponseID string          `json:"responseId,omitempty"`

	// ToolResult-only fields
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
}

// ContentBlock is a single block inside a message's content array.
type ContentBlock struct {
	Type string `json:"type"` // "text" | "thinking" | "toolCall" | "image"

	// type: "text"
	Text string `json:"text,omitempty"`

	// type: "thinking"
	Thinking         string `json:"thinking,omitempty"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`

	// type: "toolCall"
	ToolCallID   string          `json:"id,omitempty"`
	ToolCallName string          `json:"name,omitempty"`
	Arguments    json.RawMessage `json:"arguments,omitempty"`

	// type: "image"
	Data string `json:"data,omitempty"`
	MIME string `json:"mimeType,omitempty"`
}

// Usage tracks token usage for assistant messages.
type Usage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	TotalTokens int64 `json:"totalTokens"`
	Cost       struct {
		Input     float64 `json:"input"`
		Output    float64 `json:"output"`
		CacheRead float64 `json:"cacheRead"`
		CacheWrite float64 `json:"cacheWrite"`
		Total     float64 `json:"total"`
	} `json:"cost"`
}
