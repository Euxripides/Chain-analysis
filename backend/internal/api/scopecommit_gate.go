package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// ---------------------------------------------------------------------------
// P1-A / AUTH-007: the commit gate.
//
// scopeBodyMiddleware closed the "body still in flight" window and
// guardSessionWorkspace decides after the last byte of the body has arrived.
// Both of them still decide BEFORE the handler runs. The window this file closes
// is the one that is left: the authorization decision has been taken, the
// handler has not committed yet, and a scope mutation (workspace switch, logout,
// revoke-all, forced logout, password change) lands inside that window.
//
// The FIRST implementation of this file closed that window with a row lock held
// on the caller session row for the whole handler:
//
//	re-check (FOR UPDATE) -> handler -> business-write commit -> COMMIT
//
// It was correct but it made the handler length part of the critical section, so
// AUTH-007 ("a forced logout takes effect within 5 seconds") was only true while
// every gated handler was far shorter than 5 seconds. scripts/s7/c1-scope-commit-
// latency-gate-v1.sh measured the consequence on a calibration build with a 12 s
// handler: workspace switch 11066 ms, logout 11072 ms, admin revoke 11036 ms --
// 35 PASS / 1 FAIL, and the failure is the AUTH-007 budget itself.
//
// This implementation replaces that lock with an optimistic commit token and a
// real transaction:
//
//	entry    (no lock)   read the scope token of the caller session row and open
//	                     the request transaction
//	handler              every PG write of the request joins that transaction,
//	                     because store.Store.Pool is a dbtx.Pool and the request
//	                     context carries the pgx.Tx (see internal/dbtx)
//	release  (short lock) re-read the row FOR UPDATE and compare the token
//	                     unchanged -> COMMIT and send the handler response
//	                     changed   -> ROLLBACK, drop the buffered response and
//	                     answer 409 AUTHZ_WORKSPACE_SCOPE_STALE
//
// The row lock is now held only between the release re-read and COMMIT, i.e.
// for microseconds, so a 12 s handler no longer delays a scope mutation at all;
// and the business write is committed or discarded as ONE transaction, so no
// partial write can survive a scope change.
//
// The token is sessions.scope_epoch (migration 0059): a trigger bumps it on
// every UPDATE of the session row that changes any column except the two pure
// liveness columns last_seen_at / expires_at. The workspace id and revoked_at
// are compared as well, so the refusal reason stays specific and the check does
// not depend on the epoch alone.
//
// BOUNDED WAITING. The release re-read is the one place where a gated request
// waits for another transaction, and the request context cannot bound it (the
// commit decision has to outlive a client that went away). The body of the
// request transaction therefore runs under explicit server-side bounds, applied
// with SET LOCAL so they die with the transaction:
//
//	lock_timeout                        SCOPE_COMMIT_GATE_LOCK_TIMEOUT_MS       (8 s)
//	statement_timeout                   SCOPE_COMMIT_GATE_STATEMENT_TIMEOUT_MS  (60 s)
//	idle_in_transaction_session_timeout SCOPE_COMMIT_GATE_IDLE_TX_TIMEOUT_MS    (120 s)
//
// A lock timeout is a fail-closed refusal (409 AUTHZ_WORKSPACE_SCOPE_STALE with
// reason session_scope_unverifiable), never an unbounded hang and never a commit
// of a scope that could not be re-read. The idle bound is a leak backstop: a
// transaction abandoned by a cancelled handler cannot hold a row lock forever.
//
// CORRELATION. Every gate transaction sets application_name to
// "bpgate:<request id>", so pg_stat_activity can be joined to the request that
// owns it while that request is still running -- which is the only moment at
// which a lock wait is diagnosable.
//
// Honest boundaries, all asserted by tests rather than assumed:
//
//   - The handler response is buffered until COMMIT so that a rollback can still
//     be reported as 409. Nothing is ever written to the client before the commit
//     decision: past 256 KiB the response continues in a private temporary file
//     (0600, removed on every exit path), and past the hard bound the handler is
//     cancelled and the request is failed closed with a stable 5xx. A success the
//     caller can see while the transaction is being rolled back is not a
//     degradation this code accepts.
//   - Audit writes made by the middleware (emitAudit) never join the transaction:
//     it holds the raw pool and strips the token from the context, so a deny row
//     survives the rollback.
//   - Audit writes made by the HANDLER do join it, and they must: a handler that
//     writes audit in the business transaction (auditTx) and then writes another
//     audit row on a second connection would wait for a lock its own request
//     transaction can only release after the handler returns. Measured on
//     2026-09-16 (D-15): POST /api/v2/evidence/{id}/commands/verify took 60.0 s
//     and dropped its evidence:sensitive:read row, because the in-transaction
//     evidence:review row had taken the audit_chain_heads row lock. audit.Writer
//     therefore joins the request transaction whenever dbtx.IsGateRequest is true.
//     The cost is honest and bounded: the chain head IS held for the remainder of
//     a handler that wrote in-transaction audit, so long handlers must keep their
//     in-transaction audit write as late as possible.
//   - Non-PG side effects (ClickHouse, MinIO, NATS, providers) are not covered
//     by a PG rollback. Routes that reach those dependencies must re-verify the
//     token before performing the side effect; the mechanical list is produced
//     by scripts/s7/scope_commit_coverage_v3.py (44 of the 145 gated routes).
//   - A handler that writes a session row ANOTHER request is using as its own
//     caller row can still form a lock ring with that request's release re-read.
//     PostgreSQL's deadlock detector breaks such a ring in one second; the ring
//     is nevertheless a design defect for the routes that can produce it, so
//     admin:session:revoke (the one route measured doing it) is carved out of the
//     generic gate entirely and owns a dedicated short transaction with a
//     deterministic lock order -- see scopecommit_revoke.go.
// ---------------------------------------------------------------------------

const (
	// scopeCommitGateMaxConns bounds the dedicated gate pool. It is deliberately
	// small and separate from store.Store.Pool. A gated request holds one gate
	// connection for the duration of its handler (the transaction stays open,
	// without holding any row lock), so the two pools must not deadlock.
	scopeCommitGateMaxConns = 8
	// scopeCommitGateAcquire bounds how long a request waits for a gate
	// connection. A timeout is a fail-closed 503, never an allow.
	scopeCommitGateAcquire = 10 * time.Second
	// scopeCommitGateLockWaitMS bounds the release re-read. Reaching it means the
	// caller session row is locked for longer than a scope mutation should ever
	// take; the request fails closed instead of queueing without bound.
	scopeCommitGateLockWaitMS = 8000
	// scopeCommitGateStatementTimeoutDefaultMS bounds any single statement of the
	// request transaction.
	scopeCommitGateStatementTimeoutDefaultMS = 60000
	// scopeCommitGateIdleTxTimeoutDefaultMS bounds a request transaction that
	// stops doing work without ending. It is the backstop against a transaction
	// abandoned by a handler that was cancelled mid-flight.
	scopeCommitGateIdleTxTimeoutDefaultMS = 120000
	// scopeCommitGateSlowDefaultMS is the critical-section warning threshold. The
	// critical section is now the release re-read plus COMMIT, so anything above
	// this is a real anomaly rather than a slow handler.
	scopeCommitGateSlowDefaultMS = 1000
	// scopeCommitGateResponseMaxBytes is the in-memory part of the retractable
	// response buffer. Past it the response continues in a private temporary file
	// instead of being streamed to the client.
	scopeCommitGateResponseMaxBytes = 256 * 1024
	// scopeCommitGateResponseHardMaxBytes is the absolute bound of one buffered
	// response (memory plus spool file). A handler that tries to produce more is
	// cancelled, its transaction is rolled back and the caller gets a stable 5xx.
	scopeCommitGateResponseHardMaxBytes = 32 << 20
	// scopeCommitGateApplicationName is the pg_stat_activity prefix of a gate
	// transaction. It is part of the diagnostic contract: a lock wait that cannot
	// be attributed to a request cannot be diagnosed while it is happening.
	scopeCommitGateApplicationName = "bpgate"
)

// scopeCommitGateSlowMs resolves the warning threshold from SCOPE_COMMIT_GATE_SLOW_MS.
func scopeCommitGateSlowMs() int64 {
	raw := strings.TrimSpace(os.Getenv("SCOPE_COMMIT_GATE_SLOW_MS"))
	if raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return scopeCommitGateSlowDefaultMS
}

// scopeCommitGateResponseMax resolves the response buffer bound.
func scopeCommitGateResponseMax() int {
	raw := strings.TrimSpace(os.Getenv("SCOPE_COMMIT_GATE_RESPONSE_MAX_BYTES"))
	if raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return scopeCommitGateResponseMaxBytes
}

// scopeCommitGateResponseHardMax resolves the absolute response bound.
func scopeCommitGateResponseHardMax() int64 {
	raw := strings.TrimSpace(os.Getenv("SCOPE_COMMIT_GATE_RESPONSE_HARD_MAX_BYTES"))
	if raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return scopeCommitGateResponseHardMaxBytes
}

// scopeCommitGateSpoolDir resolves the directory used for spilled responses.
// An empty value means the operating system temporary directory.
func scopeCommitGateSpoolDir() string {
	return strings.TrimSpace(os.Getenv("SCOPE_COMMIT_GATE_SPOOL_DIR"))
}

// scopeCommitGatePositiveEnvMS reads a millisecond bound from the environment.
func scopeCommitGatePositiveEnvMS(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

// applyScopeTxLocalSettings bounds one request transaction and labels it for
// pg_stat_activity. Every value is passed as a bound parameter, so a request id
// can never become SQL.
func applyScopeTxLocalSettings(ctx context.Context, tx pgx.Tx, requestID string) error {
	lockMS := scopeCommitGatePositiveEnvMS("SCOPE_COMMIT_GATE_LOCK_TIMEOUT_MS", scopeCommitGateLockWaitMS)
	stmtMS := scopeCommitGatePositiveEnvMS("SCOPE_COMMIT_GATE_STATEMENT_TIMEOUT_MS", scopeCommitGateStatementTimeoutDefaultMS)
	idleMS := scopeCommitGatePositiveEnvMS("SCOPE_COMMIT_GATE_IDLE_TX_TIMEOUT_MS", scopeCommitGateIdleTxTimeoutDefaultMS)
	_, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true),
       set_config('statement_timeout', $2, true),
       set_config('idle_in_transaction_session_timeout', $3, true),
       set_config('application_name', $4, true)`,
		strconv.FormatInt(lockMS, 10)+"ms",
		strconv.FormatInt(stmtMS, 10)+"ms",
		strconv.FormatInt(idleMS, 10)+"ms",
		scopeCommitGateLabel(requestID))
	return err
}

// scopeCommitGateLabel builds the application_name of one gate transaction. The
// request id is client-supplied, so it is filtered to an identifier alphabet and
// truncated before it reaches PostgreSQL.
func scopeCommitGateLabel(requestID string) string {
	var b strings.Builder
	for _, c := range requestID {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-', c == '_', c == '.':
			b.WriteRune(c)
		}
		if b.Len() >= 40 {
			break
		}
	}
	if b.Len() == 0 {
		return scopeCommitGateApplicationName
	}
	return scopeCommitGateApplicationName + ":" + b.String()
}

// scopeCommitGateObserve records one gated request: the outcome, how long the
// request waited for the gate and how long the gate then held the resolved
// scope. It carries the permission key and the outcome only -- never a session,
// a user, a route parameter or a secret.
func scopeCommitGateObserve(permission, outcome string, waitMs, criticalMs float64) {
	observability.ScopeCommitGateRequests.WithLabelValues(permission, outcome).Inc()
	observability.ScopeCommitGateWaitMs.WithLabelValues(permission, outcome).Observe(waitMs)
	observability.ScopeCommitGateCriticalMs.WithLabelValues(permission, outcome).Observe(criticalMs)
}

// scopeCommitGateScopeToken is the optimistic commit token: the columns of the
// caller session row that decide whether the authorization decision taken before
// the handler is still valid once the handler has finished.
type scopeCommitGateScopeToken struct {
	workspaceID string
	revoked     bool
	epoch       int64
}

// scopeCommitGateTokenQuery is the ENTRY read: deliberately NOT "FOR UPDATE", so
// a scope mutation never waits for a handler.
const scopeCommitGateTokenQuery = `SELECT workspace_id::text, revoked_at, scope_epoch
FROM sessions
WHERE id = $1::uuid`

// scopeCommitGateSessionLockQuery is the RELEASE re-read. FOR UPDATE is the lock
// every scope mutation has to queue behind, and it is now held for the duration
// of the comparison plus COMMIT only.
const scopeCommitGateSessionLockQuery = `SELECT workspace_id::text, revoked_at, scope_epoch
FROM sessions
WHERE id = $1::uuid
FOR UPDATE`

// errScopeCommitGateScopeStale reports that the caller session scope was already
// invalid when an external dependency was about to be used.
var errScopeCommitGateScopeStale = errors.New("scope commit gate: session scope is no longer live")

// scopeCommitGateRecheck re-reads the caller session scope OUTSIDE the request
// transaction and reports whether it is still live.
//
// The commit gate rolls back PostgreSQL only. A gated route that reaches an
// external dependency (ClickHouse, MinIO, NATS, a provider) BEFORE the commit
// decision still performs that work for a request the platform is about to
// refuse: the P4 matrix measured a refused (409, rolled back) download preview
// charging a 21 177 ms ClickHouse FINAL scan to a scope that had already been
// invalidated (P4-CH-01). Routes that cannot make such a call transactional call
// this immediately before it.
//
// It is read-only and it runs on the raw pool (WithoutTx): the request
// transaction is invisible to other backends and holding it would block the very
// mutation being checked. It is NOT a second enforcement point: the release
// re-read stays the only authority, and this helper only decides whether the
// external work is worth starting. A read error is reported as unavailable so
// the caller skips the external work instead of proceeding on an unverifiable
// scope.
func (s *Server) scopeCommitGateRecheck(ctx context.Context, p policy.Principal) error {
	if s.deps.Store == nil || strings.TrimSpace(p.SessionID) == "" {
		return nil // machine scope (GPT Actions): there is no session row to re-read
	}
	var workspace *string
	var revokedAt *time.Time
	var epoch *int64
	err := s.deps.Store.Pool.QueryRow(dbtx.WithoutTx(ctx), scopeCommitGateTokenQuery, p.SessionID).
		Scan(&workspace, &revokedAt, &epoch)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return errScopeCommitGateScopeStale
	case err != nil:
		return fmt.Errorf("scope recheck unavailable: %w", err)
	}
	if revokedAt != nil {
		return errScopeCommitGateScopeStale
	}
	if workspace == nil || *workspace == "" || *workspace != p.WorkspaceID {
		return errScopeCommitGateScopeStale
	}
	return nil
}

var errScopeCommitGateNoDSN = errors.New("scope commit gate: no database url configured")

// errScopeGateResponseTooLarge is returned by the buffered writer instead of
// forwarding bytes it cannot retract. The gate turns it into a rollback plus a
// stable 5xx.
var errScopeGateResponseTooLarge = errors.New("scope commit gate: handler response exceeded the hard bound")

// scopeCommitGate holds the dedicated pool. It is created lazily on the first
// guarded request so that a server without a database (unit tests) never opens
// one.
type scopeCommitGate struct {
	once sync.Once
	pool *pgxpool.Pool
	err  error
}

// scopeCommitGateSelfTargetPermissions lists the guarded routes whose handler
// mutates the session row named by the {id} route parameter, so that a request
// which names the CALLER OWN session would make the handler UPDATE collide with
// the transaction this same request already holds on its own row. That single
// shape is exempt; it is not a whole-permission exclusion.
//
// The handler of admin:session:revoke calls
// iam.Sessions().RevokeByID(chi.URLParam(r, "id")), i.e. it revokes the session
// NAMED IN THE PATH, which is normally somebody else's session, so the gate
// still applies to every cross-session target.
var scopeCommitGateSelfTargetPermissions = map[string]string{
	"admin:session:revoke": "the handler revokes the session named by the {id} path parameter, so only a call whose target IS the caller own session would collide with the request transaction the gate already holds",
}

// scopeCommitGateDedicatedPermissions lists the guarded routes that own an
// equivalent, dedicated commit-time transaction INSIDE the handler instead of the
// generic request transaction, and must therefore not be wrapped by the gate.
//
// admin:session:revoke is the measured member. Wrapped by the generic gate it
// produced the S3 defect: request 1 locks its target row in the handler and then
// asks for its own caller row at release, while request 2 (whose caller IS
// request 1's target) does the exact mirror image, so the two releases form a
// ring. The route now runs one short transaction that locks actor and target in
// ascending session-id order, re-verifies the actor and the permission inside
// that transaction, updates the target and commits -- see scopecommit_revoke.go.
// The exemption is a relocation of the check, not a removal: the handler refuses
// with the same 409 and the same deny audit when the caller scope is stale.
var scopeCommitGateDedicatedPermissions = map[string]string{
	"admin:session:revoke": "the handler owns a dedicated short transaction with a deterministic lock order and an equivalent commit-time scope and permission re-check; wrapping it in the generic request transaction is what built the S3 lock ring",
}

// scopeCommitGateTargetSessionID identifies the session named by the route path
// BEFORE the handler runs, which is what makes the self-target carve-out above
// provable rather than assumed. An empty result is never treated as
// self-targeted: the gate then applies, which is the fail-closed direction.
func scopeCommitGateTargetSessionID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(chi.URLParam(r, "id"))
}

// scopeCommitGateApplies reports whether a request takes the generic commit gate.
func scopeCommitGateApplies(r *http.Request, p policy.Principal, permission string) bool {
	if !needsSessionWorkspaceCheck(r, p) {
		return false
	}
	if _, dedicated := scopeCommitGateDedicatedPermissions[permission]; dedicated {
		return false
	}
	if _, selfTargetable := scopeCommitGateSelfTargetPermissions[permission]; selfTargetable {
		if target := scopeCommitGateTargetSessionID(r); target != "" && target == p.SessionID {
			return false
		}
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
		if cfg.ConnConfig.RuntimeParams == nil {
			cfg.ConnConfig.RuntimeParams = map[string]string{}
		}
		// Every connection of this pool is a commit-gate connection, so the idle
		// name is unambiguous in pg_stat_activity even before a transaction sets
		// its own label.
		cfg.ConnConfig.RuntimeParams["application_name"] = scopeCommitGateApplicationName + "-idle"
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

// enterScopeCommitGate opens the request transaction, resolves the scope token
// without blocking any scope mutation, and returns the writer/request the
// handler must use plus the release function that commits or rolls back.
//
// It returns ok=false only after it has already written the refusal response.
func (s *Server) enterScopeCommitGate(w http.ResponseWriter, r *http.Request, p policy.Principal, permission string) (http.ResponseWriter, *http.Request, func(), bool) {
	noop := func() {}
	if !scopeCommitGateApplies(r, p, permission) {
		return w, r, noop, true
	}
	// Unit tests inject a sessionScope stub and have no pool; the stub seam is the
	// only path that skips the gate, and it exists only in tests.
	if s.sessionScope != nil {
		return w, r, noop, true
	}
	waitStart := time.Now()
	refuse := func(reason string) {
		scopeCommitGateObserve(permission, "refused_"+reason, float64(time.Since(waitStart).Milliseconds()), 0)
	}
	ctx, cancel := context.WithTimeout(r.Context(), scopeCommitGateAcquire)

	pool, err := s.scopeCommitGatePool(ctx)
	if err != nil {
		cancel()
		if s.deps.Log != nil {
			s.deps.Log.Error("scope commit gate unavailable; refusing the write", "err", err)
		}
		refuse("unavailable")
		writeErrorCode(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY",
			"The request could not be authorised at commit time; retry shortly.", nil)
		return w, r, noop, false
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		cancel()
		if s.deps.Log != nil {
			s.deps.Log.Error("scope commit gate acquire failed; refusing the write", "err", err)
		}
		refuse("acquire_failed")
		writeErrorCode(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY",
			"The request could not be authorised at commit time; retry shortly.", nil)
		return w, r, noop, false
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		cancel()
		conn.Release()
		if s.deps.Log != nil {
			s.deps.Log.Error("scope commit gate begin failed; refusing the write", "err", err)
		}
		refuse("begin_failed")
		writeErrorCode(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY",
			"The request could not be authorised at commit time; retry shortly.", nil)
		return w, r, noop, false
	}
	abort := func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		cancel()
		conn.Release()
	}
	// Bounded, labelled transaction: every wait inside it has a ceiling and every
	// wait is attributable to this request while it is still running.
	if err := applyScopeTxLocalSettings(ctx, tx, metaFrom(r.Context()).RequestID); err != nil {
		if s.deps.Log != nil {
			s.deps.Log.Error("scope commit gate could not bound the request transaction; failing closed", "err", err)
		}
		refuse("set_local_failed")
		abort()
		writeErrorCode(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY",
			"The request could not be authorised at commit time; retry shortly.", nil)
		return w, r, noop, false
	}

	entry, reason, entryErr := scopeCommitGateReadToken(ctx, tx, p, scopeCommitGateTokenQuery, false)
	if reason != "" {
		refuse(reason)
		abort()
		detail := map[string]any{"old_workspace_id": p.WorkspaceID, "gate": "commit"}
		if entryErr != nil && !errors.Is(entryErr, pgx.ErrNoRows) {
			detail["gate_error"] = entryErr.Error()
			if s.deps.Log != nil {
				s.deps.Log.Error("scope commit gate could not read the session row; failing closed",
					"err", entryErr, "session_id", p.SessionID, "route", r.URL.Path)
			}
		}
		if entry.workspaceID != "" && entry.workspaceID != p.WorkspaceID {
			detail["new_workspace_id"] = entry.workspaceID
		}
		writeErrorCode(w, r, http.StatusConflict, ErrorAUTHZWorkspaceScopeStale, workspaceScopeStaleMessage, nil)
		s.auditDenyCodeDetail(scopeCommitGateDetachedRequest(r), p, permission, reason, ErrorAUTHZWorkspaceScopeStale, http.StatusConflict, detail)
		return w, r, noop, false
	}

	// The request transaction is now visible to every store the handler uses. The
	// cancellable child exists so that a response the caller could not un-see can
	// cancel the handler instead of being streamed.
	// dbtx.WithGate marks the context so audit.Writer joins THIS transaction
	// instead of opening a second connection: an in-transaction audit write by the
	// handler already owns the audit_chain_heads row lock, and a second connection
	// would wait for a lock only this request can release (self-deadlock, D-15).
	handlerCtx, handlerCancel := context.WithCancel(
		dbtx.WithGate(dbtx.WithTx(r.Context(), tx), metaFrom(r.Context()).RequestID))
	gateCtx := handlerCtx
	gateReq := r.WithContext(gateCtx)
	gateWriter := newScopeGateWriter(w, handlerCancel)
	waitMs := float64(time.Since(waitStart).Milliseconds())

	release := func() {
		criticalStart := time.Now()
		// The transaction context must outlive the request context: the handler
		// may have returned because the client went away, and the commit decision
		// still has to be taken and recorded.
		commitCtx := context.WithoutCancel(gateCtx)
		var (
			current scopeCommitGateScopeToken
			reason  string
			readErr error
		)
		if gateWriter.responseTooLarge() {
			// The handler was cancelled before it could send anything the caller
			// could not un-see. The transaction is discarded and the caller is told
			// so, instead of being handed a success the database never kept.
			reason = "response_too_large"
		} else {
			current, reason, readErr = scopeCommitGateReadToken(commitCtx, tx, p, scopeCommitGateSessionLockQuery, true)
			if reason == "" && !current.equal(entry) {
				reason = "session_scope_changed"
			}
			if reason == "" && readErr != nil && !errors.Is(readErr, pgx.ErrNoRows) {
				reason = "session_scope_unverifiable"
			}
		}
		criticalMs := float64(time.Since(criticalStart).Milliseconds())
		if reason == "" {
			if err := tx.Commit(commitCtx); err != nil {
				reason = "commit_failed"
			}
		}
		if reason == "" {
			gateWriter.commit()
			handlerCancel()
			cancel()
			conn.Release()
			scopeCommitGateObserve(permission, "allowed", waitMs, criticalMs)
			if threshold := scopeCommitGateSlowMs(); criticalMs > float64(threshold) && s.deps.Log != nil {
				observability.ScopeCommitGateCriticalSlow.WithLabelValues(permission).Inc()
				s.deps.Log.Warn("scope commit gate commit is slow; a scope mutation queued behind it",
					"permission", permission, "route", r.URL.Path,
					"critical_ms", int64(criticalMs), "wait_ms", int64(waitMs), "threshold_ms", threshold)
			}
			return
		}
		// Fail closed: the whole request transaction is discarded, so no business
		// object from this request exists. Nothing was sent to the caller yet.
		_ = tx.Rollback(commitCtx)
		gateWriter.discard()
		handlerCancel()
		cancel()
		conn.Release()
		scopeCommitGateObserve(permission, "rolled_back_"+reason, waitMs, criticalMs)
		if reason == "response_too_large" {
			observability.ScopeCommitGateTooLarge.WithLabelValues(permission).Inc()
			if s.deps.Log != nil {
				s.deps.Log.Error("scope commit gate cancelled a handler whose response exceeded the hard bound; the request transaction was rolled back",
					"permission", permission, "route", r.URL.Path,
					"response_bytes", gateWriter.responseBytes(),
					"hard_max_bytes", gateWriter.hardBound())
			}
			writeErrorCode(w, r, http.StatusServiceUnavailable, "INTERNAL_RESPONSE_TOO_LARGE",
				"The response for this request exceeded the size a commit-gated write can report atomically; the request was not applied.", nil)
			s.auditDenyCodeDetail(scopeCommitGateDetachedRequest(r), p, permission, reason,
				"INTERNAL_RESPONSE_TOO_LARGE", http.StatusServiceUnavailable, map[string]any{"gate": "commit"})
			return
		}
		detail := map[string]any{"old_workspace_id": p.WorkspaceID, "gate": "commit", "reason": reason}
		if current.workspaceID != "" && current.workspaceID != p.WorkspaceID {
			detail["new_workspace_id"] = current.workspaceID
		}
		if readErr != nil && !errors.Is(readErr, pgx.ErrNoRows) {
			detail["gate_error"] = readErr.Error()
		}
		// 诊断线索：handler 自己已经写出的状态码。当 handler 内部的某条语句失败时，
		// 请求事务进入 aborted 状态，释放重读随之失败，本闸门会把结果改写成 409 ——
		// 真实原因（例如某条 SQL 报 "column \"id\" does not exist"）在响应里完全看不到。
		// 把 handler 的缓冲状态码一并记下来，事故现场就能立刻分流：
		//   handler_status < 400  -> 真的是作用域变更；
		//   handler_status >= 400 -> handler 自己先失败了，先修 handler（2026-09-16 真实发生）。
		if gateWriter.status != 0 {
			detail["handler_status"] = gateWriter.status
		}
		if s.deps.Log != nil {
			s.deps.Log.Warn("scope commit gate rolled back a commit: the session scope changed while the handler was running",
				"permission", permission, "route", r.URL.Path, "reason", reason,
				"handler_status", gateWriter.status,
				"critical_ms", int64(criticalMs), "wait_ms", int64(waitMs))
		}
		writeErrorCode(w, r, http.StatusConflict, ErrorAUTHZWorkspaceScopeStale, workspaceScopeStaleMessage, nil)
		s.auditDenyCodeDetail(scopeCommitGateDetachedRequest(r), p, permission, reason, ErrorAUTHZWorkspaceScopeStale, http.StatusConflict, detail)
	}
	return gateWriter, gateReq, release, true
}

// scopeCommitGateDetachedRequest removes both the client-cancellation link and
// the request transaction from a request, so a deny audit is written on its own
// connection and survives the rollback it is describing.
func scopeCommitGateDetachedRequest(r *http.Request) *http.Request {
	return r.WithContext(dbtx.WithoutTx(context.WithoutCancel(r.Context())))
}

// scopeCommitGateReadToken reads and classifies the caller session row. lock is
// true for the release re-read (FOR UPDATE) and false for the entry read.
func scopeCommitGateReadToken(ctx context.Context, tx pgx.Tx, p policy.Principal, query string, lock bool) (scopeCommitGateScopeToken, string, error) {
	var (
		workspace *string
		revokedAt *time.Time
		epoch     *int64
	)
	err := tx.QueryRow(ctx, query, p.SessionID).Scan(&workspace, &revokedAt, &epoch)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return scopeCommitGateScopeToken{}, "session_missing", err
	case err != nil:
		return scopeCommitGateScopeToken{}, "session_scope_unverifiable", err
	}
	tok := scopeCommitGateScopeToken{revoked: revokedAt != nil}
	if epoch != nil {
		tok.epoch = *epoch
	}
	if !lock {
		// The entry read refuses immediately when the decision is already stale.
		if tok.revoked {
			return tok, "session_missing", nil
		}
		if workspace == nil || *workspace == "" {
			return tok, "session_missing", nil
		}
		tok.workspaceID = *workspace
		if tok.workspaceID != p.WorkspaceID {
			return tok, "session_workspace_changed", nil
		}
		return tok, "", nil
	}
	if workspace != nil {
		tok.workspaceID = *workspace
	}
	return tok, "", nil
}

func (t scopeCommitGateScopeToken) equal(o scopeCommitGateScopeToken) bool {
	return t.workspaceID == o.workspaceID && t.revoked == o.revoked && t.epoch == o.epoch
}

// ---------------------------------------------------------------------------
// The retractable response buffer.
// ---------------------------------------------------------------------------

// scopeGateWriter buffers a gated handler response until the commit decision has
// been taken, so that a rollback can still be reported to the caller as 409.
//
// The invariant this type exists to hold is: NOTHING reaches the client before
// the commit decision. That is why there is no pass-through path at all --
// neither at the 256 KiB memory bound (the response continues in a private
// temporary file), nor past the hard bound (the handler is cancelled and the
// request fails closed), nor on Flush (the flush is deferred until the commit).
//
// Unwrap is deliberately NOT implemented: it would hand
// http.ResponseController the real writer and give a handler a way to bypass the
// invariant. Flush is implemented directly, so handlers that assert
// http.Flusher keep working.
type scopeGateWriter struct {
	target      http.ResponseWriter
	header      http.Header
	status      int
	buf         bytes.Buffer
	spool       *os.File
	spoolPath   string
	max         int
	hardMax     int64
	written     int64
	spooled     bool
	tooLarge    bool
	flushWanted bool
	committed   bool
	cancel      func()
}

func newScopeGateWriter(target http.ResponseWriter, cancel func()) *scopeGateWriter {
	return &scopeGateWriter{
		target:  target,
		header:  http.Header{},
		max:     scopeCommitGateResponseMax(),
		hardMax: scopeCommitGateResponseHardMax(),
		cancel:  cancel,
	}
}

func (w *scopeGateWriter) Header() http.Header { return w.header }

func (w *scopeGateWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

// Write buffers the response. Past the hard bound it stops accepting bytes,
// cancels the handler and returns errScopeGateResponseTooLarge; the bytes are
// dropped, never forwarded, because forwarding them is exactly the failure this
// bound exists to prevent.
func (w *scopeGateWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.tooLarge {
		return 0, errScopeGateResponseTooLarge
	}
	if int64(len(b)) > w.hardMax-w.written {
		w.written += int64(len(b))
		w.tooLarge = true
		if w.cancel != nil {
			w.cancel()
		}
		return 0, errScopeGateResponseTooLarge
	}
	if w.spool != nil {
		if _, err := w.spool.Write(b); err != nil {
			w.tooLarge = true
			if w.cancel != nil {
				w.cancel()
			}
			return 0, errScopeGateResponseTooLarge
		}
		w.written += int64(len(b))
		return len(b), nil
	}
	if w.buf.Len()+len(b) > w.max {
		if err := w.spoolToDisk(); err != nil {
			w.tooLarge = true
			if w.cancel != nil {
				w.cancel()
			}
			return 0, errScopeGateResponseTooLarge
		}
		if _, err := w.spool.Write(b); err != nil {
			w.tooLarge = true
			if w.cancel != nil {
				w.cancel()
			}
			return 0, errScopeGateResponseTooLarge
		}
		w.written += int64(len(b))
		return len(b), nil
	}
	n, err := w.buf.Write(b)
	w.written += int64(n)
	return n, err
}

// spoolToDisk moves the in-memory buffer into a private temporary file. The file
// is created 0600 inside a 0700 directory and is removed on every exit path.
func (w *scopeGateWriter) spoolToDisk() error {
	dir := scopeCommitGateSpoolDir()
	if dir == "" {
		dir = os.TempDir()
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "scope-commit-gate-*.spool")
	if err != nil {
		return err
	}
	_ = f.Chmod(0o600)
	if w.buf.Len() > 0 {
		if _, err := f.Write(w.buf.Bytes()); err != nil {
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
			return err
		}
		w.buf.Reset()
	}
	w.spool = f
	w.spoolPath = f.Name()
	w.spooled = true
	observability.ScopeCommitGateSpooled.Inc()
	return nil
}

// Flush is the deferred flush: the caller asked for the buffered bytes, but the
// commit decision has not been taken, so the request is remembered and the
// underlying flush happens only if the transaction commits.
func (w *scopeGateWriter) Flush() {
	w.flushWanted = true
}

func (w *scopeGateWriter) flushHeader() {
	if w.committed {
		return
	}
	w.committed = true
	dst := w.target.Header()
	for k, vs := range w.header {
		dst[k] = vs
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	w.target.WriteHeader(status)
}

// commit sends the buffered response to the client. It is called only after the
// request transaction has committed.
func (w *scopeGateWriter) commit() {
	if w.tooLarge {
		w.closeSpool()
		return
	}
	w.flushHeader()
	if w.spool != nil {
		_, _ = w.spool.Seek(0, io.SeekStart)
		_, _ = io.Copy(w.target, w.spool)
	} else if w.buf.Len() > 0 {
		_, _ = w.target.Write(w.buf.Bytes())
	}
	w.buf.Reset()
	w.closeSpool()
	if w.flushWanted {
		if f, ok := w.target.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// discard drops the buffered response. Nothing has been sent to the client, so
// the caller can still be told the truth about the rolled-back transaction.
func (w *scopeGateWriter) discard() {
	w.buf.Reset()
	w.header = http.Header{}
	w.status = 0
	w.written = 0
	w.flushWanted = false
	w.closeSpool()
}

// closeSpool closes and removes the temporary file. It is idempotent, so a
// commit, a rollback and a deferred cleanup can all call it.
func (w *scopeGateWriter) closeSpool() {
	name := w.spoolPath
	if w.spool != nil {
		_ = w.spool.Close()
	}
	w.spool = nil
	w.spoolPath = ""
	if name != "" {
		_ = os.Remove(name)
	}
}

// responseTooLarge reports whether the handler tried to exceed the hard bound.
func (w *scopeGateWriter) responseTooLarge() bool { return w.tooLarge }

// responseBytes is how many bytes the handler produced.
func (w *scopeGateWriter) responseBytes() int64 { return w.written }

// hardBound is the configured absolute bound, for evidence and diagnostics.
func (w *scopeGateWriter) hardBound() int64 { return w.hardMax }
