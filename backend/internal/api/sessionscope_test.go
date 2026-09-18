package api

// Session workspace staleness guard (sprint7 C1-RACE) -- regression suite.
//
// Measured defect under test: a state-changing request authenticated while the session was in
// workspace A could still commit into A after the same session had been switched to B. The
// guard is wired into require/requireAny, so these tests drive those wrappers with an
// injected live-session reader and a recording handler.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/capability"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

const (
	testSessionWorkspace = "33333333-3333-4333-8333-333333333333"
	testOtherWorkspace   = "44444444-4444-4444-8444-444444444444"
)

// stubSessionScope stands in for the single primary-key lookup on the sessions row that
// production runs against PostgreSQL.
type stubSessionScope struct {
	workspace string
	found     bool
	err       error
	calls     int
}

func (s *stubSessionScope) LiveSessionWorkspace(context.Context, string) (string, bool, error) {
	s.calls++
	return s.workspace, s.found, s.err
}

func sessionScopedPrincipal() policy.Principal {
	return policy.Principal{
		IdentityID:     "11111111-1111-4111-8111-111111111111",
		Email:          "writer@fixture.local",
		OrganizationID: "22222222-2222-4222-8222-222222222222",
		WorkspaceID:    testSessionWorkspace,
		SessionID:      "5e5510aa-0000-4000-8000-000000000001",
		Audience:       "user",
		Roles:          []string{"investigator"},
		Permissions:    []string{"case:create", "case:list"},
		Authenticated:  true,
	}
}

func newSessionScopeServer(t *testing.T, stub *stubSessionScope) *Server {
	t.Helper()
	s := &Server{
		deps:         Deps{Store: &store.Store{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		authMode:     auth.ModeSession,
		cfg:          &config.Config{},
		policy:       policy.NewEngine(nil, 0),
		capabilities: capability.NewRegistry(),
	}
	if stub != nil {
		s.sessionScope = stub
	}
	return s
}

// requireWithStub builds the production order -- scopeBodyMiddleware around
// s.require(permission, stub) -- and runs one request through it. The gate is part of the chain
// on purpose: without it the guard refuses every request that carries a body, which is the
// fail-closed invariant TestScopeBodyGateFailClosedWithoutMarker pins down.
func requireWithStub(t *testing.T, s *Server, permission string, p policy.Principal, method, target string) (*httptest.ResponseRecorder, *recordingHandler) {
	t.Helper()
	stub := &recordingHandler{}
	handler := s.scopeBodyMiddleware(s.require(permission, stub.ServeHTTP))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(withPrincipal(req.Context(), p))
	handler.ServeHTTP(rec, req)
	return rec, stub
}

func requireAnyWithStub(t *testing.T, s *Server, permissions []string, p policy.Principal, method, target string) (*httptest.ResponseRecorder, *recordingHandler) {
	t.Helper()
	stub := &recordingHandler{}
	handler := s.scopeBodyMiddleware(s.requireAny(permissions, stub.ServeHTTP))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(withPrincipal(req.Context(), p))
	handler.ServeHTTP(rec, req)
	return rec, stub
}

// TestSessionWorkspaceGuardThroughRequire is the behaviour table for s.require.
func TestSessionWorkspaceGuardThroughRequire(t *testing.T) {
	cases := []struct {
		name             string
		stub             *stubSessionScope
		method           string
		wantStatus       int
		refused          bool
		wantHandlerCalls int
		wantLookups      int
	}{
		{"same_workspace_allowed", &stubSessionScope{workspace: testSessionWorkspace, found: true}, http.MethodPost, http.StatusOK, false, 1, 1},
		{"workspace_switched_refused", &stubSessionScope{workspace: testOtherWorkspace, found: true}, http.MethodPost, http.StatusConflict, true, 0, 1},
		{"session_row_missing_refused", &stubSessionScope{found: false}, http.MethodPost, http.StatusConflict, true, 0, 1},
		{"session_row_revoked_refused", &stubSessionScope{found: false, workspace: testSessionWorkspace}, http.MethodPost, http.StatusConflict, true, 0, 1},
		{"database_error_fails_closed", &stubSessionScope{err: errors.New("pool exhausted")}, http.MethodPost, http.StatusConflict, true, 0, 1},
		{"patch_is_state_changing", &stubSessionScope{workspace: testOtherWorkspace, found: true}, http.MethodPatch, http.StatusConflict, true, 0, 1},
		{"delete_is_state_changing", &stubSessionScope{workspace: testOtherWorkspace, found: true}, http.MethodDelete, http.StatusConflict, true, 0, 1},
		{"get_reads_no_session_row", &stubSessionScope{workspace: testOtherWorkspace, found: true}, http.MethodGet, http.StatusOK, false, 1, 0},
		{"head_reads_no_session_row", &stubSessionScope{workspace: testOtherWorkspace, found: true}, http.MethodHead, http.StatusOK, false, 1, 0},
		{"options_reads_no_session_row", &stubSessionScope{workspace: testOtherWorkspace, found: true}, http.MethodOptions, http.StatusOK, false, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSessionScopeServer(t, tc.stub)
			rec, stub := requireWithStub(t, s, "case:create", sessionScopedPrincipal(), tc.method, "/api/v2/cases")
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s = %d, want %d (%s)", tc.method, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.refused != hasErrorCode(rec.Body.String(), ErrorAUTHZWorkspaceScopeStale) {
				t.Fatalf("%s body = %s, want refused=%v", tc.method, rec.Body.String(), tc.refused)
			}
			if stub.calls != tc.wantHandlerCalls {
				t.Fatalf("%s handler calls = %d, want %d", tc.method, stub.calls, tc.wantHandlerCalls)
			}
			if tc.stub.calls != tc.wantLookups {
				t.Fatalf("%s session-row lookups = %d, want %d", tc.method, tc.stub.calls, tc.wantLookups)
			}
		})
	}
}

// TestSessionWorkspaceGuardThroughRequireAny proves the second entry point is guarded too.
func TestSessionWorkspaceGuardThroughRequireAny(t *testing.T) {
	cases := []struct {
		name             string
		stub             *stubSessionScope
		method           string
		wantStatus       int
		wantHandlerCalls int
	}{
		{"same_workspace_allowed", &stubSessionScope{workspace: testSessionWorkspace, found: true}, http.MethodPost, http.StatusOK, 1},
		{"workspace_switched_refused", &stubSessionScope{workspace: testOtherWorkspace, found: true}, http.MethodPost, http.StatusConflict, 0},
		{"session_row_missing_refused", &stubSessionScope{found: false}, http.MethodPost, http.StatusConflict, 0},
		{"database_error_fails_closed", &stubSessionScope{err: errors.New("timeout")}, http.MethodPost, http.StatusConflict, 0},
		{"get_reads_no_session_row", &stubSessionScope{workspace: testOtherWorkspace, found: true}, http.MethodGet, http.StatusOK, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSessionScopeServer(t, tc.stub)
			perms := []string{"case:list", "case:create"}
			rec, stub := requireAnyWithStub(t, s, perms, sessionScopedPrincipal(), tc.method, "/api/v2/cases")
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s = %d, want %d (%s)", tc.method, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if stub.calls != tc.wantHandlerCalls {
				t.Fatalf("%s handler calls = %d, want %d", tc.method, stub.calls, tc.wantHandlerCalls)
			}
		})
	}
}

// TestSessionWorkspaceGuardFailsClosedWithoutStore: a server that cannot read the session row
// must refuse, never allow.
func TestSessionWorkspaceGuardFailsClosedWithoutStore(t *testing.T) {
	s := newSessionScopeServer(t, nil)
	rec, stub := requireWithStub(t, s, "case:create", sessionScopedPrincipal(), http.MethodPost, "/api/v2/cases")
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.String(), ErrorAUTHZWorkspaceScopeStale) {
		t.Fatalf("write without a usable store = %d %s, want 409 %s", rec.Code, rec.Body.String(), ErrorAUTHZWorkspaceScopeStale)
	}
	if stub.calls != 0 {
		t.Fatal("the handler ran although the session scope could not be verified")
	}
}

// TestSessionWorkspaceGuardSkipsNonSessionPrincipals: system/compat principals, organisation
// level principals (no workspace) and principals without a session are not session-scoped
// writes and must not pay for a lookup.
func TestSessionWorkspaceGuardSkipsNonSessionPrincipals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(p *policy.Principal)
	}{
		{"system_principal", func(p *policy.Principal) { p.System = true }},
		{"no_session_id", func(p *policy.Principal) { p.SessionID = "" }},
		{"organisation_level_principal", func(p *policy.Principal) { p.WorkspaceID = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubSessionScope{workspace: testOtherWorkspace, found: true}
			s := newSessionScopeServer(t, stub)
			p := sessionScopedPrincipal()
			tc.mutate(&p)
			rec, handler := requireWithStub(t, s, "case:create", p, http.MethodPost, "/api/v2/cases")
			if rec.Code != http.StatusOK || handler.calls != 1 {
				t.Fatalf("POST = %d (handler calls %d), want 200/1", rec.Code, handler.calls)
			}
			if stub.calls != 0 {
				t.Fatalf("session-row lookups = %d, want 0", stub.calls)
			}
		})
	}
}

// TestSessionWorkspaceStaleRefusalIsAudited: the refusal leaves an explicit deny row with the
// route and the request id.
func TestSessionWorkspaceStaleRefusalIsAudited(t *testing.T) {
	s := newSessionScopeServer(t, &stubSessionScope{workspace: testOtherWorkspace, found: true})
	sink := &recordingAuditSink{}
	s.auditSink = sink

	stub := &recordingHandler{}
	handler := s.scopeBodyMiddleware(s.require("case:create", stub.ServeHTTP))
	req := httptest.NewRequest(http.MethodPost, "/api/v2/cases", nil)
	ctx := withPrincipal(req.Context(), sessionScopedPrincipal())
	ctx = withRequestMeta(ctx, RequestMeta{RequestID: "req-scope-audit", TraceID: "trace-scope-audit"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Action != "authorization.denied" || ev.Category != "authorization" {
		t.Fatalf("audit action/category = %s/%s", ev.Action, ev.Category)
	}
	if ev.ErrorCode != ErrorAUTHZWorkspaceScopeStale || ev.DenyReason != "session_workspace_changed" {
		t.Fatalf("audit code/reason = %s/%s", ev.ErrorCode, ev.DenyReason)
	}
	if ev.Route != "/api/v2/cases" || ev.RequestID != "req-scope-audit" || ev.HTTPStatus != http.StatusConflict {
		t.Fatalf("audit row lost route/request id/status: %+v", ev)
	}
}

// checkSessionWorkspaceGuardWired reports whether require and requireAny call the guard.
func checkSessionWorkspaceGuardWired(source string) error {
	for _, fn := range []string{"func (s *Server) require(", "func (s *Server) requireAny("} {
		start := strings.Index(source, fn)
		if start < 0 {
			return errors.New("entry point not found in authmw.go: " + fn)
		}
		body := source[start:]
		if end := strings.Index(body[1:], "\nfunc "); end > 0 {
			body = body[:end+1]
		}
		if !strings.Contains(body, "s.guardSessionWorkspace(w, r,") {
			return errors.New("entry point does not call guardSessionWorkspace: " + fn)
		}
	}
	return nil
}

// TestSessionWorkspaceGuardWiringCalibration is the NEGATIVE CALIBRATION for this guard: it
// fails if require/requireAny stop calling it, and it proves the detector detects by feeding
// it a copy of the source with the calls deliberately removed.
func TestSessionWorkspaceGuardWiringCalibration(t *testing.T) {
	raw, err := os.ReadFile("authmw.go")
	if err != nil {
		t.Fatalf("read authmw.go: %v", err)
	}
	source := string(raw)
	if err := checkSessionWorkspaceGuardWired(source); err != nil {
		t.Fatalf("the session workspace guard is NOT wired into the authorization layer: %v", err)
	}

	disabled := strings.ReplaceAll(source, "s.guardSessionWorkspace(w, r,", "s.guardSessionWorkspaceRemoved(w, r,")
	if disabled == source {
		t.Fatal("calibration setup failed: no guard call sites were found in authmw.go")
	}
	if err := checkSessionWorkspaceGuardWired(disabled); err == nil {
		t.Fatal("NEGATIVE CALIBRATION FAILED: require/requireAny without the guard call were accepted")
	}
}
