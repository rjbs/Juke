package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"semiotic.systems/juke/internal/store"
)

// ---- push ---------------------------------------------------------------------
//
// The event source of RFC 8620 §7.3.  Each connection remembers the
// states it last reported and sends a StateChange when any differ.  It
// looks again whenever this server commits a /set, which wakes every
// connection at once, and every PushPollInterval besides, which catches
// writes from outside the server, such as an import.

// wakeup is a broadcast: a channel closed, and replaced, on every change.
type wakeup struct {
	mu sync.Mutex
	ch chan struct{}
}

// wait returns a channel that is closed at the next change.
func (w *wakeup) wait() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ch == nil {
		w.ch = make(chan struct{})
	}
	return w.ch
}

func (w *wakeup) wake() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ch != nil {
		close(w.ch)
		w.ch = nil
	}
}

var pushTypes = []string{store.TypeTrack, store.TypeAlbum, store.TypePlaylist, store.TypeSmartPlaylist}

func (s *Server) handleEventSource(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	types := parsePushTypes(q.Get("types"))
	closeAfter := q.Get("closeafter")
	if closeAfter != "" && closeAfter != "state" && closeAfter != "no" {
		http.Error(w, "closeafter must be state or no", http.StatusBadRequest)
		return
	}
	ping := 0
	if p := q.Get("ping"); p != "" {
		var err error
		ping, err = strconv.Atoi(p)
		if err != nil || ping < 0 {
			http.Error(w, "ping must be a whole number of seconds", http.StatusBadRequest)
			return
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	// The wait for the next wakeup always begins before the states it's
	// compared with are read, so a commit between the two isn't missed.
	// -- claude, 2026-10-25
	woken := s.changed.wait()
	last, err := s.pushStates(ctx, types)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// A comment, so the client sees the stream open before any event.
	fmt.Fprint(w, ": juke\n\n")
	flusher.Flush()

	poll := time.NewTicker(s.cfg.PushPollInterval)
	defer poll.Stop()
	var pings <-chan time.Time
	if ping > 0 {
		t := time.NewTicker(time.Duration(ping) * time.Second)
		defer t.Stop()
		pings = t.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-pings:
			fmt.Fprintf(w, "event: ping\ndata: {\"interval\":%d}\n\n", ping)
			flusher.Flush()
			continue
		case <-woken:
		case <-poll.C:
		}
		woken = s.changed.wait()
		now, err := s.pushStates(ctx, types)
		if err != nil {
			s.cfg.Log.Printf("eventsource: %v", err)
			return
		}
		changed := map[string]string{}
		for typ, state := range now {
			if last[typ] != state {
				changed[typ] = state
			}
		}
		if len(changed) == 0 {
			continue
		}
		last = now
		data, _ := json.Marshal(map[string]any{
			"@type":   "StateChange",
			"changed": map[string]any{s.cfg.AccountID: changed},
		})
		fmt.Fprintf(w, "event: state\ndata: %s\n\n", data)
		flusher.Flush()
		if closeAfter == "state" {
			return
		}
	}
}

// parsePushTypes reads the types parameter: "*" or a comma-separated
// list.  Types this server doesn't have are ignored, as RFC 8620 says.
func parsePushTypes(param string) []string {
	if param == "" || param == "*" {
		return pushTypes
	}
	known := map[string]bool{}
	for _, t := range pushTypes {
		known[t] = true
	}
	var types []string
	for _, t := range strings.Split(param, ",") {
		if known[t] {
			types = append(types, t)
		}
	}
	return types
}

func (s *Server) pushStates(ctx context.Context, types []string) (map[string]string, error) {
	out := map[string]string{}
	for _, t := range types {
		state, _, err := s.state(ctx, s.store.DB, t)
		if err != nil {
			return nil, err
		}
		out[t] = state
	}
	return out, nil
}
