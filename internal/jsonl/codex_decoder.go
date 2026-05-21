package jsonl

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func ParseCodexSessionMeta(line []byte) (CodexSessionMeta, bool) {
	var env CodexEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return CodexSessionMeta{}, false
	}
	if env.Type != "session_meta" {
		return CodexSessionMeta{}, false
	}
	var payload CodexSessionMeta
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return CodexSessionMeta{}, false
	}
	return payload, true
}

func IsCodexUserSession(meta CodexSessionMeta) bool {
	if meta.ID == "" {
		return false
	}
	if meta.ThreadSource == "subagent" {
		return false
	}
	if sourceNamesInternalCodexSession(meta.Source) {
		return false
	}
	model := strings.ToLower(meta.Model)
	if strings.Contains(model, "codex-auto-review") || strings.Contains(model, "guardian") {
		return false
	}
	if meta.ThreadSource == "user" {
		return true
	}
	return meta.ThreadSource == ""
}

func sourceNamesInternalCodexSession(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		v := strings.ToLower(s)
		return strings.Contains(v, "guardian") || strings.Contains(v, "auto-review") || strings.Contains(v, "approval")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	if _, ok := obj["subagent"]; ok {
		return true
	}
	for key := range obj {
		v := strings.ToLower(key)
		if strings.Contains(v, "guardian") || strings.Contains(v, "auto-review") || strings.Contains(v, "approval") {
			return true
		}
	}
	return false
}

type CodexDecoder struct {
	path   string
	offset int64
	file   *os.File
	reader *bufio.Reader
}

func NewCodexDecoder(path string, offset int64) (*CodexDecoder, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return nil, fmt.Errorf("seek %s: %w", path, err)
		}
	}
	return &CodexDecoder{
		path:   path,
		offset: offset,
		file:   f,
		reader: bufio.NewReader(f),
	}, nil
}

func (d *CodexDecoder) Offset() int64 { return d.offset }

func (d *CodexDecoder) Path() string { return d.path }

func (d *CodexDecoder) Close() error { return d.file.Close() }

func (d *CodexDecoder) Next() (*Event, error) {
	for {
		lineStartOffset := d.offset
		line, err := d.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if line == "" {
					return nil, io.EOF
				}
			} else {
				return nil, fmt.Errorf("read %s: %w", d.path, err)
			}
		}

		trimmed := strings.TrimSpace(line)
		if err == io.EOF && trimmed != "" && !json.Valid([]byte(trimmed)) {
			if _, seekErr := d.file.Seek(lineStartOffset, io.SeekStart); seekErr != nil {
				return nil, fmt.Errorf("seek %s: %w", d.path, seekErr)
			}
			d.reader.Reset(d.file)
			return nil, io.EOF
		}

		d.offset += int64(len(line))
		if trimmed == "" {
			if err == io.EOF {
				return nil, io.EOF
			}
			continue
		}

		ev, drop := normalizeCodexLineAtOffset(trimmed, lineStartOffset)
		if drop {
			return nil, nil
		}
		return ev, nil
	}
}

func normalizeCodexLine(line string) (*Event, bool) {
	return normalizeCodexLineAtOffset(line, 0)
}

func normalizeCodexLineAtOffset(line string, offset int64) (*Event, bool) {
	var env CodexEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return nil, true
	}
	if env.Type != "response_item" {
		return nil, true
	}

	var payload struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return nil, true
	}

	switch payload.Type {
	case "message":
		return normalizeCodexMessage(env.Timestamp, env.Payload, offset)
	case "function_call":
		return normalizeCodexFunctionCall(env.Timestamp, env.Payload)
	case "function_call_output":
		return normalizeCodexFunctionCallOutput(env.Timestamp, env.Payload)
	default:
		return nil, true
	}
}

func normalizeCodexMessage(timestamp string, raw json.RawMessage, offset int64) (*Event, bool) {
	var msg CodexMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, true
	}

	var blocks []ContentBlock
	for _, c := range msg.Content {
		switch c.Type {
		case "input_text", "output_text", "text":
			if c.Text != "" {
				blocks = append(blocks, ContentBlock{Type: "text", Text: c.Text})
			}
		}
	}
	if msg.Role == "" || len(blocks) == 0 {
		return nil, true
	}

	id := "codex-" + shortStableID(timestamp, msg.Role) + "-" + shortHash(raw) + "-o" + strconv.FormatInt(offset, 10)
	return marshalCodexEvent(timestamp, id, map[string]interface{}{
		"role":    msg.Role,
		"content": blocks,
	})
}

func normalizeCodexFunctionCall(timestamp string, raw json.RawMessage) (*Event, bool) {
	var call CodexFunctionCall
	if err := json.Unmarshal(raw, &call); err != nil {
		return nil, true
	}
	if call.CallID == "" || call.Name == "" {
		return nil, true
	}

	return marshalCodexEvent(timestamp, call.CallID, map[string]interface{}{
		"role": "assistant",
		"content": []ContentBlock{{
			Type:         "toolCall",
			ToolCallID:   call.CallID,
			ToolCallName: call.Name,
			Arguments:    parseCodexArguments(call.Arguments),
		}},
	})
}

func normalizeCodexFunctionCallOutput(timestamp string, raw json.RawMessage) (*Event, bool) {
	var out CodexFunctionCallOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, true
	}
	if out.CallID == "" {
		return nil, true
	}

	return marshalCodexEvent(timestamp, out.CallID+"-result", map[string]interface{}{
		"role":       "toolResult",
		"toolCallId": out.CallID,
		"content":    []ContentBlock{{Type: "text", Text: out.Output}},
		"isError":    false,
	})
}

func parseCodexArguments(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage(`{}`)
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err == nil {
		return raw
	}
	b, _ := json.Marshal(map[string]string{"value": s})
	return json.RawMessage(b)
}

func marshalCodexEvent(timestamp, id string, message map[string]interface{}) (*Event, bool) {
	out := map[string]interface{}{
		"type":      "message",
		"id":        id,
		"timestamp": timestamp,
		"message":   message,
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, true
	}
	return &Event{
		Type:      "message",
		ID:        id,
		Timestamp: timestamp,
		Raw:       b,
	}, false
}

func shortStableID(timestamp, role string) string {
	clean := strings.NewReplacer(":", "", "-", "", ".", "", "Z", "").Replace(timestamp)
	if clean == "" {
		clean = "unknown"
	}
	return role + "-" + clean
}

func shortHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}
