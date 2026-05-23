package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetFirstUserMessageCodexSkipsEnvironmentContext(t *testing.T) {
	path := writeTempSession(t,
		`{"timestamp":"2026-05-19T02:39:55.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/tmp/project</cwd>\n</environment_context>"}]}}`,
		`{"timestamp":"2026-05-19T02:39:56.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"real user prompt"}]}}`,
	)

	got := getFirstUserMessage(path, "codex")
	if got != "real user prompt" {
		t.Fatalf("expected real user prompt, got %q", got)
	}
}

func TestReadCodexSessionInfoToleratesLongLineBeforeMeta(t *testing.T) {
	path := writeTempSession(t,
		longCodexUserMessage("ignored", 40*1024),
		`{"timestamp":"2026-05-19T02:39:55.659Z","type":"session_meta","payload":{"id":"codex-session-1","cwd":"/tmp/codex-project","thread_source":"user","model":"gpt-5"}}`,
	)

	meta, ok := readCodexSessionInfo(path)
	if !ok {
		t.Fatal("expected session metadata")
	}
	if meta.ID != "codex-session-1" {
		t.Fatalf("unexpected session id: %q", meta.ID)
	}
}

func TestAggregateCodexSessionDataToleratesLongLineBeforeModel(t *testing.T) {
	path := writeTempSession(t,
		`{"timestamp":"2026-05-19T02:39:55.659Z","type":"session_meta","payload":{"id":"codex-session-2","cwd":"/tmp/codex-project","thread_source":"user"}}`,
		longCodexUserMessage("ignored", 40*1024),
		`{"timestamp":"2026-05-19T02:39:56.659Z","type":"turn_context","payload":{"model":"gpt-5"}}`,
	)

	lineCount, cwd, model, _, _, _, _, _ := aggregateSessionData(path, "codex")
	if lineCount != 3 {
		t.Fatalf("expected 3 lines counted, got %d", lineCount)
	}
	if cwd != "/tmp/codex-project" {
		t.Fatalf("unexpected cwd: %q", cwd)
	}
	if model != "gpt-5" {
		t.Fatalf("unexpected model: %q", model)
	}
}

func TestListSessionsCodexDerivesProjectFromCWD(t *testing.T) {
	root := t.TempDir()
	piDir := filepath.Join(root, "pi")
	codexDir := filepath.Join(root, "codex")
	if err := os.MkdirAll(piDir, 0755); err != nil {
		t.Fatalf("create pi dir: %v", err)
	}
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatalf("create codex dir: %v", err)
	}
	path := filepath.Join(codexDir, "session.jsonl")
	content := `{"timestamp":"2026-05-19T02:39:55.659Z","type":"session_meta","payload":{"id":"codex-session-3","cwd":"/tmp/final-codex-project","thread_source":"user","model":"gpt-5"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write codex session: %v", err)
	}

	s := &Server{sessionsDir: piDir, codexSessionsDir: codexDir}
	sessions := s.listSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Project != "final-codex-project" {
		t.Fatalf("unexpected project: %q", sessions[0].Project)
	}
}

func writeTempSession(t *testing.T, lines ...string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp session: %v", err)
	}
	return path
}

func longCodexUserMessage(prefix string, n int) string {
	return `{"timestamp":"2026-05-19T02:39:56.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + prefix + strings.Repeat("x", n) + `"}]}}`
}

func TestPathMatches(t *testing.T) {
	tests := []struct {
		projectDir string
		path       string
		expected   bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/", "/a/b", true},
		{"/a/b", "/a/b/", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b/c", "/a/b", true},
		{"/a/b", "/a/bc", false},
		{"/a/bc", "/a/b", false},
		{"", "/a/b", false},
		{"/a/b", "", false},
	}
	for _, tt := range tests {
		got := pathMatches(tt.projectDir, tt.path)
		if got != tt.expected {
			t.Errorf("pathMatches(%q, %q) = %v; want %v", tt.projectDir, tt.path, got, tt.expected)
		}
	}
}
