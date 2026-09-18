package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// progressEventType 是进度类事件的 event_type（由 events.progress.<jobID> 经
// jobEventRoute 解析得到）。
const progressEventType = "progress"

// progressEventPersistInterval 是**同一个任务**的进度类事件落 job_event_log 的最小间隔。
//
// 为什么是 10 秒：进度快照的生产频率是 1 秒/活跃任务（internal/download/progress.go 的
// progressLoop），而 job_event_log 已经 3.99 GB、实测 392 行/分钟，其中绝大多数是**数值几乎
// 不变的进度快照**。每行都是一次 INSERT + JSONB 写入，持续产生 WAL 与死元组。
// 10 秒粒度足以让“任务进度历史”保持可用（审计与状态迁移本来就不依赖进度帧），
// 同时把这一步的写入量降到原来的 1/10 以下。
const progressEventPersistInterval = 10 * time.Second

// jobEventThrottle 是 per-job、**进程内内存**的进度事件落盘节流器。
//
// 设计约束（与需求一一对应）：
//   - 只节流**落盘**，不节流**推送**：被节流的进度事件仍然照常 hub.Publish 到 SSE，
//     前端看到的进度刷新频率保持 1 秒不变（实时性不受影响）。
//   - 只作用于 eventType == "progress"。状态变化、终态（COMPLETED/FAILED/CANCELLED/
//     PARTIAL_FAILED）、错误、分片迁移（shards）与控制指令（control/retry/admin_control）
//     一律**立即落盘**，不受节流影响——SSE 重放的“真相”与审计都依赖它们。
//   - 节流状态不持久化：进程重启后重新开始计时，最多多写一行，无正确性影响。
//
// 权衡（必须知情，不能当作没有代价）：
// 被节流掉的进度事件**没有** job_event_log.id，因此也**不会**带上 SSE 的 id。
// 浏览器断线重连时的 Last-Event-ID 重放以**持久 id**（job_event_log.id）为水位，
// 所以重放会**缺失 10 秒粒度内的这些进度帧**：客户端重连后会立刻收到 snapshot 事件
// （handleUserJobEvents 里的 snapshotFor 校准帧）以及随后的实时进度帧，
// 进度显示不会倒退、不会卡住，但中间那几帧的“历史回放”确实没有。
// 这是“1 秒实时推送”与“落盘降到 ≤1 次/10 秒”之间被明确接受的取舍；
// 需要完整进度历史的场景应改用导出/指标（Prometheus 的 Download* 指标不经过这条路）。
type jobEventThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// shouldPersistProgress 判断该任务此刻的进度事件是否应当落盘（只读判断，不记录）。
func (t *jobEventThrottle) shouldPersistProgress(jobID string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, ok := t.last[jobID]
	return !ok || now.Sub(prev) >= progressEventPersistInterval
}

// markProgressPersisted 在**成功落盘之后**记录时间戳；插入失败时不记录，
// 这样下一次进度事件会立刻重试，而不会因为一次失败丢掉整整 10 秒的进度历史。
func (t *jobEventThrottle) markProgressPersisted(jobID string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = map[string]time.Time{}
	}
	t.last[jobID] = now
	// 惰性清理：进度事件只对活跃任务产生，超过 10 分钟没有再落盘的任务视为已结束，
	// 避免 API 长期运行时这个 map 无界增长。
	if len(t.last) > 256 {
		cutoff := now.Add(-10 * time.Minute)
		for id, at := range t.last {
			if at.Before(cutoff) {
				delete(t.last, id)
			}
		}
	}
}

// emitJobEvent 先落 job_event_log 获得单调 id，再扇出到 SSE hub。
// 该日志只保存小型任务事件；数据本体在 MinIO/ClickHouse（§21.5）。
//
// 进度类事件按 job 做 10 秒粒度的**落盘**节流（见 jobEventThrottle）；其余事件照旧每条落盘。
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
	persist := true
	if eventType == progressEventType {
		persist = s.jobEvents.shouldPersistProgress(jobID, time.Now())
	}
	if persist {
		var id int64
		if err := s.deps.Store.Pool.QueryRow(ctx,
			`INSERT INTO job_event_log(job_id,event_type,payload) VALUES($1,$2,$3::jsonb) RETURNING id`,
			jobID, eventType, raw).Scan(&id); err != nil {
			// SSE 日志失败不能阻断任务控制；保留 NATS/DB 轮询作为降级路径。
			s.deps.Log.Warn("job event log insert failed", "job_id", jobID, "err", err)
			return
		}
		if eventType == progressEventType {
			s.jobEvents.markProgressPersisted(jobID, time.Now())
		}
		clean["event_id"] = id
		clean["_sse_id"] = id
	}
	// 被节流的进度事件没有 event_id/_sse_id：writeSSEFrame 只在 id>0 时写 "id:" 行，
	// 因此客户端的 Last-Event-ID 会停留在此前最后一条**已落盘**事件上（重放语义见 jobEventThrottle）。
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

	// C1-SSE-OLD-SCOPE-OPEN: register the subscription with the scope it was
	// authorised under, so a workspace switch, logout or revoke closes it.
	sub := s.openSSESubscription(w, r, job.ID)
	defer s.closeSSESubscription(r, sub)
	ch := sub.ch

	// 先订阅再重放，避免重放与实时流之间的空窗丢事件。
	maxSeen := lastID
	if lastID > 0 && !s.sseFrameAllowed(sub) {
		return
	}
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
		if !s.sseFrameAllowed(sub) {
			return
		}
		frame, _ := json.Marshal(snap)
		_, _ = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", frame)
		flusher.Flush()
	}

	hb := time.NewTicker(15 * time.Second)
	defer hb.Stop()
	// P1-B: same deterministic AUTH-007 bound as the v1 stream. This tick writes
	// no frame; it only re-reads the live session row and closes the stream when
	// the scope it was authorised under is gone, so a subscription never depends
	// on the bus control message being delivered.
	scopeTick := time.NewTicker(sseScopeRecheckInterval())
	defer scopeTick.Stop()
	var poll *time.Ticker
	if s.deps.Bus == nil || !s.deps.Bus.Enabled() {
		poll = time.NewTicker(3 * time.Second)
		defer poll.Stop()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.closed:
			// A scope change closed this stream; return without another frame so
			// the client observes EOF and the suspended scope gets no ping.
			return
		case msg := <-ch:
			if !s.sseFrameAllowed(sub) {
				return
			}
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
			if !s.sseFrameAllowed(sub) {
				return
			}
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-scopeTick.C:
			if !s.sseFrameAllowed(sub) {
				return
			}
		case <-tickC(poll):
			if !s.sseFrameAllowed(sub) {
				return
			}
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
