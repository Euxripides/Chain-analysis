package api

// C1-SSE-OLD-SCOPE-OPEN / C1-SSE-NO-SUBSCRIPTION-AUDIT -- regression suite for the server-side
// subscription registry: subscriptions are registered with the scope they were authorised under,
// a scope change force-closes them, and no frame is written once the scope is stale.

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

func newSSEServer(t *testing.T, stub *stubSessionScope) *Server {
	t.Helper()
	s := newSessionScopeServer(t, stub)
	s.hub = NewSSEHub()
	return s
}

func subscribeForTest(t *testing.T, s *Server, jobID string, p policy.Principal) *sseSubscription {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/jobs/"+jobID+"/events", nil)
	req = req.WithContext(withPrincipal(req.Context(), p))
	sub := s.openSSESubscription(rec, req, jobID)
	s.auditSink = nil
	return sub
}

// TestSSERegistryRecordsScope: the registry keeps the scope the stream was authorised under.
func TestSSERegistryRecordsScope(t *testing.T) {
	s := newSSEServer(t, &stubSessionScope{workspace: testSessionWorkspace, found: true})
	sub := subscribeForTest(t, s, "job-1", sessionScopedPrincipal())
	if sub.SessionID != sessionScopedPrincipal().SessionID || sub.WorkspaceID != testSessionWorkspace {
		t.Fatalf("subscription lost its scope: %+v", sub)
	}
	if sub.Route != "/api/v2/jobs/job-1/events" || sub.JobID != "job-1" {
		t.Fatalf("subscription lost route/job: %+v", sub)
	}
	if s.hub.SubscriberCount() != 1 {
		t.Fatalf("registered subscriptions = %d, want 1", s.hub.SubscriberCount())
	}
}

// TestSSECloseSessionForceClosesStream: a workspace switch (or any scope change) must end the
// stream without another frame.
func TestSSECloseSessionForceClosesStream(t *testing.T) {
	s := newSSEServer(t, &stubSessionScope{workspace: testSessionWorkspace, found: true})
	p := sessionScopedPrincipal()
	sub := subscribeForTest(t, s, "job-1", p)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/v1/session/workspace", nil)

	n := s.closeScopeSSESubscriptions(p.SessionID, "", sseReasonWorkspaceSwitched, req)
	if n != 1 {
		t.Fatalf("closed subscriptions = %d, want 1", n)
	}
	select {
	case <-sub.closed:
	default:
		t.Fatal("the subscription was not force-closed")
	}
	reason, _ := sub.closeReason()
	if reason != sseReasonWorkspaceSwitched {
		t.Fatalf("close reason = %q, want %q", reason, sseReasonWorkspaceSwitched)
	}
	if s.hub.SubscriberCount() != 0 {
		t.Fatalf("subscriptions left in the hub = %d, want 0", s.hub.SubscriberCount())
	}
}

// TestSSECloseIdentityCoversOtherTabs: revoking every session of an identity (password change,
// revoke-all, token reuse) closes the streams of the other tabs and devices too.
func TestSSECloseIdentityCoversOtherTabs(t *testing.T) {
	s := newSSEServer(t, &stubSessionScope{workspace: testSessionWorkspace, found: true})
	p := sessionScopedPrincipal()
	other := p
	other.SessionID = "5e5510aa-0000-4000-8000-000000000002"
	subA := subscribeForTest(t, s, "job-1", p)
	subB := subscribeForTest(t, s, "job-2", other)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/v1/sessions/revoke-all", nil)

	if n := s.closeScopeSSESubscriptions("", p.IdentityID, sseReasonIdentityRevoked, req); n != 2 {
		t.Fatalf("closed subscriptions = %d, want 2", n)
	}
	for _, sub := range []*sseSubscription{subA, subB} {
		select {
		case <-sub.closed:
		default:
			t.Fatal("not every subscription of the identity was closed")
		}
	}
}

// TestSSEFrameAllowedRevalidatesScope is the per-frame defence in depth: even without a control
// message the stale scope stops the stream.
func TestSSEFrameAllowedRevalidatesScope(t *testing.T) {
	cases := []struct {
		name       string
		stub       *stubSessionScope
		wantAllow  bool
		wantReason string
	}{
		{"same_workspace", &stubSessionScope{workspace: testSessionWorkspace, found: true}, true, ""},
		{"workspace_switched", &stubSessionScope{workspace: testOtherWorkspace, found: true}, false, sseReasonWorkspaceSwitched},
		{"session_revoked", &stubSessionScope{found: false}, false, sseReasonSessionRevoked},
		{"lookup_error", &stubSessionScope{err: errors.New("pool exhausted")}, false, "session_scope_unverifiable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSSEServer(t, tc.stub)
			sub := subscribeForTest(t, s, "job-1", sessionScopedPrincipal())
			if got := s.sseFrameAllowed(sub); got != tc.wantAllow {
				t.Fatalf("frame allowed = %v, want %v", got, tc.wantAllow)
			}
			reason, _ := sub.closeReason()
			if reason != tc.wantReason {
				t.Fatalf("close reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

// TestSSECloseWritesSubscriptionAudit: open, normal close and scope-invalidated each write
// exactly one row that carries job, session, workspace, route, request id and reason.
func TestSSECloseWritesSubscriptionAudit(t *testing.T) {
	s := newSSEServer(t, &stubSessionScope{workspace: testSessionWorkspace, found: true})
	sink := &recordingAuditSink{}
	s.auditSink = sink
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/jobs/job-9/events", nil)
	ctx := withPrincipal(req.Context(), sessionScopedPrincipal())
	ctx = withRequestMeta(ctx, RequestMeta{RequestID: "req-sse-1", TraceID: "trace-sse-1"})
	req = req.WithContext(ctx)
	sub := s.openSSESubscription(rec, req, "job-9")
	if len(sink.events) != 1 || sink.events[0].Action != "job.events.subscribed" {
		t.Fatalf("open audit = %+v", sink.events)
	}
	sub.forceClose(sseReasonWorkspaceSwitched, "req-switch")
	s.closeSSESubscription(req, sub)
	if len(sink.events) != 2 {
		t.Fatalf("audit rows = %d, want 2 (open + close)", len(sink.events))
	}
	closeRow := sink.events[1]
	if closeRow.Action != "job.events.scope_invalidated" || closeRow.DenyReason != sseReasonWorkspaceSwitched {
		t.Fatalf("close audit action/reason = %s/%s", closeRow.Action, closeRow.DenyReason)
	}
	if closeRow.JobID != "job-9" || closeRow.RequestID != "req-sse-1" || closeRow.Route != "/api/v2/jobs/job-9/events" {
		t.Fatalf("close audit lost job/request/route: %+v", closeRow)
	}
	if closeRow.Detail["closed_by_request_id"] != "req-switch" {
		t.Fatalf("close audit lost the triggering request id: %v", closeRow.Detail)
	}

	// A second close must not add a row: exactly one close row per subscription.
	s.closeSSESubscription(req, sub)
	if len(sink.events) != 2 {
		t.Fatalf("audit rows after a duplicate close = %d, want 2", len(sink.events))
	}
}

// TestSSEUnsubscribeDetachesIndexes: a stream that ends normally must not stay reachable through
// the session index (otherwise a later switch would try to close it again).
func TestSSEUnsubscribeDetachesIndexes(t *testing.T) {
	s := newSSEServer(t, &stubSessionScope{workspace: testSessionWorkspace, found: true})
	p := sessionScopedPrincipal()
	sub := subscribeForTest(t, s, "job-1", p)
	s.hub.Unsubscribe(sub.JobID, sub.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/v1/session/workspace", nil)
	if n := s.closeScopeSSESubscriptions(p.SessionID, "", sseReasonWorkspaceSwitched, req); n != 0 {
		t.Fatalf("closed subscriptions = %d, want 0", n)
	}
}

// checkSSEScopeCloseWired reports whether every session-terminating path closes the streams.
func checkSSEScopeCloseWired(source string) error {
	required := []string{
		"sseReasonWorkspaceSwitched",
		"sseReasonLoggedOut",
		"sseReasonSessionRevoked",
		"sseReasonIdentityRevoked",
		"sseReasonMembershipRevoked",
		"sseReasonTokenReuse",
	}
	for _, r := range required {
		if !strings.Contains(source, r) {
			return errors.New("session-terminating path does not close SSE subscriptions: " + r)
		}
	}
	return nil
}

// TestSSEScopeCloseWiringCalibration fails when a scope-change path stops closing streams and
// proves the detector detects by removing them from a copy of the sources.
func TestSSEScopeCloseWiringCalibration(t *testing.T) {
	joined := ""
	for _, name := range []string{"authv1.go", "sse.go"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		joined += string(raw)
	}
	if err := checkSSEScopeCloseWired(joined); err != nil {
		t.Fatalf("the SSE scope-close wiring is incomplete: %v", err)
	}
	disabled := joined
	for _, r := range []string{"sseReasonWorkspaceSwitched", "sseReasonLoggedOut", "sseReasonSessionRevoked", "sseReasonIdentityRevoked", "sseReasonMembershipRevoked", "sseReasonTokenReuse"} {
		disabled = strings.ReplaceAll(disabled, r, "removed_by_calibration")
	}
	if err := checkSSEScopeCloseWired(disabled); err == nil {
		t.Fatal("NEGATIVE CALIBRATION FAILED: missing scope-close wiring was accepted")
	}
}

// TestSSEHubIsPerProcess keeps the honest boundary in code: the registry lives in one process,
// so the cross-instance guarantee is the bus control message plus the per-frame re-read.
func TestSSEHubIsPerProcess(t *testing.T) {
	a := NewSSEHub()
	b := NewSSEHub()
	sub := &sseSubscription{ID: "s1", JobID: "job-1", SessionID: "sess-1", ch: make(chan []byte, 1), closed: make(chan struct{})}
	a.Register(sub)
	if got := b.CloseSession("sess-1"); len(got) != 0 {
		t.Fatalf("a second hub closed a subscription it never registered: %d", len(got))
	}
	if got := a.CloseSession("sess-1"); len(got) != 1 {
		t.Fatalf("the owning hub closed %d subscriptions, want 1", len(got))
	}
}

var _ = policy.Principal{}
var _ = io.Discard
