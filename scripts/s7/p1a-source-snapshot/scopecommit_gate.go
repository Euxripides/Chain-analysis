package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// ---------------------------------------------------------------------------
// P1-A, third layer: the commit gate.
//
// scopeBodyMiddleware closed the "body still in flight" window, and
// guardSessionWorkspace decides after the last byte of the body has arrived.
// Both of them still decide BEFORE the handler runs. The window that is left is
// the one this file closes: the authorization decision has been taken, the
// handler has not committed yet, and a scope mutation (workspace switch, logout,
// revoke-all, forced logout, password change) lands inside that window.
//
// That window is not hypothetical. c1-scope-commit-gate-v3.sh drives it with a
// calibration build that delays /api/v2/cases at exactly this point and measured
// the frozen implementation committing a case into the abandoned workspace
// 5 seconds AFTER the session had already moved (and, in the logout case, 5
// seconds AFTER the session had been revoked). Both rounds are on record.
//
// The gate is enforced by PostgreSQL, not by Go control flow: for a
// session-scoped state-changing request it opens a transaction on a dedicated
// pool, takes a row lock on the caller own session row (SELECT ... FOR UPDATE)
// and re-reads the scope. Every scope mutation in this product is an UPDATE of
// the sessions row -- iam.SessionManager.RebindWorkspace, RevokeByID,
// RevokeAllForIdentity, RevokeDevice, RevokeFamily and the token rotation, plus
// the password-change revoke in iam.Service -- so every one of them has to queue
// behind that lock.
//
// The critical section therefore spans
//
//	re-check -> handler -> business-write commit -> COMMIT (lock release)
//
// and a scope mutation that overlaps a guarded request has exactly two possible
// outcomes:
//
//	the mutation committed first -> the re-check observes it and the request is
//	    refused with 409 AUTHZ_WORKSPACE_SCOPE_STALE; no business object is
//	    created, and a synchronous deny audit row is written;
//	the mutation arrives later  -> it blocks on the lock and is ordered strictly
//	    after the committed write (the audit seq of the write is lower than the
//	    audit seq of the mutation), which is the linearizable outcome.
//
// There is no third outcome, and in particular no outcome in which a business
// object commits under a scope that had already been switched away or revoked.
//
// Honest boundary: this is a lock-scoped critical section, not a literal
// "handler writes join the gate transaction". Handlers use s.deps.Store.Pool
// directly (325 call sites, no shared transaction seam), so the invariant is
// enforced by mutual exclusion with every session-row mutation instead. The
// coverage this buys is mechanical and total: scripts/s7/scope_commit_coverage.py
// enumerates all 163 state-changing route registrations, of which 144 session
// permission-guarded writes take the gate, 1 is excluded on record and 18 are
// the auth self-service face and the bearer API-key surface that carry no session
// workspace at all.
// ---------------------------------------------------------------------------

const (
	// scopeCommitGateMaxConns bounds the dedicated gate pool. It is deliberately
	// small and separate from store.Store.Pool: a gated request holds one gate
	// connection for the duration of its handler, and if that connection came
	// from the same pool the handler uses, N concurrent gated requests could hold
	// all N connections while waiting for handlers that need one more.
	scopeCommitGateMaxConns = 8
	// scopeCommitGateAcquire bounds how long a request waits for a gate
	// connection. A timeout is a fail-closed 503, never an allow.
	scopeCommitGateAcquire = 10 * time.Second
	// scopeCommitGateLockWaitMS bounds the row-lock wait. Reaching it means the
	// caller session row is locked for longer than a scope mutation or a
	// concurrent gated write should ever take; the request fails closed instead of
	// queueing without bound.
	scopeCommitGateLockWaitMS = 8000
)

// scopeCommitGateSessionLockQuery is the commit-time scope re-check. FOR UPDATE
// is the lock every scope mutation has to queue behind; revoked_at IS NULL is the
// session-revocation half of the same check.
const scopeCommitGateSessionLockQuery = `SELECT workspace_id::text
FROM sessions
WHERE id = $1::uuid AND revoked_at IS NULL
FOR UPDATE`

var errScopeCommitGateNoDSN = errors.New("scope commit gate: no database url configured")

// scopeCommitGate holds the dedicated pool. It is created lazily on the first
// guarded request so that a server without a database (unit tests) never opens
// one.
type scopeCommitGate struct {
	once sync.Once
	pool *pgxpool.Pool
	err  error
}

// scopeCommitGateExcludedPermissions lists the guarded routes whose handler
// mutates the caller own session row. They must NOT take the row lock: the
// handler UPDATE would queue behind the lock this same request holds. They are
// enumerated by permission key so that
// scripts/s7/scope_commit_coverage.py prints every exclusion with its reason,
// and TestScopeCommitGateExclusionListIsEnumerated asserts that the list matches
// the routes that really revoke sessions.
//
// Excluding them does not reopen the hole: these routes do not create business
// objects, they revoke sessions. The pre-handler guard still applies to them, and
// a session that is revoked or switched by one of them is observed by every later
// guarded request.
var scopeCommitGateExcludedPermissions = map[string]string{
	"admin:session:revoke": "revokes the session row named in the path; a self-targeted call would queue behind the gate own row lock",
}

// scopeCommitGateApplies reports whether a request takes the commit gate.
func scopeCommitGateApplies(r *http.Request, p policy.Principal, permission string) bool {
	if !needsSessionWorkspaceCheck(r, p) {
		return false
	}
	if _, excluded := scopeCommitGateExcludedPermissions[permission]; excluded {
		return false
	}
	return true
}

// scopeCommitGatePool returns the dedicated pool, creating it once.
func (s *Server) scopeCommitGatePool(ctx context.Context) (*pgxpool.Pool, error) {
	s.scopeGate.once.Do(func() {
		url := ""
		role := ""
		if s.deps.Cfg != nil {
			url = strings.TrimSpace(s.deps.Cfg.DatabaseURL)
			role = strings.TrimSpace(s.deps.Cfg.DatabaseRuntimeRole)
		}
		if url == "" {
			s.scopeGate.err = errScopeCommitGateNoDSN
			return
		}
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			s.scopeGate.err = fmt.Errorf("scope commit gate pool parse: %w", err)
			return
		}
		cfg.MaxConns = scopeCommitGateMaxConns
		cfg.MinConns = 0
		cfg.MaxConnIdleTime = 5 * time.Minute
		if role != "" {
			// Same runtime-role discipline as store.NewWithRuntimeRole: the gate
			// must not run with more privilege than the rest of the API.
			safe := pgx.Identifier{role}.Sanitize()
			cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
				_, err := conn.Exec(ctx, "SET ROLE "+safe)
				return err
			}
		}
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			s.scopeGate.err = fmt.Errorf("scope commit gate pool: %w", err)
			return
		}
		s.scopeGate.pool = pool
	})
	return s.scopeGate.pool, s.scopeGate.err
}

// enterScopeCommitGate acquires the commit-boundary critical section for one
// request. It returns (release, true) when the caller may run the handler, and
// (nil, false) after it has already written the refusal response.
func (s *Server) enterScopeCommitGate(w http.ResponseWriter, r *http.Request, p policy.Principal, permission string) (func(), bool) {
	if !scopeCommitGateApplies(r, p, permission) {
		return func() {}, true
	}
	// Unit tests inject a sessionScope stub and have no pool; the stub seam is the
	// only path that skips the gate, and it exists only in tests.
	if s.sessionScope != nil {
		return func() {}, true
	}
	ctx, cancel := context.WithTimeout(r.Context(), scopeCommitGateAcquire)
	defer cancel()

	pool, err := s.scopeCommitGatePool(ctx)
	if err != nil {
		if s.deps.Log != nil {
			s.deps.Log.Error("scope commit gate unavailable; refusing the write", "err", err)
		}
		writeErrorCode(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY",
			"The request could not be authorised at commit time; retry shortly.", nil)
		return nil, false
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		if s.deps.Log != nil {
			s.deps.Log.Error("scope commit gate acquire failed; refusing the write", "err", err)
		}
		writeErrorCode(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY",
			"The request could not be authorised at commit time; retry shortly.", nil)
		return nil, false
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		conn.Release()
		if s.deps.Log != nil {
			s.deps.Log.Error("scope commit gate begin failed; refusing the write", "err", err)
		}
		writeErrorCode(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY",
			"The request could not be authorised at commit time; retry shortly.", nil)
		return nil, false
	}
	abort := func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		conn.Release()
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", scopeCommitGateLockWaitMS)); err != nil {
		abort()
		if s.deps.Log != nil {
			s.deps.Log.Error("scope commit gate lock_timeout could not be set; refusing the write", "err", err)
		}
		writeErrorCode(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY",
			"The request could not be authorised at commit time; retry shortly.", nil)
		return nil, false
	}

	var current *string
	err = tx.QueryRow(ctx, scopeCommitGateSessionLockQuery, p.SessionID).Scan(&current)
	reason := ""
	detail := map[string]any{"old_workspace_id": p.WorkspaceID, "gate": "commit"}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		reason = "session_missing"
	case err != nil:
		reason = "session_scope_unverifiable"
	case current == nil || *current == "":
		reason = "session_missing"
	case *current != p.WorkspaceID:
		reason = "session_workspace_changed"
	}
	if reason == "" {
		release := func() {
			_ = tx.Commit(context.WithoutCancel(ctx))
			conn.Release()
		}
		return release, true
	}
	abort()
	if current != nil && *current != "" {
		detail["new_workspace_id"] = *current
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		detail["gate_error"] = err.Error()
		if s.deps.Log != nil {
			s.deps.Log.Error("scope commit gate could not read the session row; failing closed",
				"err", err, "session_id", p.SessionID, "route", r.URL.Path)
		}
	}
	writeErrorCode(w, r, http.StatusConflict, ErrorAUTHZWorkspaceScopeStale, workspaceScopeStaleMessage, nil)
	s.auditDenyCodeDetail(r, p, permission, reason, ErrorAUTHZWorkspaceScopeStale, http.StatusConflict, detail)
	return nil, false
}
