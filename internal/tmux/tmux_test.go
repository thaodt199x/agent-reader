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

	input := `my-project|3|1|1779272400|0|/Users/dt/code/my-project
agent-work|1|2|1779266400|1|/Users/dt/code/agent-work
`

	var sessions []Session
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 6)
		if len(parts) != 6 {
			t.Fatalf("expected 6 parts, got %d for line: %s", len(parts), line)
		}

		createdUnix, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			t.Fatalf("failed to parse created time: %v", err)
		}
		created := time.Unix(createdUnix, 0)

		windows, _ := strconv.Atoi(parts[1])
		panes, _ := strconv.Atoi(parts[2])

		sessions = append(sessions, Session{
			Name:     parts[0],
			Windows:  windows,
			Panes:    panes,
			Created:  created,
			Attached: parts[4] == "1",
			Path:     parts[5],
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
	if sessions[0].Path != "/Users/dt/code/my-project" {
		t.Errorf("expected session 0 path '/Users/dt/code/my-project', got %q", sessions[0].Path)
	}

	expected := time.Unix(1779272400, 0)
	if !sessions[0].Created.Equal(expected) {
		t.Errorf("expected session 0 created %v, got %v", expected, sessions[0].Created)
	}

	if sessions[1].Name != "agent-work" {
		t.Errorf("expected session 1 name 'agent-work', got %q", sessions[1].Name)
	}
	if !sessions[1].Attached {
		t.Error("expected session 1 attached to be true")
	}
	if sessions[1].Path != "/Users/dt/code/agent-work" {
		t.Errorf("expected session 1 path '/Users/dt/code/agent-work', got %q", sessions[1].Path)
	}
}

func TestStopWithoutStart(t *testing.T) {
	t.Parallel()
	a := NewAttach("nonexistent-session", -1)
	// Should not deadlock
	a.Stop()
}

func TestDoubleStop(t *testing.T) {
	t.Parallel()
	a := NewAttach("nonexistent-session", -1)
	go a.Start()
	// Give Start a moment to begin
	time.Sleep(50 * time.Millisecond)
	// Should not panic on double close
	a.Stop()
	a.Stop()
}

func TestStopWithoutStart_NonEmptyPanic(t *testing.T) {
	t.Parallel()
	a := NewAttach("nonexistent-session", -1)
	// Stop twice without ever starting — must not panic
	a.Stop()
	a.Stop()
}

func TestListWindows_Parsing(t *testing.T) {
	t.Parallel()

	input := `0|bash|1|1|/Users/dt/code/my-project
1|nvim|0|1|/Users/dt/code/my-project/src
2||0|2|/Users/dt/code/my-project/tests
`

	var windows []Window
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

		index, _ := strconv.Atoi(parts[0])
		panes, _ := strconv.Atoi(parts[3])

		windows = append(windows, Window{
			Index:  index,
			Name:   parts[1],
			Active: parts[2] == "1",
			Panes:  panes,
			Path:   parts[4],
		})
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(windows) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(windows))
	}

	if windows[0].Index != 0 || windows[0].Name != "bash" || !windows[0].Active || windows[0].Path != "/Users/dt/code/my-project" {
		t.Errorf("window 0: %+v", windows[0])
	}
	if windows[1].Index != 1 || windows[1].Name != "nvim" || windows[1].Active || windows[1].Path != "/Users/dt/code/my-project/src" {
		t.Errorf("window 1: %+v", windows[1])
	}
	if windows[2].Index != 2 || windows[2].Name != "" || windows[2].Active || windows[2].Panes != 2 || windows[2].Path != "/Users/dt/code/my-project/tests" {
		t.Errorf("window 2: %+v", windows[2])
	}
}
