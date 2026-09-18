package api

// C1-SCOPE-RACE-STALE -- late scope gate regression suite.
//
// The guard itself lives in authmw.go and is covered by sessionscope_test.go. These tests cover
// the half that makes the guard meaningful: the body of a session-scoped state-changing request
// must have been received BEFORE the scope decision runs, otherwise the decision compares the
// snapshot with the row it was just derived from and the mid-upload window stays open.

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

// flippingBody flips the injected session workspace to another workspace part way through the
// body, reproducing "the session was switched while the request body was still uploading".
type flippingBody struct {
	stub    *stubSessionScope
	target  string
	payload []byte
	off     int
	flipped bool
	readErr error
	closed  bool
}

func (b *flippingBody) Read(p []byte) (int, error) {
	if !b.flipped {
		b.flipped = true
		b.stub.workspace = b.target
	}
	if b.off >= len(b.payload) {
		if b.readErr != nil {
			return 0, b.readErr
		}
		return 0, io.EOF
	}
	n := copy(p, b.payload[b.off:])
	b.off += n
	return n, nil
}

func (b *flippingBody) Close() error {
	b.closed = true
	return nil
}

// gatedChain runs one request through the real order -- scopeBodyMiddleware, then require --
// which is what the router does in production.
func gatedChain(s *Server, permission string, p policy.Principal, method, target string, body io.Reader) (*httptest.ResponseRecorder, *recordingHandler) {
	stub := &recordingHandler{}
	handler := s.scopeBodyMiddleware(s.require(permission, stub.ServeHTTP))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, body)
	req = req.WithContext(withPrincipal(req.Context(), p))
	handler.ServeHTTP(rec, req)
	return rec, stub
}

// TestScopeBodyGateClosesMidUploadScopeChange is the behavioural proof of the fix: the workspace
// is switched while the body is still being read, and the request must be refused with 409
// before the handler can commit anything.
func TestScopeBodyGateClosesMidUploadScopeChange(t *testing.T) {
	stub := &stubSessionScope{workspace: testSessionWorkspace, found: true}
	s := newSessionScopeServer(t, stub)
	s.hub = NewSSEHub()
	s.auditSink = &recordingAuditSink{}
	payload := []byte(`{"case_no":"RACE","name":"RACE"}`)
	body := &flippingBody{stub: stub, target: testOtherWorkspace, payload: payload}

	rec, handler := gatedChain(s, "case:create", sessionScopedPrincipal(), http.MethodPost, "/api/v2/cases", body)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if !hasErrorCode(rec.Body.String(), ErrorAUTHZWorkspaceScopeStale) {
		t.Fatalf("body = %s, want %s", rec.Body.String(), ErrorAUTHZWorkspaceScopeStale)
	}
	if handler.calls != 0 {
		t.Fatalf("handler ran %d time(s): the object would have been created in the abandoned workspace", handler.calls)
	}
	if !body.flipped {
		t.Fatal("the fixture never flipped the workspace, so the race window was not exercised")
	}
	if stub.calls != 1 {
		t.Fatalf("session-row lookups = %d, want exactly 1", stub.calls)
	}
	sink, _ := s.auditSink.(*recordingAuditSink)
	if sink == nil || len(sink.events) != 1 {
		t.Fatalf("audit rows = %v, want exactly 1", sink)
	}
	if sink.events[0].DenyReason != "session_workspace_changed" {
		t.Fatalf("audit reason = %s, want session_workspace_changed", sink.events[0].DenyReason)
	}
	if sink.events[0].Detail["old_workspace_id"] != testSessionWorkspace ||
		sink.events[0].Detail["new_workspace_id"] != testOtherWorkspace {
		t.Fatalf("audit detail lost the old/new scope: %v", sink.events[0].Detail)
	}
}

// TestScopeBodyGateReplaysBodyIntact: the gate must not change what the handler reads.
func TestScopeBodyGateReplaysBodyIntact(t *testing.T) {
	stub := &stubSessionScope{workspace: testSessionWorkspace, found: true}
	s := newSessionScopeServer(t, stub)
	payload := strings.Repeat("x", 200000)
	var seen string
	handler := s.scopeBodyMiddleware(s.require("case:create", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("handler could not read the replayed body: %v", err)
		}
		seen = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/cases", strings.NewReader(payload))
	req = req.WithContext(withPrincipal(req.Context(), sessionScopedPrincipal()))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if seen != payload {
		t.Fatalf("handler saw %d bytes, want %d", len(seen), len(payload))
	}
}

// TestScopeBodyGateRefusesOversizedBody: a body above the bound is refused before any business
// logic, so it cannot be used to reach a handler.
func TestScopeBodyGateRefusesOversizedBody(t *testing.T) {
	stub := &stubSessionScope{workspace: testSessionWorkspace, found: true}
	s := newSessionScopeServer(t, stub)
	big := strings.NewReader(strings.Repeat("y", maxScopeGatedBodyBytes+16))
	rec, handler := gatedChain(s, "case:create", sessionScopedPrincipal(), http.MethodPost, "/api/v2/cases", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (%s)", rec.Code, rec.Body.String())
	}
	if handler.calls != 0 {
		t.Fatal("the handler ran for an oversized body")
	}
	if stub.calls != 0 {
		t.Fatalf("session-row lookups = %d, want 0 (the request never reached the guard)", stub.calls)
	}
}

// TestScopeBodyGateRefusesUnreadableBody: a client that disconnects in the middle of the body
// must not reach a handler.
func TestScopeBodyGateRefusesUnreadableBody(t *testing.T) {
	stub := &stubSessionScope{workspace: testSessionWorkspace, found: true}
	s := newSessionScopeServer(t, stub)
	body := &flippingBody{stub: stub, target: testSessionWorkspace, payload: []byte("{}"), readErr: errors.New("client disconnected")}
	rec, handler := gatedChain(s, "case:create", sessionScopedPrincipal(), http.MethodPost, "/api/v2/cases", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if handler.calls != 0 {
		t.Fatal("the handler ran although the body could not be read in full")
	}
	if !body.closed {
		t.Fatal("the gate did not close the body it consumed")
	}
}

// TestScopeBodyGateFailClosedWithoutMarker is the calibration for the ordering invariant: a
// request that carries a body but never passed the gate must be refused, so the pre-fix order
// (decide first, read the body afterwards) can never come back silently.
func TestScopeBodyGateFailClosedWithoutMarker(t *testing.T) {
	stub := &stubSessionScope{workspace: testSessionWorkspace, found: true}
	s := newSessionScopeServer(t, stub)
	s.auditSink = &recordingAuditSink{}
	handler := s.require("case:create", (&recordingHandler{}).ServeHTTP)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/cases", strings.NewReader(`{"case_no":"X"}`))
	req = req.WithContext(withPrincipal(req.Context(), sessionScopedPrincipal()))
	handler(rec, req)
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.String(), ErrorAUTHZWorkspaceScopeStale) {
		t.Fatalf("ungated write = %d %s, want 409 %s", rec.Code, rec.Body.String(), ErrorAUTHZWorkspaceScopeStale)
	}
	if stub.calls != 0 {
		t.Fatalf("session-row lookups = %d, want 0: the guard must not even try when the body is unread", stub.calls)
	}
	sink, _ := s.auditSink.(*recordingAuditSink)
	if sink == nil || len(sink.events) != 1 || sink.events[0].DenyReason != "request_body_not_fully_received" {
		t.Fatalf("audit rows = %v, want one row with reason request_body_not_fully_received", sink)
	}
}

// TestScopeBodyGateSkipsNonSessionPrincipals: principals the guard does not apply to must keep
// their body untouched and must not pay for a read or a lookup.
func TestScopeBodyGateSkipsNonSessionPrincipals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(p *policy.Principal)
	}{
		{"system_principal", func(p *policy.Principal) { p.System = true }},
		{"no_session_id", func(p *policy.Principal) { p.SessionID = "" }},
		{"no_workspace", func(p *policy.Principal) { p.WorkspaceID = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubSessionScope{workspace: testOtherWorkspace, found: true}
			s := newSessionScopeServer(t, stub)
			body := &flippingBody{stub: stub, target: testOtherWorkspace, payload: []byte("{}")}
			p := sessionScopedPrincipal()
			tc.mutate(&p)
			rec, handler := gatedChain(s, "case:create", p, http.MethodPost, "/api/v2/cases", body)
			if rec.Code != http.StatusOK || handler.calls != 1 {
				t.Fatalf("status = %d handler calls = %d, want 200/1", rec.Code, handler.calls)
			}
			if body.off != 0 {
				t.Fatal("the gate consumed the body of a request it does not guard")
			}
			if stub.calls != 0 {
				t.Fatalf("session-row lookups = %d, want 0", stub.calls)
			}
		})
	}
}

// TestScopeBodyGateSkipsSafeMethods: reads are never buffered and never looked up.
func TestScopeBodyGateSkipsSafeMethods(t *testing.T) {
	stub := &stubSessionScope{workspace: testOtherWorkspace, found: true}
	s := newSessionScopeServer(t, stub)
	body := &flippingBody{stub: stub, target: testOtherWorkspace, payload: []byte("{}")}
	rec, handler := gatedChain(s, "case:list", sessionScopedPrincipal(), http.MethodGet, "/api/v2/cases", body)
	if rec.Code != http.StatusOK || handler.calls != 1 {
		t.Fatalf("GET status = %d handler calls = %d, want 200/1", rec.Code, handler.calls)
	}
	if body.off != 0 || stub.calls != 0 {
		t.Fatalf("safe method was gated: body_off=%d lookups=%d", body.off, stub.calls)
	}
}

// checkScopeBodyMiddlewareWired reports whether the router registers the gate.
func checkScopeBodyMiddlewareWired(source string) error {
	if !strings.Contains(source, "r.Use(s.scopeBodyMiddleware)") {
		return errors.New("buildRouter does not register s.scopeBodyMiddleware")
	}
	return nil
}

// TestScopeBodyMiddlewareWiringCalibration is the negative calibration for the registration: it
// fails when the gate is unregistered and proves the detector detects by feeding it a copy of
// server.go with the registration removed.
func TestScopeBodyMiddlewareWiringCalibration(t *testing.T) {
	raw, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	source := string(raw)
	if err := checkScopeBodyMiddlewareWired(source); err != nil {
		t.Fatalf("the late scope gate is NOT registered in the router: %v", err)
	}
	disabled := strings.ReplaceAll(source, "r.Use(s.scopeBodyMiddleware)", "// removed by calibration")
	if disabled == source {
		t.Fatal("calibration setup failed: no registration site found in server.go")
	}
	if err := checkScopeBodyMiddlewareWired(disabled); err == nil {
		t.Fatal("NEGATIVE CALIBRATION FAILED: an unregistered gate was accepted")
	}
}

// TestScopeBodyGateOrderingIsReal proves the fixture moves the session workspace during the body
// read: without that, TestScopeBodyGateClosesMidUploadScopeChange would prove nothing.
func TestScopeBodyGateOrderingIsReal(t *testing.T) {
	stub := &stubSessionScope{workspace: testSessionWorkspace, found: true}
	body := &flippingBody{stub: stub, target: testOtherWorkspace, payload: []byte("{}")}
	_, _ = io.ReadAll(body)
	if stub.workspace != testOtherWorkspace {
		t.Fatal("the fixture does not actually move the session workspace during the body read")
	}
}
