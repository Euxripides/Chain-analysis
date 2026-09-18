package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
)

// ---------------------------------------------------------------------------
// C1-SSE-OLD-SCOPE-OPEN / C1-SSE-NO-SUBSCRIPTION-AUDIT.
//
// Measured defects (c1-sse-scope-20260913T181737Z):
//  1. a subscription opened while the session was in workspace A stayed open and
//     kept receiving frames (" : ping") after the same session had been switched
//     to workspace B -- the per-request scope check does not apply to a long
//     lived connection;
//  2. opening or closing a subscription wrote no audit row at all, so a family
//     of long-lived authenticated reads was invisible in audit_events_v2.
//
// Fix, in this file:
//  - every subscription is registered server-side with session_id, workspace_id,
//    organization_id, identity_id, job, route and request_id;
//  - a scope change (workspace switch, logout, revoke, revoke-all, password
//    change, administrative revoke, membership revocation) force-closes every
//    subscription of that session/identity in this process;
//  - the same control message is published on the event bus so other API
//    instances close their local subscriptions too, and the live session row is
//    re-read before EVERY frame (data and heartbeat) as defence in depth, which
//    also covers an instance that never saw the control message;
//  - open / close / scope-invalidated each write one audit_events_v2 row with
//    the subscription, job, session, workspace, route, request id and reason.
//
// Honest boundary: the hub itself is per process. Immediate closure is
// guaranteed by the in-process registry; across instances the guarantee is the
// bus control message PLUS the scope re-check tick described below, whose default
// period is 2 s and whose configured maximum is 4 s. The bare 15 s heartbeat is a
// keep-alive, never the bound. This comment used to claim the worst case was one
// heartbeat interval (15 s), which was both stale and outside the AUTH-007
// budget; the bound is now stated by the constant and enforced by the clamp.
// Nothing here claims a distributed hub.
// ---------------------------------------------------------------------------

// sseScopeControlSubject carries "close the subscriptions of this session or
// identity" to every API instance listening on the event bus.
const sseScopeControlSubject = "events.sse.scope_invalidated"

// P1-B: the deterministic AUTH-007 bound for a cross-instance stream.
//
// The bus control message closes a remote subscription immediately, but a bus is
// not a delivery guarantee: an instance that joined late, missed the message or
// lost its bus connection entirely must still stop sending bytes once the scope
// it was authorised under is gone. The heartbeat alone is 15 s, which is far
// outside the 5 s AUTH-007 requirement, so the stream carries a second, faster
// tick whose only job is to re-read the live session row:
//
//	sseScopeRecheckDefaultMS -- the default period, well under 5 s;
//	SSE_SCOPE_RECHECK_MS     -- environment override, clamped to [250, 15000].
//
// The tick never writes a frame. It either proves the scope is still valid or it
// closes the stream, so a healthy subscription produces no extra traffic and an
// abandoned one cannot outlive the period by more than one DB round trip.
const (
	sseScopeRecheckDefaultMS = 2000
	sseScopeRecheckMinMS     = 250
	// The maximum is deliberately below the 5 s AUTH-007 budget: an operator
	// override must never be able to configure the fail-closed cross-instance
	// bound outside the requirement it exists to satisfy.
	sseScopeRecheckMaxMS = 4000
)

// sseScopeRecheckInterval resolves the re-check period from the environment.
func sseScopeRecheckInterval() time.Duration {
	ms := sseScopeRecheckDefaultMS
	if raw := strings.TrimSpace(os.Getenv("SSE_SCOPE_RECHECK_MS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			ms = v
		}
	}
	if ms < sseScopeRecheckMinMS {
		ms = sseScopeRecheckMinMS
	}
	if ms > sseScopeRecheckMaxMS {
		ms = sseScopeRecheckMaxMS
	}
	return time.Duration(ms) * time.Millisecond
}

// sseScopeControlDisabled reports whether this instance must behave as if the
// bus control message never arrived. It exists for the negative calibration of
// the cross-instance gate: an instance started with SSE_SCOPE_CONTROL_DISABLE=1
// ignores events.sse.scope_invalidated exactly as an instance with a broken or
// absent bus would, and therefore has to fall back to the re-check tick. It is
// never set on a production instance.
func sseScopeControlDisabled() bool {
	return strings.TrimSpace(os.Getenv("SSE_SCOPE_CONTROL_DISABLE")) == "1"
}

// sseSubscription is one live server-sent-events subscription plus the scope it
// was authorised under.
type sseSubscription struct {
	ID             string
	JobID          string
	SessionID      string
	WorkspaceID    string
	OrganizationID string
	IdentityID     string
	Route          string
	RequestID      string
	TraceID        string
	OpenedAt       time.Time

	ch       chan []byte
	closed   chan struct{}
	once     sync.Once
	audited  sync.Once
	mu       sync.Mutex
	reason   string
	closedBy string
}

func (sub *sseSubscription) forceClose(reason, closedBy string) {
	sub.mu.Lock()
	if sub.reason == "" {
		sub.reason = reason
		sub.closedBy = closedBy
	}
	sub.mu.Unlock()
	sub.once.Do(func() { close(sub.closed) })
}

// closeReason is the reason recorded when this subscription ends: the forced
// reason when a scope change closed it, otherwise the empty string (which the
// caller maps to a normal client-side end of stream).
func (sub *sseSubscription) closeReason() (string, string) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	return sub.reason, sub.closedBy
}

// SSEHub fans job events out to SSE clients and keeps the scope of every
// subscription so a scope change can close exactly the affected streams.
type SSEHub struct {
	mu         sync.Mutex
	clients    map[string]map[string]*sseSubscription // job id -> subscription id
	bySession  map[string]map[string]*sseSubscription // session id -> subscription id
	byIdentity map[string]map[string]*sseSubscription // identity id -> subscription id
}

func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients:    map[string]map[string]*sseSubscription{},
		bySession:  map[string]map[string]*sseSubscription{},
		byIdentity: map[string]map[string]*sseSubscription{},
	}
}

func addIndex(idx map[string]map[string]*sseSubscription, key string, sub *sseSubscription) {
	if key == "" {
		return
	}
	if idx[key] == nil {
		idx[key] = map[string]*sseSubscription{}
	}
	idx[key][sub.ID] = sub
}

func dropIndex(idx map[string]map[string]*sseSubscription, key, id string) {
	if key == "" {
		return
	}
	m := idx[key]
	if m == nil {
		return
	}
	delete(m, id)
	if len(m) == 0 {
		delete(idx, key)
	}
}

// Register adds a subscription to the fan-out and to the scope indexes.
func (h *SSEHub) Register(sub *sseSubscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[sub.JobID] == nil {
		h.clients[sub.JobID] = map[string]*sseSubscription{}
	}
	h.clients[sub.JobID][sub.ID] = sub
	addIndex(h.bySession, sub.SessionID, sub)
	addIndex(h.byIdentity, sub.IdentityID, sub)
}

// Unsubscribe removes a subscription; it is idempotent.
func (h *SSEHub) Unsubscribe(jobID, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.clients[jobID]; m != nil {
		sub := m[id]
		delete(m, id)
		if len(m) == 0 {
			delete(h.clients, jobID)
		}
		if sub != nil {
			dropIndex(h.bySession, sub.SessionID, id)
			dropIndex(h.byIdentity, sub.IdentityID, id)
		}
	}
}

func takeAll(idx map[string]map[string]*sseSubscription, key string) []*sseSubscription {
	if key == "" {
		return nil
	}
	m := idx[key]
	if m == nil {
		return nil
	}
	out := make([]*sseSubscription, 0, len(m))
	for id, sub := range m {
		out = append(out, sub)
		delete(m, id)
	}
	delete(idx, key)
	return out
}

// CloseSession force-closes every subscription of one session and detaches it
// from both indexes. The caller records the reason and audits each closure.
func (h *SSEHub) CloseSession(sessionID string) []*sseSubscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.detachLocked(takeAll(h.bySession, sessionID))
}

// CloseIdentity force-closes every subscription of one identity (used when a
// password change or a token-reuse response revokes the whole session family).
func (h *SSEHub) CloseIdentity(identityID string) []*sseSubscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.detachLocked(takeAll(h.byIdentity, identityID))
}

func (h *SSEHub) detachLocked(subs []*sseSubscription) []*sseSubscription {
	for _, sub := range subs {
		if m := h.clients[sub.JobID]; m != nil {
			delete(m, sub.ID)
			if len(m) == 0 {
				delete(h.clients, sub.JobID)
			}
		}
		dropIndex(h.bySession, sub.SessionID, sub.ID)
		dropIndex(h.byIdentity, sub.IdentityID, sub.ID)
	}
	return subs
}

// SubscriberCount is the number of live subscriptions (diagnostics only).
func (h *SSEHub) SubscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, m := range h.clients {
		n += len(m)
	}
	return n
}

func (h *SSEHub) Publish(jobID string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.clients[jobID] {
		select {
		case sub.ch <- data:
		default: // 客户端消费慢则丢弃，等待下一次聚合推送
		}
	}
}

// sseScopeControlMsg is the bus payload of a scope-change control message.
type sseScopeControlMsg struct {
	SessionID  string `json:"session_id"`
	IdentityID string `json:"identity_id"`
	Reason     string `json:"reason"`
	RequestID  string `json:"request_id"`
	OccurredAt string `json:"occurred_at"`
}

// busPublishTimeout bounds every best-effort publish the request path makes.
//
// AUTH-007 (2026-09-15): the two publishes below used context.Background() with
// no deadline at all. go-nats waits for the JetStream acknowledgement, so a bus
// that accepts the TCP connection and then stops answering held the whole scope
// mutation open: the watchdog-protected SIGSTOP drill measured the workspace
// switch, logout and admin force revoke all exceeding the 12 s client bound while
// the bus was paused, even though every one of them still took effect in the
// database afterwards. That is an availability defect, not a correctness one, and
// it is fixed here rather than by weakening anything: the local SSE close, the
// database mutation and its audit row have all already happened when these
// publishes run. The bound is a var, not a const, so a test can pin it.
var busPublishTimeout = 750 * time.Millisecond

// publishBestEffort publishes without ever letting the bus delay a request
// unboundedly. The caller has already committed its own state, so a notification
// that no instance may ever receive must not become the caller's problem, and a
// timeout or a failure here is contained by construction.
func (s *Server) publishBestEffort(subject string, payload any) {
	if s.deps.Bus == nil || !s.deps.Bus.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), busPublishTimeout)
	defer cancel()
	s.deps.Bus.Publish(ctx, subject, payload)
}

// publishSSEScopeControl asks every API instance to close the subscriptions of
// this session (or identity). Best effort: the local close never depends on it.
func (s *Server) publishSSEScopeControl(sessionID, identityID, reason, requestID string) {
	if s.deps.Bus == nil || !s.deps.Bus.Enabled() {
		return
	}
	if sessionID == "" && identityID == "" {
		return
	}
	s.publishBestEffort(sseScopeControlSubject, sseScopeControlMsg{
		SessionID:  sessionID,
		IdentityID: identityID,
		Reason:     reason,
		RequestID:  requestID,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// handleSSEScopeControl closes this instance's subscriptions for the scope named
// in a control message.
func (s *Server) handleSSEScopeControl(data []byte) {
	var msg sseScopeControlMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	if s.hub == nil {
		return
	}
	for _, sub := range s.hub.CloseSession(msg.SessionID) {
		sub.forceClose(msg.Reason, msg.RequestID)
	}
	for _, sub := range s.hub.CloseIdentity(msg.IdentityID) {
		sub.forceClose(msg.Reason, msg.RequestID)
	}
}

// sseScopeCloseReason explains a forced closure; the empty reason means the
// client simply went away.
const (
	sseReasonWorkspaceSwitched = "session_workspace_changed"
	sseReasonSessionRevoked    = "session_revoked"
	sseReasonLoggedOut         = "session_logged_out"
	sseReasonIdentityRevoked   = "identity_sessions_revoked"
	sseReasonMembershipRevoked = "workspace_access_revoked"
	sseReasonTokenReuse        = "session_token_reuse"
)

// closeScopeSSESubscriptions closes the subscriptions of one session and/or one
// identity in this process, publishes the control message for other instances
// and returns how many local streams were force-closed. Every closure writes one
// audit row through the stream's own deferred close, which guarantees exactly
// one row per subscription.
func (s *Server) closeScopeSSESubscriptions(sessionID, identityID, reason string, r *http.Request) int {
	requestID := ""
	if r != nil {
		requestID = metaFrom(r.Context()).RequestID
	}
	return s.closeScopeSSESubscriptionsCtx(sessionID, identityID, reason, requestID)
}

// closeScopeSSESubscriptionsCtx is the same as closeScopeSSESubscriptions for
// callers that only hold a context (identity middleware and token-reuse paths).
func (s *Server) closeScopeSSESubscriptionsCtx(sessionID, identityID, reason, requestID string) int {
	if s.hub == nil {
		return 0
	}
	n := 0
	for _, sub := range s.hub.CloseSession(sessionID) {
		sub.forceClose(reason, requestID)
		n++
	}
	for _, sub := range s.hub.CloseIdentity(identityID) {
		sub.forceClose(reason, requestID)
		n++
	}
	s.publishSSEScopeControl(sessionID, identityID, reason, requestID)
	return n
}

// openSSESubscription registers the calling handler's subscription, recording
// the scope it was authorised under, and writes the "subscribed" audit row.
func (s *Server) openSSESubscription(w http.ResponseWriter, r *http.Request, jobID string) *sseSubscription {
	p := PrincipalFrom(r.Context())
	meta := metaFrom(r.Context())
	sub := &sseSubscription{
		ID:             uuid.NewString(),
		JobID:          jobID,
		SessionID:      p.SessionID,
		WorkspaceID:    p.WorkspaceID,
		OrganizationID: p.OrganizationID,
		IdentityID:     p.IdentityID,
		Route:          r.URL.Path,
		RequestID:      meta.RequestID,
		TraceID:        meta.TraceID,
		OpenedAt:       time.Now().UTC(),
		ch:             make(chan []byte, 256),
		closed:         make(chan struct{}),
	}
	s.hub.Register(sub)
	s.auditSSEEvent(r, "job.events.subscribed", "success", sub, "", "", nil)
	return sub
}

// sseFrameAllowed re-reads the live session row before a frame is written. It is
// the cross-instance and post-restart half of the fix: an instance that never
// received the control message still refuses to send another byte once the
// session workspace moved or the session was revoked. A frame is never written
// when this returns false, so a closed stream stays silent.
func (s *Server) sseFrameAllowed(sub *sseSubscription) bool {
	if sub.SessionID == "" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	current, found, err := s.liveSessionWorkspace(ctx, sub.SessionID)
	switch {
	case err != nil:
		sub.forceClose("session_scope_unverifiable", "")
		return false
	case !found:
		sub.forceClose(sseReasonSessionRevoked, "")
		return false
	case current != sub.WorkspaceID:
		sub.forceClose(sseReasonWorkspaceSwitched, "")
		return false
	}
	return true
}

// closeSSESubscription detaches the subscription and writes exactly one close
// audit row: scope_invalidated when a scope change closed it, unsubscribed when
// the client simply went away.
func (s *Server) closeSSESubscription(r *http.Request, sub *sseSubscription) {
	s.hub.Unsubscribe(sub.JobID, sub.ID)
	// Exactly one close row per subscription, however many times a caller reaches
	// this point: a stream can end through the closed channel, the request context
	// or a frame re-check, and every one of those paths runs the same deferred
	// call.
	sub.audited.Do(func() {
		reason, closedBy := sub.closeReason()
		action := "job.events.unsubscribed"
		result := "success"
		detail := map[string]any{"duration_ms": time.Since(sub.OpenedAt).Milliseconds()}
		if reason != "" {
			action = "job.events.scope_invalidated"
			result = "deny"
			detail["reason"] = reason
			detail["closed_by_request_id"] = closedBy
		}
		s.auditSSEEvent(r, action, result, sub, reason, closedBy, detail)
	})
}

// auditSSEEvent writes one subscription audit row. The write is synchronous and
// detached from the client connection (a client that already went away must not
// lose the close row) and bounded by emitAudit to 5 s. It never records a
// cookie, a token or a password.
func (s *Server) auditSSEEvent(r *http.Request, action, result string, sub *sseSubscription, reason, closedBy string, detail map[string]any) {
	if s.auditSink == nil && s.audit == nil {
		return
	}
	p := PrincipalFrom(r.Context())
	meta := metaFrom(r.Context())
	d := map[string]any{
		"subscription_id": sub.ID,
		"job_id":          sub.JobID,
		"session_id":      sub.SessionID,
		"workspace_id":    sub.WorkspaceID,
	}
	for k, v := range detail {
		d[k] = v
	}
	if reason != "" {
		d["reason"] = reason
	}
	if closedBy != "" {
		d["closed_by_request_id"] = closedBy
	}
	ev := audit.Event{
		ActorType: "user", ActorID: p.IdentityID, ActorEmail: p.Email, ActorName: p.DisplayName,
		ActorRoles: p.Roles, OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID,
		SessionID: p.SessionID, DeviceID: p.DeviceID,
		Action: action, Category: "authorization",
		ResourceType: "download_job", ResourceID: sub.JobID, JobID: sub.JobID,
		HTTPMethod: r.Method, Route: sub.Route, RequestID: sub.RequestID, TraceID: sub.TraceID,
		Result: result, HTTPStatus: http.StatusOK,
		PermissionKey: "job:read", PolicyDecision: "allow", DenyReason: reason,
		RiskLevel: "low", Sensitive: true,
		PeerIP: meta.IP.PeerIP, ClientIP: meta.IP.ClientIP, ProxyChain: meta.IP.ProxyChain,
		UserAgent: r.UserAgent(),
		Detail:    d,
	}
	if err := s.emitAudit(auditRequestDetached(r).Context(), ev); err != nil && s.deps.Log != nil {
		s.deps.Log.Error("audit sse event write failed", "err", err, "action", action, "job_id", sub.JobID)
	}
}

// startBusForwarder 订阅 NATS 事件并转发到 SSE Hub。
func (s *Server) startBusForwarder(log *slog.Logger) {
	if s.deps.Bus == nil {
		return
	}
	stop, err := s.deps.Bus.Subscribe("events.>", func(subject string, data []byte) {
		// Scope-change control messages must be handled before the job-event
		// routing below, which would otherwise treat them as a job status frame.
		if subject == sseScopeControlSubject {
			if !sseScopeControlDisabled() {
				s.handleSSEScopeControl(data)
			}
			return
		}
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
	// SSE 流会持续推送任务的成本/进度，必须先做对象级授权。
	if !s.scopedObject(w, r, "job", jobID) {
		return
	}
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

	sub := s.openSSESubscription(w, r, jobID)
	defer s.closeSSESubscription(r, sub)
	// A subscriber must receive the current state immediately. With NATS
	// enabled there may be no future event for an already completed job, and
	// waiting for the heartbeat makes the stream appear broken.
	if snap := s.snapshotFor(r.Context(), jobID); snap != nil {
		if !s.sseFrameAllowed(sub) {
			return
		}
		b, _ := json.Marshal(snap)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}

	hb := time.NewTicker(15 * time.Second)
	defer hb.Stop()
	// P1-B: the deterministic AUTH-007 bound. This tick is independent of the
	// heartbeat and writes no frame, so a valid subscription is silent while an
	// abandoned one is closed within one period plus one DB round trip.
	scopeTick := time.NewTicker(sseScopeRecheckInterval())
	defer scopeTick.Stop()
	var poll *time.Ticker
	if s.deps.Bus == nil || !s.deps.Bus.Enabled() {
		poll = time.NewTicker(2 * time.Second)
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
		case msg := <-sub.ch:
			if !s.sseFrameAllowed(sub) {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			fl.Flush()
		case <-hb.C:
			if !s.sseFrameAllowed(sub) {
				return
			}
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case <-scopeTick.C:
			// Re-read the live session row without writing anything. When the
			// scope moved, sseFrameAllowed force-closes the subscription and this
			// handler returns, so the client observes EOF and the abandoned scope
			// receives no further byte or frame.
			if !s.sseFrameAllowed(sub) {
				return
			}
		case <-tickC(poll):
			if !s.sseFrameAllowed(sub) {
				return
			}
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
//
// AUTH-007 (2026-09-15): this is a request-path publish as well -- the job control
// handlers call it -- so it goes through the same bounded helper as the SSE scope
// control message instead of waiting on context.Background().
func (s *Server) publishEvent(subject string, payload any) {
	s.publishBestEffort(subject, payload)
}
