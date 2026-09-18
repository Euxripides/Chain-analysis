package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// ---------------------------------------------------------------------------
// AUTH-007 / S3: the dedicated short transaction for a cross-session revoke.
//
// WHY THIS FILE EXISTS
//
// POST /api/admin/v1/sessions/{id}/commands/revoke is the one measured route
// whose handler writes a sessions row that ANOTHER concurrent request may be
// using as its own caller row. Inside the generic commit gate that produced a
// lock ring:
//
//	R1 (caller S1, target S2): handler UPDATE sessions ... id=S2   -> holds S2
//	                           release SELECT ... id=S1 FOR UPDATE -> wants S1
//	R2 (caller S2, target S1): handler UPDATE sessions ... id=S1   -> holds S1
//	                           release SELECT ... id=S2 FOR UPDATE -> wants S2
//
// scripts/s7/c1-auth007-scope-epoch-gate-v2.sh measured the consequence: both
// requests outlived a 60 s client bound and neither answered (r1=000 r2=000,
// elapsed 60011 ms), while the same scenario against the first implementation
// answered in 42 ms. The ring is real; the fix is not a longer timeout and not a
// retry, it is to stop taking the two row locks in a request-order-dependent
// sequence.
//
// WHAT THIS FILE DOES
//
// The route no longer enters the generic request transaction. Instead the
// handler owns ONE short transaction that:
//
//	1. applies the same bounded, labelled SET LOCAL settings the gate uses
//	   (lock_timeout, statement_timeout, idle_in_transaction_session_timeout,
//	   application_name = bpgate:<request id>);
//	2. reads the caller token WITHOUT a lock -- the authorization decision the
//	   middleware already took, re-captured at the handler boundary;
//	3. locks the actor row and the target row in ASCENDING SESSION-ID ORDER, so
//	   two mutual revokes acquire the same locks in the same order and the second
//	   one waits instead of deadlocking;
//	4. re-verifies, on the LOCKED caller row, everything the pre-handler decision
//	   depended on: the row still exists, is not revoked, still carries the
//	   principal workspace and still carries the epoch observed in step 2;
//	5. re-decides the admin permission against the same principal;
//	6. updates the target and commits.
//
// The refusal is the same 409 AUTHZ_WORKSPACE_SCOPE_STALE with the same
// detached, exactly-once deny audit the generic gate writes, so the exemption is
// a relocation of the commit-time check, not a removal of it.
//
// SELF-TARGET. A revoke whose target IS the caller session is a single-row path:
// the actor and the target are the same row, it is locked once, and the caller
// cannot outlive its own write. It is handled explicitly here rather than by the
// "the target is somebody else" assumption.
// ---------------------------------------------------------------------------

// scopeRevokeOutcome is the closed set of results of the dedicated transaction.
type scopeRevokeOutcome int

const (
	// scopeRevokeCommitted: the target row was revoked and the transaction committed.
	scopeRevokeCommitted scopeRevokeOutcome = iota
	// scopeRevokeActorStale: the caller session is gone, revoked, moved to another
	// workspace or its epoch advanced. The transaction rolled back; the caller is
	// refused with the shared 409 scope code.
	scopeRevokeActorStale
	// scopeRevokeTargetMissing: no such session, or a session of another
	// organisation for a caller without platform scope. Reported as 404, exactly
	// like the pre-existing behaviour, so object existence is not leaked.
	scopeRevokeTargetMissing
	// scopeRevokePermissionDenied: the commit-time permission re-check failed.
	scopeRevokePermissionDenied
	// scopeRevokeUnavailable: the transaction could not be taken (no pool, lock
	// timeout, statement timeout, commit failure). Fail closed.
	scopeRevokeUnavailable
)

// scopeRevokeRow is one sessions row read inside the dedicated transaction.
type scopeRevokeRow struct {
	exists         bool
	id             string
	identityID     string
	organizationID string
	workspaceID    string
	revoked        bool
	epoch          int64
}

// scopeRevokePeekQuery reads one sessions row without taking a lock.
const scopeRevokePeekQuery = `SELECT id::text, COALESCE(identity_id::text, ''), COALESCE(organization_id::text, ''),
       COALESCE(workspace_id::text, ''), revoked_at, COALESCE(scope_epoch, 0)
FROM sessions
WHERE id = $1::uuid`

// scopeRevokeLockQuery reads one sessions row and locks it. It is executed once
// per session id, in ascending id order.
const scopeRevokeLockQuery = `SELECT id::text, COALESCE(identity_id::text, ''), COALESCE(organization_id::text, ''),
       COALESCE(workspace_id::text, ''), revoked_at, COALESCE(scope_epoch, 0)
FROM sessions
WHERE id = $1::uuid
FOR UPDATE`

// scopeRevokeReadRow reads one sessions row, optionally locking it. A row that
// does not exist is not an error: it is reported as exists=false, because
// "missing" and "unreadable" must not be conflated.
func scopeRevokeReadRow(ctx context.Context, tx pgx.Tx, id string, lock bool) (scopeRevokeRow, error) {
	query := scopeRevokePeekQuery
	if lock {
		query = scopeRevokeLockQuery
	}
	var (
		rowID       string
		identityID  string
		orgID       string
		workspaceID string
		revokedAt   *time.Time
		epoch       int64
	)
	err := tx.QueryRow(ctx, query, id).Scan(&rowID, &identityID, &orgID, &workspaceID, &revokedAt, &epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return scopeRevokeRow{}, nil
	}
	if err != nil {
		return scopeRevokeRow{}, err
	}
	return scopeRevokeRow{
		exists:         true,
		id:             rowID,
		identityID:     identityID,
		organizationID: orgID,
		workspaceID:    workspaceID,
		revoked:        revokedAt != nil,
		epoch:          epoch,
	}, nil
}

// scopeRevokeUUIDLike reports whether a route parameter can be a session id. It
// keeps a malformed path from reaching PostgreSQL as a cast error, which would
// otherwise be indistinguishable from a database failure.
func scopeRevokeUUIDLike(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			switch {
			case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			default:
				return false
			}
		}
	}
	return true
}

// revokeSessionDedicatedTx performs one cross-session (or self-targeted) admin
// revoke inside a single short transaction with a deterministic lock order and a
// commit-time re-verification of the caller.
//
// It returns the outcome, the refusal reason (for the deny audit), the revoked
// session's identity id (for the success audit) and the underlying error.
func (s *Server) revokeSessionDedicatedTx(r *http.Request, p policy.Principal, targetID, reason string) (scopeRevokeOutcome, string, string, error) {
	if s.deps.Store == nil || s.deps.Store.Pool == nil || s.iam == nil {
		return scopeRevokeUnavailable, "no_database", "", errScopeCommitGateNoDSN
	}
	actorID := strings.TrimSpace(p.SessionID)
	targetID = strings.TrimSpace(targetID)
	if actorID == "" || !scopeRevokeUUIDLike(actorID) {
		return scopeRevokeActorStale, "no_caller_session", "", nil
	}
	if !scopeRevokeUUIDLike(targetID) {
		return scopeRevokeTargetMissing, "target_not_a_session_id", "", nil
	}
	// The commit decision has to outlive a client that disconnected while the
	// revoke was in flight.
	ctx := context.WithoutCancel(r.Context())
	tx, err := s.deps.Store.Pool.Begin(ctx)
	if err != nil {
		return scopeRevokeUnavailable, "begin_failed", "", err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := applyScopeTxLocalSettings(ctx, tx, metaFrom(r.Context()).RequestID); err != nil {
		return scopeRevokeUnavailable, "set_local_failed", "", err
	}

	// (2) The authorization decision, re-captured without a lock.
	entry, err := scopeRevokeReadRow(ctx, tx, actorID, false)
	if err != nil {
		return scopeRevokeUnavailable, "entry_read_failed", "", err
	}
	if !entry.exists || entry.revoked || entry.workspaceID == "" || entry.workspaceID != p.WorkspaceID {
		return scopeRevokeActorStale, "actor_scope_stale_at_entry", "", nil
	}

	// (3) Deterministic lock order. Both members of a mutual revoke compute the
	// same order, so the second one queues behind the first instead of forming a
	// ring with it.
	first, second := actorID, targetID
	if second < first {
		first, second = second, first
	}
	rows := make(map[string]scopeRevokeRow, 2)
	for _, id := range []string{first, second} {
		if _, seen := rows[id]; seen {
			continue
		}
		row, err := scopeRevokeReadRow(ctx, tx, id, true)
		if err != nil {
			return scopeRevokeUnavailable, "lock_failed", "", err
		}
		rows[id] = row
	}

	// (4) Commit-time re-verification of the caller on the LOCKED row.
	locked, ok := rows[actorID]
	switch {
	case !ok || !locked.exists:
		return scopeRevokeActorStale, "session_missing", "", nil
	case locked.revoked:
		return scopeRevokeActorStale, "session_missing", "", nil
	case locked.workspaceID != p.WorkspaceID:
		return scopeRevokeActorStale, "session_workspace_changed", "", nil
	case locked.epoch != entry.epoch:
		return scopeRevokeActorStale, "session_scope_changed", "", nil
	}

	// (5) Commit-time permission re-check against the same principal the
	// middleware authorised.
	if s.policy == nil {
		return scopeRevokeUnavailable, "policy_unavailable", "", nil
	}
	if decision := s.decide(r, p, "admin:session:revoke"); !decision.Allowed {
		return scopeRevokePermissionDenied, decision.Reason, locked.identityID, nil
	}

	// (6) Target checks, on the LOCKED target row.
	target, ok := rows[targetID]
	if !ok || !target.exists {
		return scopeRevokeTargetMissing, "target_missing", "", nil
	}
	if target.organizationID != p.OrganizationID && !s.isPlatformScope(r) {
		return scopeRevokeTargetMissing, "target_other_organization", "", nil
	}
	if !target.revoked {
		txCtx := dbtx.WithTx(ctx, tx)
		if err := s.iam.Sessions().RevokeByID(txCtx, targetID, "revoked_by_admin", p.IdentityID); err != nil {
			return scopeRevokeUnavailable, "update_failed", target.identityID, err
		}
		meta := metaFrom(r.Context())
		if err := iam.RecordLoginEvent(txCtx, s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
			EventType:  iam.EventSessionRevoked,
			Result:     "success",
			IdentityID: target.identityID,
			Audience:   iam.AudienceAdmin,
			SessionID:  targetID,
			IP:         meta.IP,
			UserAgent:  meta.UserAgent,
			RiskLevel:  "critical",
			Detail:     map[string]any{"actor_id": p.IdentityID, "reason": reason},
		}); err != nil {
			return scopeRevokeUnavailable, "login_event_failed", target.identityID, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return scopeRevokeUnavailable, "commit_failed", target.identityID, err
	}
	return scopeRevokeCommitted, "", target.identityID, nil
}
