package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"
)

//go:embed static
var staticFiles embed.FS

// ---------------------------------------------------------------------------
// SSE broker — fan-out events to all connected clients
// ---------------------------------------------------------------------------

type sseBroker struct {
	mu      sync.RWMutex
	clients map[chan string]struct{}
}

var broker = &sseBroker{clients: make(map[chan string]struct{})}

func (b *sseBroker) subscribe() chan string {
	ch := make(chan string, 16)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *sseBroker) unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
	close(ch)
}

func (b *sseBroker) broadcast(event, data string) {
	msg := "event: " + event + "\ndata: " + data + "\n\n"
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default: // slow client, skip
		}
	}
}

// ---------------------------------------------------------------------------
// Background poller — pushes diffs every 3 s
// ---------------------------------------------------------------------------

type snapshot struct {
	status    ConnectionStatus
	workloads []Workload
}

var (
	lastSnap   snapshot
	lastSnapMu sync.Mutex
)

func startPoller(ctx context.Context) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			pollAndBroadcast(ctx)
		}
	}
}

func pollAndBroadcast(ctx context.Context) {
	status := GetStatus(ctx)

	lastSnapMu.Lock()
	lastSnap.status = status
	lastSnapMu.Unlock()

	if b, err := json.Marshal(status); err == nil {
		broker.broadcast("status", string(b))
	}

	if !status.Connected {
		return
	}

	// We don't have namespace context here, use empty (default)
	workloads, err := ListWorkloads(ctx, "")
	if err == nil {
		lastSnapMu.Lock()
		lastSnap.workloads = workloads
		lastSnapMu.Unlock()
		if b, err := json.Marshal(workloads); err == nil {
			broker.broadcast("workloads", string(b))
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func handleStatus(w http.ResponseWriter, r *http.Request) {
	lastSnapMu.Lock()
	s := lastSnap.status
	lastSnapMu.Unlock()
	writeJSON(w, http.StatusOK, s)
}

func handleWorkloads(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	workloads, err := ListWorkloads(r.Context(), ns)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, workloads)
}

func handleNamespaces(w http.ResponseWriter, r *http.Request) {
	ns, err := ListNamespaces(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ns)
}

func handleIntercept(w http.ResponseWriter, r *http.Request) {
	var req InterceptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.Workload == "" {
		writeError(w, http.StatusBadRequest, "workload is required")
		return
	}
	if err := StartIntercept(req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Give telepresence a moment then push a fresh workloads event
	go func() {
		time.Sleep(2 * time.Second)
		pollAndBroadcast(context.Background())
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "intercept started", "workload": req.Workload})
}

func handleLeave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := LeaveIntercept(r.Context(), body.Name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go func() {
		time.Sleep(2 * time.Second)
		pollAndBroadcast(context.Background())
	}()
	writeJSON(w, http.StatusOK, map[string]string{"status": "left", "name": body.Name})
}

func handleConnect(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	if err := Connect(r.Context(), ns); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go func() {
		time.Sleep(2 * time.Second)
		pollAndBroadcast(context.Background())
	}()
	writeJSON(w, http.StatusOK, map[string]string{"status": "connected"})
}

func handleQuit(w http.ResponseWriter, r *http.Request) {
	if err := Quit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go func() {
		time.Sleep(2 * time.Second)
		pollAndBroadcast(context.Background())
	}()
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

// handleSSE streams events to the browser (Server-Sent Events).
func handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := broker.subscribe()
	defer broker.unsubscribe(ch)

	// Send current snapshot immediately so UI doesn't wait 3 s on load
	lastSnapMu.Lock()
	snap := lastSnap
	lastSnapMu.Unlock()
	if b, err := json.Marshal(snap.status); err == nil {
		_, _ = w.Write([]byte("event: status\ndata: " + string(b) + "\n\n"))
	}
	if snap.workloads != nil {
		if b, err := json.Marshal(snap.workloads); err == nil {
			_, _ = w.Write([]byte("event: workloads\ndata: " + string(b) + "\n\n"))
		}
	}
	flusher.Flush()

	// Keep-alive ticker
	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			_, _ = w.Write([]byte(msg))
			flusher.Flush()
		case <-keepAlive.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		}
	}
}

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

func newRouter() http.Handler {
	mux := http.NewServeMux()

	// API
	mux.HandleFunc("GET /api/status", handleStatus)
	mux.HandleFunc("GET /api/workloads", handleWorkloads)
	mux.HandleFunc("GET /api/namespaces", handleNamespaces)
	mux.HandleFunc("POST /api/intercept", handleIntercept)
	mux.HandleFunc("POST /api/leave", handleLeave)
	mux.HandleFunc("POST /api/connect", handleConnect)
	mux.HandleFunc("POST /api/quit", handleQuit)

	// SSE
	mux.HandleFunc("GET /events", handleSSE)

	// Static files (index.html etc.)
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal("embed error:", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	return mux
}
