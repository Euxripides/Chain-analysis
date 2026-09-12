package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// SSEHub 按 job_id 向 SSE 客户端扇出事件。
type SSEHub struct {
	mu      sync.Mutex
	clients map[string]map[string]chan []byte
}

func NewSSEHub() *SSEHub {
	return &SSEHub{clients: map[string]map[string]chan []byte{}}
}

func (h *SSEHub) Subscribe(jobID string) (string, chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[jobID] == nil {
		h.clients[jobID] = map[string]chan []byte{}
	}
	id := uuid.NewString()
	ch := make(chan []byte, 256)
	h.clients[jobID][id] = ch
	return id, ch
}

func (h *SSEHub) Unsubscribe(jobID, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.clients[jobID]; m != nil {
		delete(m, id)
		if len(m) == 0 {
			delete(h.clients, jobID)
		}
	}
}

func (h *SSEHub) Publish(jobID string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.clients[jobID] {
		select {
		case ch <- data:
		default: // 客户端消费慢则丢弃，等待下一次聚合推送
		}
	}
}

// startBusForwarder 订阅 NATS 事件并转发到 SSE Hub。
func (s *Server) startBusForwarder(log *slog.Logger) {
	if s.deps.Bus == nil {
		return
	}
	stop, err := s.deps.Bus.Subscribe("events.>", func(subject string, data []byte) {
		if s.runtime != nil {
			s.runtime.HandleJobEvent(context.Background(), subject, data)
		}
		// S5：任务事件先落 job_event_log 拿到单调 id，再扇出；
		// 这样浏览器断线重连时 Last-Event-ID 可以精确重放（§18.5/§18.11）。
		if jobID, eventType, payload, ok := jobEventRoute(subject, data); ok {
			s.emitJobEvent(context.Background(), jobID, eventType, payload)
			return
		}
		idx := strings.LastIndex(subject, ".")
		if idx < 0 {
			return
		}
		s.hub.Publish(subject[idx+1:], data)
	})
	if err != nil {
		log.Warn("event bus subscribe failed, SSE falls back to DB polling", "err", err)
		return
	}
	s.busStop = stop
}

func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if _, err := s.deps.Store.GetJob(r.Context(), jobID); err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	id, ch := s.hub.Subscribe(jobID)
	defer s.hub.Unsubscribe(jobID, id)
	// A subscriber must receive the current state immediately. With NATS
	// enabled there may be no future event for an already completed job, and
	// waiting for the heartbeat makes the stream appear broken.
	if snap := s.snapshotFor(r.Context(), jobID); snap != nil {
		b, _ := json.Marshal(snap)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}

	hb := time.NewTicker(15 * time.Second)
	defer hb.Stop()
	var poll *time.Ticker
	if s.deps.Bus == nil || !s.deps.Bus.Enabled() {
		poll = time.NewTicker(2 * time.Second)
		defer poll.Stop()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			fl.Flush()
		case <-hb.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case <-tickC(poll):
			if snap := s.snapshotFor(r.Context(), jobID); snap != nil {
				b, _ := json.Marshal(snap)
				fmt.Fprintf(w, "data: %s\n\n", b)
				fl.Flush()
			}
		}
	}
}

func tickC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// publishEvent 通过 NATS 发布事件（API 侧状态变更）。
func (s *Server) publishEvent(subject string, payload any) {
	if s.deps.Bus == nil {
		return
	}
	s.deps.Bus.Publish(context.Background(), subject, payload)
}
