package jsonl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Decoder reads a single JSONL file line-by-line, tracking the current
// byte offset so it can resume from where it left off (tail-like behaviour).
type Decoder struct {
	path   string
	offset int64 // bytes read so far
	file   *os.File
	reader *bufio.Reader
}

// NewDecoder opens path for reading. If offset > 0, seeks to that position.
func NewDecoder(path string, offset int64) (*Decoder, error) {
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
	return &Decoder{
		path:   path,
		offset: offset,
		file:   f,
		reader: bufio.NewReader(f),
	}, nil
}

// Offset returns the current byte offset (how many bytes have been read).
func (d *Decoder) Offset() int64 { return d.offset }

// Path returns the file path.
func (d *Decoder) Path() string { return d.path }

// Next reads the next JSONL line and returns a typed Event.
// Returns (nil, io.EOF) when no more data is available.
func (d *Decoder) Next() (*Event, error) {
	line, err := d.reader.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("read %s: %w", d.path, err)
	}

	d.offset += int64(len(line))

	line = trimLine(line)
	if line == "" {
		return nil, nil // skip blank lines
	}

	return parseEvent(line)
}

// Close releases the file handle.
func (d *Decoder) Close() error {
	return d.file.Close()
}

// parseEvent decodes a single JSONL line into a typed Event.
func parseEvent(line string) (*Event, error) {
	var wrapper struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(line), &wrapper); err != nil {
		return nil, fmt.Errorf("decode type: %w", err)
	}

	ev := &Event{Type: wrapper.Type}

	// Unmarshal common fields first
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, fmt.Errorf("decode raw: %w", err)
	}

	if v, ok := raw["id"]; ok {
		json.Unmarshal(v, &ev.ID)
	}
	if v, ok := raw["parentId"]; ok {
		if string(v) != "null" {
			var s string
			json.Unmarshal(v, &s)
			ev.ParentID = &s
		}
	}
	if v, ok := raw["timestamp"]; ok {
		json.Unmarshal(v, &ev.Timestamp)
	}

	ev.Raw = []byte(line)
	return ev, nil
}

// DecodeSession decodes raw JSON into a SessionEvent.
func DecodeSession(raw json.RawMessage) (*SessionEvent, error) {
	var ev SessionEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// DecodeModelChange decodes raw JSON into a ModelChangeEvent.
func DecodeModelChange(raw json.RawMessage) (*ModelChangeEvent, error) {
	var ev ModelChangeEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// DecodeThinkingLevelChange decodes raw JSON into a ThinkingLevelChangeEvent.
func DecodeThinkingLevelChange(raw json.RawMessage) (*ThinkingLevelChangeEvent, error) {
	var ev ThinkingLevelChangeEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// DecodeMessage decodes raw JSON into a MessageEvent.
func DecodeMessage(raw json.RawMessage) (*MessageEvent, error) {
	var ev MessageEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// trimLine removes trailing whitespace / newline characters.
func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\r' || s[len(s)-1] == '\n' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
