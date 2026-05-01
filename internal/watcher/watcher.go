// Package watcher monitors ~/.pi/agent/sessions for new/modified JSONL files.
package watcher

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"agent-web/internal/jsonl"

	"github.com/fsnotify/fsnotify"
)

// Event is emitted by the Watcher when a new JSONL event is found.
type Event struct {
	SessionID string          `json:"session_id"`
	Project   string          `json:"project"` // cwd
	File      string          `json:"file"`    // absolute path
	Data      json.RawMessage `json:"data"`    // raw JSONL line
	Timestamp time.Time       `json:"timestamp"`
}

// Watcher uses fsnotify to tail JSONL files in the sessions directory.
type Watcher struct {
	baseDir   string
	fsw       *fsnotify.Watcher
	decoders  map[string]*jsonl.Decoder // path -> decoder
	mu        sync.Mutex
	events    chan Event
	quit      chan struct{}
	wg        sync.WaitGroup
}

// New creates a Watcher that monitors baseDir for JSONL changes.
func New(baseDir string) (*Watcher, error) {
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve path %s: %w", baseDir, err)
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify: %w", err)
	}

	w := &Watcher{
		baseDir:  abs,
		fsw:      fsw,
		decoders: make(map[string]*jsonl.Decoder),
		events:   make(chan Event, 1024),
		quit:     make(chan struct{}),
	}

	// Walk existing directories and add watches
	if err := w.addWatches(); err != nil {
		fsw.Close()
		return nil, err
	}

	return w, nil
}

// Events returns the read-only event channel.
func (w *Watcher) Events() <-chan Event {
	return w.events
}

// Start begins watching. Blocks until Stop is called.
func (w *Watcher) Start() {
	w.wg.Add(2)
	go w.watchLoop()
	go w.scanLoop()
}

// Stop signals the watcher to shut down and waits for goroutines.
func (w *Watcher) Stop() {
	close(w.quit)
	w.fsw.Close()
	w.wg.Wait()
	close(w.events)
}

// watchLoop handles fsnotify events (new directories, file writes).
func (w *Watcher) watchLoop() {
	defer w.wg.Done()
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleFSNotify(ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			log.Printf("[watcher] error: %v", err)
		case <-w.quit:
			return
		}
	}
}

// scanLoop periodically checks for new files in watched directories.
func (w *Watcher) scanLoop() {
	defer w.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.addWatches()
		case <-w.quit:
			return
		}
	}
}

// addWatches walks baseDir and adds watches for all subdirectories.
func (w *Watcher) addWatches() error {
	return filepath.WalkDir(w.baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors gracefully
		}
		if d.IsDir() {
			w.fsw.Add(path)
		}
		return nil
	})
}

func (w *Watcher) handleFSNotify(ev fsnotify.Event) {
	// Only care about JSONL files
	if filepath.Ext(ev.Name) != ".jsonl" {
		return
	}

	if ev.Op.Has(fsnotify.Create) {
		// New file — start reading from beginning
		w.tailFile(ev.Name)
	} else if ev.Op.Has(fsnotify.Write) {
		// Existing file modified — read new lines
		w.tailFile(ev.Name)
	}
}

// tailFile reads new lines from a JSONL file.
func (w *Watcher) tailFile(path string) {
	w.mu.Lock()
	dec, exists := w.decoders[path]
	w.mu.Unlock()

	if !exists {
		var err error
		dec, err = jsonl.NewDecoder(path, 0)
		if err != nil {
			log.Printf("[watcher] open %s: %v", path, err)
			return
		}
		w.mu.Lock()
		w.decoders[path] = dec
		w.mu.Unlock()
	}

	// Extract session metadata from filename
	sessionID, project := extractMeta(path)

	for {
		event, err := dec.Next()
		if err != nil {
			break // EOF or error
		}
		if event == nil {
			continue
		}

		w.events <- Event{
			SessionID: sessionID,
			Project:   project,
			File:      path,
			Data:      event.Raw,
			Timestamp: time.Now(),
		}
	}
}

// extractMeta pulls session ID and project (cwd) from the file path.
// Format: <baseDir>/<project-encoded>/<timestamp>_<session-id>.jsonl
func extractMeta(path string) (sessionID, project string) {
	dir := filepath.Dir(path)
	project = filepath.Base(dir)

	base := filepath.Base(path)
	// session ID is after the underscore
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '_' {
			sessionID = base[i+1 : len(base)-len(".jsonl")]
			break
		}
	}
	return sessionID, project
}
