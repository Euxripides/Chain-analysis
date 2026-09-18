# Sprint 7 -- must_change_password enforcement + session workspace staleness guard

Status: IMPLEMENTED AND LOCALLY VERIFIED, NOT DEPLOYED.
Owner: sprint7 C1-SEC (mandatory password change) and C1-RACE (in-flight workspace switch).
Scope of this note: the design, the exact refusal contracts, the audit events, the measured
blast radius, and the rollback. Everything below was measured on this working tree, not on the
VM.

## 1. Problem statement

### 1.1 C1-SEC: identities.must_change_password was decorative

Measured on the running API: all 39 identities, including the single real account
admin@local, carry must_change_password = true in PostgreSQL; the flag was only echoed in the
/api/auth/v1/login and /api/auth/v1/session response bodies. A session whose identity still
had the flag got HTTP 200 on business reads and writes. Documentation alone does not close
this, and flipping the flag to false does not close it either, so the decision was to make the
flag load bearing on the server.

### 1.2 C1-RACE: a session could commit into the workspace it had left

Measured (evidence dir sprint7/c1-session-scope-convergence-20260913T181249Z): a write that
was authenticated while the session was in workspace A and whose body was still being uploaded
committed into A after the same session had been switched to B. Observed: the switch returned
200 at t+1s, the in-flight POST /api/v2/cases returned 201 at t+4s and created the case in the
abandoned workspace A (cases named RACE-... in A went 0 -> 1). The security-critical variant
(membership revoked in A) is already fail closed by casework's own membership re-check, which
returns 401 and creates nothing; the remaining gap is specifically the SESSION workspace going
stale mid-request.

## 2. C1-SEC: the refusal contract

    HTTP status : 403
    error code  : AUTHN_PASSWORD_CHANGE_REQUIRED
    envelope    : the standard error envelope (success=false, error.code, error.message,
                  error.details, meta.request_id, meta.trace_id)
    details     : {"reason": "must_change_password",
                   "password_change_endpoint": "/api/auth/v1/password/change"}
    message     : "Password change required: change the password through the self-service
                   endpoint before using business APIs."
    leaks       : nothing. No password, no hash, no algorithm, no session token, no identity
                  detail. Clients must branch on error.code, never on the message.

The refusal happens BEFORE any business logic: the guard is a router middleware that runs after
identityMiddleware and before auditCaptureMiddleware and before route handlers, so a refused
request never reaches a business handler and never produces a business audit row.

### 2.1 Guarded surfaces (fail closed)

| Surface          | Guarded |
|------------------|---------|
| /api/v1/         | yes (business surfaces, business prefixes) |
| /api/v2/         | yes |
| /api/admin/v1/   | yes |
| any other /api/ path that is not the self-service surface | yes (fail closed: a future /api/v3 is guarded without a code change) |
| /api/auth/v1/ (self-service, outside the allowlist) | no -- previous behaviour is preserved exactly |
| /health/live, /health/ready, /metrics, /openapi.json, /docs, /redoc | no -- outside the guard entirely |

The rules and the allowlist are DATA in backend/internal/api/mustchangepw.go
(mustChangePasswordGuardedPrefixes, mustChangePasswordAllowlist, mustChangePasswordSurface),
not scattered conditionals, so the reachable set is reviewable in one place.

### 2.2 Allowlist (reachable while the flag is set)

| Method | Path                          | Why it must stay reachable |
|--------|-------------------------------|----------------------------|
| GET    | /api/auth/v1/session          | the console reads its own session state and sees that a change is pending |
| POST   | /api/auth/v1/logout           | leave a session that cannot be used |
| POST   | /api/auth/v1/password/change  | the only action that clears the flag |
| GET    | /api/auth/v1/methods          | discover which sign-in methods are configured |

Explicit decisions for other genuine "self" endpoints (default stays fail closed):
- GET /api/auth/v1/devices, PATCH /api/auth/v1/devices/{id}, GET /api/auth/v1/sessions,
  DELETE /api/auth/v1/sessions/{id}, POST /api/auth/v1/sessions/revoke-all,
  POST /api/auth/v1/session/workspace (+ its GET), POST /api/auth/v1/refresh,
  POST /api/auth/v1/login, GET /api/auth/v1/oidc/*, POST /api/auth/v1/mfa/verify:
  NOT added to the allowlist. They are reachable because the whole self-service surface keeps
  its previous behaviour, which is what the requirement asked for. Adding them to the
  allowlist would have narrowed or widened behaviour beyond what was needed. Because the
  default is fail closed, moving any of them under a business prefix in the future would
  refuse them until the table above is extended deliberately.

## 3. Where the flag comes from (no extra query per request)

- iam.SessionManager.loadByToken already selects i.must_change_password from identities on
  every Authenticate call (backend/internal/iam/session.go) and scans it into
  sess.Identity.MustChangePassword. There is no session-level cache of the flag.
- principalFromSession (backend/internal/api/authmw.go) now copies it into the new
  policy.Principal.MustChangePassword field.
- Consequence (requirement 6): when the password change clears the flag in PostgreSQL, the
  very next request of the same session re-reads it and is allowed. There is no cached flag to
  invalidate and no re-read added for the password/change path.
- policy.Principal gained one keyed bool with json tag must_change_password. Every existing
  construction site still compiles: the zero value is "flag not set", which is the correct
  default for legacy-header and system principals.

## 4. Audit event (C1-SEC)

Every refusal writes exactly one row through the existing audit writer
(backend/internal/api/authmw.go: auditDenyCode, which follows the auditDeny convention):

    action       : authorization.denied
    category     : authorization
    result       : deny
    http_status  : 403
    error_code   : AUTHN_PASSWORD_CHANGE_REQUIRED
    deny_reason  : must_change_password
    route        : r.URL.Path (the requested route)
    request_id   : meta.RequestID, trace_id likewise
    actor        : identity id, email, display name, roles, org, workspace, session id
    risk_level   : high, sensitive=true
    detail       : {"reason": "must_change_password"}

The write is synchronous and detached from the client connection (auditRequestDetached) so the
row survives a client disconnect, and it is bounded by emitAudit (now+5s). A failed audit write
is logged and does NOT turn the refusal into an allow: the refusal is the fail-closed decision.
No password, password hash, token or cookie value is ever recorded.

## 5. C1-RACE: the session workspace staleness guard

    applies to  : state-changing methods only (never GET/HEAD/OPTIONS)
    skipped for : system/compat principals, principals without a session id, and
                  organisation-level principals that carry no workspace
    check       : one primary-key lookup,
                  SELECT workspace_id::text FROM sessions WHERE id=$1::uuid AND revoked_at IS NULL
    refused with: HTTP 409, error code AUTHZ_WORKSPACE_SCOPE_STALE
    message     : "The session workspace changed while this request was in flight; reload the
                  session and retry." (names no object, workspace or session identifier)
    fail closed : a missing row, a revoked row, a workspace mismatch, a missing/unusable pool
                  and any database error all refuse; there is no allow path on error
    audit       : action authorization.denied, category authorization, result deny,
                  http_status 409, error_code AUTHZ_WORKSPACE_SCOPE_STALE, deny_reason one of
                  session_workspace_changed | session_missing | session_scope_unverifiable,
                  plus route and request id
    call sites  : s.require and s.requireAny, after the permission decision and after the CSRF
                  check has passed, so a request that would already be refused for another
                  reason keeps its existing code
    cost        : one indexed primary-key lookup per state-changing request, and zero database
                  reads for safe methods
    metrics     : no new Prometheus series; the audit row plus the stable error code are the
                  observability, and a bounded counter can be added later if dashboards
                  need it
    rollback    : revert the binary to the previously deployed SHA (section 7); the guard is a
                  pure addition with no schema change, no migration and no data change

## 6. Measured blast radius of enabling C1-SEC in production

- 39 of 39 identities currently have must_change_password = true (including admin@local).
  All of them would immediately be refused on every business surface.
- admin@local would keep exactly: session read, logout, password change, auth method
  discovery, health endpoints. Everything else -- /api/v2 (user console), /api/admin/v1
  (admin console) and the legacy /api/v1 surface -- would answer 403
  AUTHN_PASSWORD_CHANGE_REQUIRED until a REAL password change happens.
- A real password change is required for the flag to clear; iam.Repo.SetPassword revokes every
  session of that identity (section 6.3 forced logout), so the flow is: change password (the current
  session is revoked and its cookies are cleared), sign in again, and the business surfaces are
  open again because the new session carries must_change_password = false.
- Because the flag is re-read per request, a session does NOT keep refusing after a successful
  change; there is no cache to invalidate. The revoked-session behaviour is the designed
  forced logout, not a defect of this guard.
- Operational consequence: activating this on the current production database is a
  deliberate, user-visible lockout of the administrator until the first real password change.
  That is exactly why activation requires explicit authorisation (section 8).

## 7. Rollback

The guard is included in the current source tree but has NOT been built into the deployed
artifact by this change. If an activation ever has to be undone:

1. Redeploy the previously deployed binary by its recorded SHA
   39e0f74772237fb1bc20c9cc7fcfbfec73463779d20de0dff743062351e267f7 (this is the frozen,
   already-deployed artifact that does not contain the guard).
2. If a source-level rollback is preferred, revert the frozen source tree that produced that
   SHA. Do NOT hand-edit the guard out of a deployed binary.
3. No database rollback is needed: the change adds no schema, no migration and no data write.
   The identity flag values in PostgreSQL are untouched by this change (no password and no
   flag was modified while implementing or testing it).
4. After a rollback, remember that the security gap reopens: business endpoints accept
   sessions whose identity still has must_change_password = true.

## 8. DO NOT DEPLOY WITHOUT USER AUTHORISATION

This change must NOT be deployed to production without explicit authorisation from the user
(or root). Reasons, in order:

1. Every one of the 39 identities is flagged, so enabling enforcement blocks all business
   endpoints for all users until each of them completes a real password change.
2. The single real administrator (admin@local) would be locked out of the user console, the
   admin console and the legacy surface. This is a security-correct but operationally
   disruptive state that only the user can authorise, and only with a prepared password-change
   runbook and a maintenance window.
3. No real password was read, printed or changed while building and testing this. Testing used
   fixtures and stub sessions only, and the production activation remains unproven in
   production.
4. The candidate build and the rollback binary are identified, but the deploy itself is out of
   scope for this change.

## 9. Verification performed locally

- gofmt -l on every touched Go file: no output.
- go build ./... : exit 0.
- go vet ./... : exit 0.
- go test ./internal/api/... ./internal/iam/... ./internal/policy/... : ok, ok, ok (exit 0).
- New regression suites (see the test files for the full tables):
  * TestMustChangePasswordSurfaceClassification -- the data-driven surface rule.
  * TestMustChangePasswordGuardRefusesBusinessSurfaces -- one /api/v2 read, one /api/v2 write,
    one /api/admin/v1 read, one /api/admin/v1 write, one /api/v1 read and the legacy ticket
    route, each refused with the documented code, with a stub handler proving the business
    handler never ran.
  * TestMustChangePasswordGuardAllowsAllowlistedSelfService -- all four allowlist entries.
  * TestMustChangePasswordGuardAllowsUnflaggedIdentity -- the same samples with a clean flag.
  * TestMustChangePasswordGuardAllowsAfterFlagCleared -- same session, flag cleared, allowed.
  * TestMustChangePasswordGuardOnProductionRouter -- the same three tables through the real
    router built by buildRouter, plus /health/live staying 200.
  * TestMustChangePasswordRefusalIsAudited -- the audit row fields.
  * TestMustChangePasswordGuardMountCalibration -- THE NEGATIVE CALIBRATION for C1-SEC: it
    fails if the middleware is not in the root chain, runs before identityMiddleware, or is
    moved inside a business route block; it also feeds the detector a source with the mount
    line deleted to prove the detector is not blind.
  * TestIdentityFlagIsReloadedPerRequest -- the session loader still selects and scans
    identities.must_change_password, which is why no cache invalidation is needed.
  * TestSessionWorkspaceGuardThroughRequire / ...RequireAny -- same workspace allowed,
    switched workspace refused, missing/revoked session refused, database error refused,
    safe methods not checked and no session read for them.
  * TestSessionWorkspaceGuardFailsClosedWithoutStore -- no usable pool refuses.
  * TestSessionWorkspaceGuardSkipsNonSessionPrincipals -- system/no-session/org-level skip.
  * TestSessionWorkspaceStaleRefusalIsAudited -- the audit row fields.
  * TestSessionWorkspaceGuardWiringCalibration -- THE NEGATIVE CALIBRATION for C1-RACE: it
    fails if require/requireAny stop calling the guard, and it proves the detector detects by
    feeding it a copy of authmw.go with the call sites renamed away.

## 10. Known limitations

- No real browser or UI flow was exercised. The guard was proven with Go tests at the
  middleware, authorization-wrapper and router level; no front-end was built, served or
  clicked. The front-end currently does not map AUTHN_PASSWORD_CHANGE_REQUIRED to a
  change-password modal, so today the user would see a 403 rather than a guided flow.
- The refusal message is English-only (the whole patch is constrained to ASCII). The stable
  error code is the contract; the console should localise on the code.
- The same-session-after-change proof is at the composition level plus a source assertion that
  the session loader re-reads the flag per request; the database-backed variant needs the live
  test (mustchangepw_live_test.go, build tag live, opt-in by environment) which was NOT run
  here because no PostgreSQL was reachable from this machine.
- The session workspace guard's production SQL was not executed against PostgreSQL here; the
  guard logic is covered with an injected reader, including the database-error fail-closed
  branch. Only the live run can prove the query against real rows.
- The state-change race was not reproduced end to end here (it was reproduced by the measuring
  agent); this change closes the window at the authorization layer and cannot prove the
  original race is gone under production load.
- Activation in production is NOT authorised (section 8), and no real password was changed.
