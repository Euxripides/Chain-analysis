package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// emitJobEvent 先落 job_event_log 获得单调 id，再扇出到 SSE hub。
// 该日志只保存小型任务事件；数据本体在 MinIO/ClickHouse（§21.5）。
func (s *Server) emitJobEvent(ctx context.Context, jobID, eventType string, payload map[string]any) {
	if s.deps.Store == nil || s.hub == nil {
		return
	}
	clean := map[string]any{}
	for k, v := range payload {
		if strings.HasPrefix(k, "_") {
			continue
		}
		clean[k] = v
	}
	clean["job_id"] = jobID
	clean["event_type"] = eventType
	clean["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(clean)
	if err != nil {
		return
	}
	var id int64
	if err := s.deps.Store.Pool.QueryRow(ctx,
		`INSERT INTO job_event_log(job_id,event_type,payload) VALUES($1,$2,$3::jsonb) RETURNING id`,
		jobID, eventType, raw).Scan(&id); err != nil {
		// SSE 日志失败不能阻断任务控制；保留 NATS/DB 轮询作为降级路径。
		s.deps.Log.Warn("job event log insert failed", "job_id", jobID, "err", err)
		return
	}
	clean["event_id"] = id
	clean["_sse_id"] = id
	out, _ := json.Marshal(clean)
	s.hub.Publish(jobID, out)
}

func jobEventRoute(subject string, data []byte) (string, string, map[string]any, bool) {
	parts := strings.Split(subject, ".")
	if len(parts) < 3 || parts[0] != "events" {
		return "", "", nil, false
	}
	jobID := parts[len(parts)-1]
	if _, err := uuid.Parse(jobID); err != nil {
		return "", "", nil, false
	}
	eventType := strings.Join(parts[1:len(parts)-1], ".")
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil || payload == nil {
		payload = map[string]any{"raw": string(data)}
	}
	return jobID, eventType, payload, true
}

// ---- 任务控制 ----

func (s *Server) handleUserJobCommand(w http.ResponseWriter, r *http.Request) {
	job, ok := s.requireJobScope(r, chi.URLParam(r, "id"))
	if !ok {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "任务不存在或无权访问", nil)
		return
	}
	command := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "command")))
	p := PrincipalFrom(r.Context())
	required := "job:control"
	if command == "retry" {
		required = "job:retry"
	}
	if !p.Has(required) {
		respondError(w, r, http.StatusForbidden, "AUTHZ_FORBIDDEN", "当前账号无权控制该任务", map[string]any{"permission": required})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			validationError(w, r, "请求体不是合法 JSON", nil)
			return
		}
	}
	body.Reason = strings.TrimSpace(body.Reason)

	switch command {
	case "pause", "resume", "cancel":
		target := model.JobPauseReq
		switch command {
		case "resume":
			target = model.JobResuming
		case "cancel":
			target = model.JobCancelReq
		}
		if err := s.validateJobTransition(job, command); err != nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", err.Error(), nil)
			return
		}
		if err := s.deps.Store.SetJobStatus(r.Context(), job.ID, target); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "任务状态更新失败", nil)
			return
		}
		s.writeAuditNow(r, "job."+command, "job", "download_job", job.ID, map[string]any{"target": target, "reason": body.Reason})
		s.emitJobEvent(r.Context(), job.ID, "control", map[string]any{"command": command, "status": target, "reason": body.Reason, "actor": p.IdentityID})
		respondOK(w, r, map[string]any{"job_id": job.ID, "status": target, "command": command})
	case "retry":
		reset, err := s.deps.Store.RetryJobFailures(r.Context(), job.ID)
		if err != nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", err.Error(), nil)
			return
		}
		s.writeAuditNow(r, "job.retry", "job", "download_job", job.ID, map[string]any{"reset_shards": reset, "reason": body.Reason})
		s.emitJobEvent(r.Context(), job.ID, "retry", map[string]any{"status": model.JobQueued, "reset_shards": reset, "reason": body.Reason, "actor": p.IdentityID})
		respondOK(w, r, map[string]any{"job_id": job.ID, "status": model.JobQueued, "command": "retry", "reset_shards": reset,
			"note": "只重置失败分片，已完成分片与成功游标保留。"})
	default:
		validationError(w, r, "不支持的任务命令", []fieldError{{Field: "command", Code: "invalid", Message: command}})
	}
}

func (s *Server) validateJobTransition(job *model.DownloadJob, command string) error {
	switch command {
	case "pause":
		if job.Status != model.JobRunning && job.Status != model.JobQueued && job.Status != model.JobPauseReq {
			return fmt.Errorf("任务当前状态为 %s，不能暂停", job.Status)
		}
	case "resume":
		if job.Status != model.JobPaused && job.Status != model.JobPausedFallbackTooExpensive {
			return fmt.Errorf("任务当前状态为 %s，不能恢复", job.Status)
		}
	case "cancel":
		switch job.Status {
		case model.JobCompleted, model.JobCancelled, model.JobFailed, model.JobPartialFailed, model.JobAbortedByUser:
			return fmt.Errorf("任务已是终态 %s", job.Status)
		}
	}
	return nil
}

// ---- SSE v2：Last-Event-ID 重放 + 快照校准 ----

func (s *Server) handleUserJobEvents(w http.ResponseWriter, r *http.Request) {
	job, ok := s.requireJobScope(r, chi.URLParam(r, "id"))
	if !ok {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "任务不存在或无权访问", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "当前服务器不支持 SSE", nil)
		return
	}
	lastID := int64(0)
	rawLast := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if rawLast == "" {
		rawLast = strings.TrimSpace(r.URL.Query().Get("last_event_id"))
	}
	if rawLast != "" {
		v, err := strconv.ParseInt(rawLast, 10, 64)
		if err != nil || v < 0 {
			validationError(w, r, "Last-Event-ID 不合法", nil)
			return
		}
		lastID = v
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	subID, ch := s.hub.Subscribe(job.ID)
	defer s.hub.Unsubscribe(job.ID, subID)

	// 先订阅再重放，避免重放与实时流之间的空窗丢事件。
	maxSeen := lastID
	if lastID > 0 {
		rows, err := s.deps.Store.Pool.Query(r.Context(), `
			SELECT id, event_type, payload FROM job_event_log
			WHERE job_id=$1 AND id>$2 ORDER BY id ASC LIMIT 500`, job.ID, lastID)
		if err == nil {
			for rows.Next() {
				var id int64
				var eventType string
				var payload []byte
				if rows.Scan(&id, &eventType, &payload) == nil {
					writeSSEFrame(w, id, eventType, payload)
					if id > maxSeen {
						maxSeen = id
					}
				}
			}
			rows.Close()
			flusher.Flush()
		}
	}
	if snap := s.snapshotFor(r.Context(), job.ID); snap != nil {
		frame, _ := json.Marshal(snap)
		_, _ = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", frame)
		flusher.Flush()
	}

	hb := time.NewTicker(15 * time.Second)
	defer hb.Stop()
	var poll *time.Ticker
	if s.deps.Bus == nil || !s.deps.Bus.Enabled() {
		poll = time.NewTicker(3 * time.Second)
		defer poll.Stop()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			var envelope map[string]any
			_ = json.Unmarshal(msg, &envelope)
			id := int64(0)
			if v, ok := envelope["_sse_id"].(float64); ok {
				id = int64(v)
			}
			if id > 0 && id <= maxSeen {
				continue
			}
			if id > maxSeen {
				maxSeen = id
			}
			delete(envelope, "_sse_id")
			payload, _ := json.Marshal(envelope)
			writeSSEFrame(w, id, "update", payload)
			flusher.Flush()
		case <-hb.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-tickC(poll):
			if snap := s.snapshotFor(r.Context(), job.ID); snap != nil {
				frame, _ := json.Marshal(snap)
				_, _ = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", frame)
				flusher.Flush()
			}
		}
	}
}

func writeSSEFrame(w http.ResponseWriter, id int64, eventType string, payload []byte) {
	if id > 0 {
		fmt.Fprintf(w, "id: %d\n", id)
	}
	if eventType == "" {
		eventType = "update"
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
}
