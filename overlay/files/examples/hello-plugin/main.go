// Copyright (c) 2026 LucX-UI Project.
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

func main() {
	panel, err := NewPanel()
	if err != nil {
		log.Printf("panel API unavailable, /clients will answer 503: %v", err)
	}
	log.Print("hello plugin ready")
	if err := Serve(newMux(panel, StateDir())); err != nil {
		log.Fatal(err)
	}
}

func newMux(panel *Panel, stateDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"message": "hello", "user": RequestUser(r)})
	})
	mux.HandleFunc("GET /clients", func(w http.ResponseWriter, r *http.Request) {
		if panel == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no panel token"})
			return
		}
		var clients []json.RawMessage
		if err := panel.Do(r.Context(), http.MethodGet, "/clients/list", nil, &clients); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"clients": len(clients)})
	})
	events := &eventLog{}
	counter := &counter{path: filepath.Join(stateDir, "counter")}
	mux.Handle("POST /_lucx/events", EventHandler(events.add))
	// The panel calls these once after the first healthy start and just before removal.
	mux.HandleFunc("POST /_lucx/install", func(w http.ResponseWriter, _ *http.Request) {
		if err := os.WriteFile(filepath.Join(stateDir, "installed"), []byte("1\n"), 0o600); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		log.Print("hello plugin: install hook ran")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /_lucx/uninstall", func(w http.ResponseWriter, _ *http.Request) {
		if err := os.WriteFile(filepath.Join(stateDir, "uninstalled"), []byte("1\n"), 0o600); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		log.Print("hello plugin: uninstall hook ran")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, events.last())
	})
	mux.HandleFunc("POST /counter", func(w http.ResponseWriter, _ *http.Request) {
		n, err := counter.increment()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"counter": n})
	})
	mux.Handle("/ui/", pageHandler())
	// The panel's page bridge only reaches /api/..., so the same routes answer below that prefix.
	outer := http.NewServeMux()
	outer.Handle("/api/", http.StripPrefix("/api", mux))
	outer.Handle("/", mux)
	return outer
}

// counter keeps a number in the plugin's state directory so it survives restarts.
type counter struct {
	mu   sync.Mutex
	path string
}

func (c *counter) increment() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	if raw, err := os.ReadFile(c.path); err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	n++
	return n, os.WriteFile(c.path, []byte(strconv.Itoa(n)), 0o600)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// eventLog keeps the latest events the panel delivered, to show the mechanism.
type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func (l *eventLog) add(ev Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
	if len(l.events) > 20 {
		l.events = l.events[len(l.events)-20:]
	}
}

func (l *eventLog) last() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Event{}, l.events...)
}
