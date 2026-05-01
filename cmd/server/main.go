package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"agent-web/internal/server"
)

func main() {
	addr := flag.String("addr", ":8081", "HTTP listen address")
	sessionsDir := flag.String("sessions", "", "Path to .pi/agent/sessions directory")
	flag.Parse()

	// Default sessions directory
	if *sessionsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("cannot determine home directory: %v", err)
		}
		*sessionsDir = filepath.Join(home, ".pi", "agent", "sessions")
	}

	// Verify directory exists
	info, err := os.Stat(*sessionsDir)
	if err != nil {
		log.Fatalf("sessions directory %s: %v", *sessionsDir, err)
	}
	if !info.IsDir() {
		log.Fatalf("sessions path is not a directory: %s", *sessionsDir)
	}

	srv, err := server.New(*sessionsDir)
	if err != nil {
		log.Fatalf("create server: %v", err)
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
