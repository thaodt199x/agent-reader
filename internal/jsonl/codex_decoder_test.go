package jsonl

import (
	"encoding/json"
	"io"
	"os"
	"testing"
)

func TestParseCodexSessionMeta_UserFacing(t *testing.T) {
	line := `{"timestamp":"2026-05-19T02:39:55.659Z","type":"session_meta","payload":{"id":"019e3e1a-5f70-7511-84e4-fb07e05f6234","timestamp":"2026-05-19T02:39:36.304Z","cwd":"/Users/dt/code/dotfiles","source":"cli","thread_source":"user","model_provider":"openai"}}`

	meta, ok := ParseCodexSessionMeta([]byte(line))
	if !ok {
		t.Fatal("expected session_meta to parse")
	}
	if meta.ID != "019e3e1a-5f70-7511-84e4-fb07e05f6234" {
		t.Fatalf("unexpected id: %q", meta.ID)
	}
	if meta.CWD != "/Users/dt/code/dotfiles" {
		t.Fatalf("unexpected cwd: %q", meta.CWD)
	}
	if !IsCodexUserSession(meta) {
		t.Fatal("expected user-facing Codex session")
	}
}

func TestParseCodexSessionMeta_DropsSubagent(t *testing.T) {
	line := `{"timestamp":"2026-05-19T02:42:17.656Z","type":"session_meta","payload":{"id":"019e3e1c-d053-7d50-b72b-a85cbf675322","cwd":"/Users/dt/code/dotfiles","source":{"subagent":{"other":"guardian"}},"thread_source":"subagent","model_provider":"openai"}}`

	meta, ok := ParseCodexSessionMeta([]byte(line))
	if !ok {
		t.Fatal("expected session_meta to parse")
	}
	if IsCodexUserSession(meta) {
		t.Fatal("expected subagent session to be excluded")
	}
}

func TestParseCodexSessionMeta_DropsMissingID(t *testing.T) {
	line := `{"timestamp":"2026-05-19T02:39:55.659Z","type":"session_meta","payload":{"cwd":"/Users/dt/code/dotfiles","thread_source":"user"}}`

	meta, ok := ParseCodexSessionMeta([]byte(line))
	if !ok {
		t.Fatal("expected session_meta to parse")
	}
	if IsCodexUserSession(meta) {
		t.Fatal("expected session with no id to be excluded")
	}
}

func TestCodexDecoderNormalizeMessage_User(t *testing.T) {
	line := `{"timestamp":"2026-05-19T02:39:56.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"update tmux and kitty"}]}}`

	out, drop := normalizeCodexLine(line)
	if drop {
		t.Fatal("expected user message")
	}
	if out.Type != "message" {
		t.Fatalf("expected message event, got %q", out.Type)
	}
	var msg MessageEvent
	if err := json.Unmarshal(out.Raw, &msg); err != nil {
		t.Fatalf("invalid message JSON: %v", err)
	}
	if msg.Message.Role != "user" {
		t.Fatalf("expected user role, got %q", msg.Message.Role)
	}
	if len(msg.Message.Content) != 1 || msg.Message.Content[0].Text != "update tmux and kitty" {
		t.Fatalf("unexpected content: %#v", msg.Message.Content)
	}
}

func TestCodexDecoderNormalizeMessage_Assistant(t *testing.T) {
	line := `{"timestamp":"2026-05-19T02:41:59.106Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The edits are in place."}]}}`

	out, drop := normalizeCodexLine(line)
	if drop {
		t.Fatal("expected assistant message")
	}
	var msg MessageEvent
	if err := json.Unmarshal(out.Raw, &msg); err != nil {
		t.Fatalf("invalid message JSON: %v", err)
	}
	if msg.Message.Role != "assistant" {
		t.Fatalf("expected assistant role, got %q", msg.Message.Role)
	}
	if msg.Message.Content[0].Text != "The edits are in place." {
		t.Fatalf("unexpected text: %q", msg.Message.Content[0].Text)
	}
}

func TestCodexDecoderNormalizeMessageIDsDifferForSameTimestampAndRole(t *testing.T) {
	line1 := `{"timestamp":"2026-05-19T02:39:56.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first message"}]}}`
	line2 := `{"timestamp":"2026-05-19T02:39:56.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second message"}]}}`

	ev1, drop := normalizeCodexLine(line1)
	if drop {
		t.Fatal("expected first user message")
	}
	ev2, drop := normalizeCodexLine(line2)
	if drop {
		t.Fatal("expected second user message")
	}
	if ev1.ID == ev2.ID {
		t.Fatalf("expected distinct event IDs, got %q", ev1.ID)
	}
}

func TestCodexDecoderNormalizeFunctionCall(t *testing.T) {
	line := `{"timestamp":"2026-05-19T02:42:10.685Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"git status --short\",\"workdir\":\"/Users/dt/code/dotfiles\"}","call_id":"call_j9lbx16QVDJBAHnTbcD2DPlZ"}}`

	out, drop := normalizeCodexLine(line)
	if drop {
		t.Fatal("expected tool call message")
	}
	var msg MessageEvent
	if err := json.Unmarshal(out.Raw, &msg); err != nil {
		t.Fatalf("invalid message JSON: %v", err)
	}
	if msg.Message.Role != "assistant" {
		t.Fatalf("expected assistant role, got %q", msg.Message.Role)
	}
	block := msg.Message.Content[0]
	if block.Type != "toolCall" || block.ToolCallName != "exec_command" || block.ToolCallID != "call_j9lbx16QVDJBAHnTbcD2DPlZ" {
		t.Fatalf("unexpected tool call block: %#v", block)
	}
}

func TestCodexDecoderNormalizeFunctionCallOutput(t *testing.T) {
	line := `{"timestamp":"2026-05-19T02:42:05.990Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_j9lbx16QVDJBAHnTbcD2DPlZ","output":"Chunk ID: 65fc69\nWall time: 0.0000 seconds\nProcess exited with code 0\nOutput:\n M kitty/current-theme.conf\n"}}`

	out, drop := normalizeCodexLine(line)
	if drop {
		t.Fatal("expected tool result message")
	}
	var msg MessageEvent
	if err := json.Unmarshal(out.Raw, &msg); err != nil {
		t.Fatalf("invalid message JSON: %v", err)
	}
	if msg.Message.Role != "toolResult" {
		t.Fatalf("expected toolResult role, got %q", msg.Message.Role)
	}
	if msg.Message.ToolCallID != "call_j9lbx16QVDJBAHnTbcD2DPlZ" {
		t.Fatalf("unexpected toolCallId: %q", msg.Message.ToolCallID)
	}
	if len(msg.Message.Content) != 1 || msg.Message.Content[0].Type != "text" {
		t.Fatalf("unexpected content: %#v", msg.Message.Content)
	}
}

func TestCodexDecoderDropsBookkeeping(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-05-19T02:39:55.659Z","type":"session_meta","payload":{"id":"s","thread_source":"user"}}`,
		`{"timestamp":"2026-05-19T02:39:55.660Z","type":"turn_context","payload":{"model":"gpt-5.5"}}`,
		`{"timestamp":"2026-05-19T02:39:55.661Z","type":"event_msg","payload":{"type":"token_count"}}`,
		`{"timestamp":"2026-05-19T02:39:55.662Z","type":"event_msg","payload":{"type":"agent_message","message":"duplicate visible text"}}`,
		`{"timestamp":"2026-05-19T02:39:55.663Z","type":"response_item","payload":{"type":"reasoning","summary":[]}}`,
	}
	for _, line := range lines {
		if _, drop := normalizeCodexLine(line); !drop {
			t.Fatalf("expected to drop %s", line)
		}
	}
}

func TestCodexDecoderNext(t *testing.T) {
	content := `{"timestamp":"2026-05-19T02:39:55.659Z","type":"session_meta","payload":{"id":"s","thread_source":"user"}}` + "\n" +
		`{"timestamp":"2026-05-19T02:39:56.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n"
	path := t.TempDir() + "/rollout-test.jsonl"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	dec, err := NewCodexDecoder(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	ev, err := dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev != nil {
		t.Fatalf("first bookkeeping line should be dropped, got %#v", ev)
	}
	ev, err = dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "message" {
		t.Fatalf("expected message, got %q", ev.Type)
	}
}

func TestCodexDecoderNextMessageIDsDifferForIdenticalMessagesAtDifferentOffsets(t *testing.T) {
	line := `{"timestamp":"2026-05-19T02:39:56.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"same message"}]}}`
	content := line + "\n" + line + "\n"
	path := t.TempDir() + "/rollout-test.jsonl"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	dec, err := NewCodexDecoder(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	ev1, err := dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	ev2, err := dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev1.ID == ev2.ID {
		t.Fatalf("expected distinct event IDs, got %q", ev1.ID)
	}
}

func TestCodexDecoderRetriesPartialLineAtEOF(t *testing.T) {
	partial := `{"timestamp":"2026-05-19T02:39:56.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello`
	remainder := `"}]}}`
	path := t.TempDir() + "/partial-test.jsonl"
	if err := os.WriteFile(path, []byte(partial), 0644); err != nil {
		t.Fatal(err)
	}

	dec, err := NewCodexDecoder(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	ev, err := dec.Next()
	if err != io.EOF {
		t.Fatalf("expected EOF for partial line, got event=%#v err=%v", ev, err)
	}
	if dec.Offset() != 0 {
		t.Fatalf("partial line advanced offset to %d", dec.Offset())
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(remainder + "\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ev, err = dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev == nil {
		t.Fatal("expected completed line event")
	}
	var msg MessageEvent
	if err := json.Unmarshal(ev.Raw, &msg); err != nil {
		t.Fatal(err)
	}
	if got := msg.Message.Content[0].Text; got != "hello" {
		t.Fatalf("unexpected text: %q", got)
	}
}
