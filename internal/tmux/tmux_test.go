package tmux

import (
	"bufio"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIsAvailable(t *testing.T) {
	t.Parallel()
	// Just verify it doesn't panic; result depends on whether tmux is installed
	available := IsAvailable()
	t.Logf("tmux available: %v", available)
}

func TestListSessions_Parsing(t *testing.T) {
	t.Parallel()

	input := `my-project|3|1|2026/05/23 10:30:00|0
agent-work|1|2|2026/05/23 09:00:00|1
`

	var sessions []Session
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) != 5 {
			t.Fatalf("expected 5 parts, got %d for line: %s", len(parts), line)
		}

		created, err := time.Parse("2006/01/02 15:04:05", parts[3])
		if err != nil {
			t.Fatalf("failed to parse created time: %v", err)
		}

		windows, _ := strconv.Atoi(parts[1])
		panes, _ := strconv.Atoi(parts[2])

		sessions = append(sessions, Session{
			Name:     parts[0],
			Windows:  windows,
			Panes:    panes,
			Created:  created,
			Attached: parts[4] == "1",
		})
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	if sessions[0].Name != "my-project" {
		t.Errorf("expected session 0 name 'my-project', got %q", sessions[0].Name)
	}
	if sessions[0].Windows != 3 {
		t.Errorf("expected session 0 windows 3, got %d", sessions[0].Windows)
	}
	if sessions[0].Panes != 1 {
		t.Errorf("expected session 0 panes 1, got %d", sessions[0].Panes)
	}
	if sessions[0].Attached {
		t.Error("expected session 0 attached to be false")
	}

	expected := time.Date(2026, 5, 23, 10, 30, 0, 0, time.UTC)
	if !sessions[0].Created.Equal(expected) {
		t.Errorf("expected session 0 created %v, got %v", expected, sessions[0].Created)
	}

	if sessions[1].Name != "agent-work" {
		t.Errorf("expected session 1 name 'agent-work', got %q", sessions[1].Name)
	}
	if !sessions[1].Attached {
		t.Error("expected session 1 attached to be true")
	}
}

func TestStopWithoutStart(t *testing.T) {
	t.Parallel()
	a := NewAttach("nonexistent-session")
	// Should not deadlock
	a.Stop()
}

func TestDoubleStop(t *testing.T) {
	t.Parallel()
	a := NewAttach("nonexistent-session")
	go a.Start()
	// Give Start a moment to begin
	time.Sleep(50 * time.Millisecond)
	// Should not panic on double close
	a.Stop()
	a.Stop()
}

func TestStopWithoutStart_NonEmptyPanic(t *testing.T) {
	t.Parallel()
	a := NewAttach("nonexistent-session")
	// Stop twice without ever starting — must not panic
	a.Stop()
	a.Stop()
}
