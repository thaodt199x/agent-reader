package main

import (
	"bufio"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"agent-web/internal/server"
)

// loadEnv reads key=value pairs from .env file (simple format, no quotes needed).
func loadEnv(path string) map[string]string {
	result := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return result // silently ignore missing .env
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) == 2 {
			result[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return result
}

func main() {
	addr := flag.String("addr", ":8081", "HTTP listen address")
	sessionsDir := flag.String("sessions", "", "Path to .pi/agent/sessions directory")
	claudeProjectsDir := flag.String("claude-projects", "", "Path to ~/.claude/projects directory")
	codexSessionsDir := flag.String("codex-sessions", "", "Path to ~/.codex/sessions directory")
	allowedRoots := flag.String("roots", "", "Comma-separated allowed root folders for filesystem API")
	flag.Parse()

	// Load .env file (looks in current dir)
	env := loadEnv(".env")

	// Use .env values as fallback for flags
	if *allowedRoots == "" {
		*allowedRoots = env["ALLOWED_ROOT_FOLDERS"]
	}

	// Set LM Studio env vars from .env (used by llm package)
	if env["LMSTUDIO_URL"] != "" {
		os.Setenv("LMSTUDIO_URL", env["LMSTUDIO_URL"])
	}
	if env["LMSTUDIO_MODEL"] != "" {
		os.Setenv("LMSTUDIO_MODEL", env["LMSTUDIO_MODEL"])
	}

	// Default sessions directory
	if *sessionsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("cannot determine home directory: %v", err)
		}
		*sessionsDir = filepath.Join(home, ".pi", "agent", "sessions")
	}

	// Default Claude projects directory
	if *claudeProjectsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("cannot determine home directory: %v", err)
		}
		*claudeProjectsDir = filepath.Join(home, ".claude", "projects")
	}

	// Default Codex sessions directory
	if *codexSessionsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("cannot determine home directory: %v", err)
		}
		*codexSessionsDir = filepath.Join(home, ".codex", "sessions")
	}

	// Verify pi sessions directory exists
	info, err := os.Stat(*sessionsDir)
	if err != nil {
		log.Fatalf("sessions directory %s: %v", *sessionsDir, err)
	}
	if !info.IsDir() {
		log.Fatalf("sessions path is not a directory: %s", *sessionsDir)
	}

	srv, err := server.New(*sessionsDir, *claudeProjectsDir, *codexSessionsDir, *allowedRoots)
	if err != nil {
		log.Fatalf("create server: %v", err)
	}

	if *allowedRoots != "" {
		log.Printf("[main] Allowed filesystem roots: %s", *allowedRoots)
	} else {
		log.Printf("[main] No allowed filesystem roots configured (fsbrowse API disabled)")
	}

	// Graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sig
		log.Println("shutting down...")
		srv.Stop()
		os.Exit(0)
	}()

	log.Fatal(srv.Start(*addr))
}
