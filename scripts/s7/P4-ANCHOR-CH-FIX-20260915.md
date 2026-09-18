# P4-ANCHOR-01 and P4-CH-01 -- fix round 2026-09-15 (session 12:08 -> 13:0x +08)

Production was NOT deployed and NOT restarted. API PID 1922757 / sha 910b7ca2... and
worker PID 4011175 / sha 513e67bd... are unchanged. Every artifact below is
additive; nothing was deleted, rewritten, truncated or reset.

## P4-ANCHOR-01 -- POST /api/admin/v1/audit/anchors refused EVERY legitimate request

### Reproduction (frozen probe, unchanged bytes, sha 08825103e409577c50bffdab6873fd5059d6f80920fdcb0ecf2cd33158cbeb23)
evidence: acceptance-evidence/dual-system-v1/sprint7/p4-anchorprobe-20260915T050040Z
  candidate api-expcap-r3-calib 1829a1db...
  positive_http=409  {"error":{"code":"AUTHZ_WORKSPACE_SCOPE_STALE", ...}}
  negative_http=409
  before/after blockchain-audit 57/57 | blockchain-exports 1572/1572 | blockchain-raw 597699/597699
  anchor_rows_before=8  anchor_rows_after=8        (no object, no row, either way)
audit trail: seq 54387..54390 | authorization.denied | /api/admin/v1/audit/anchors
  detail {"gate":"commit","reason":"session_scope_unverifiable",
          "gate_error":"failed to deallocate cached statement(s): conn busy"}

### Root cause (proved, not inferred)
internal/audit/verify.go VerifyChain opened its range result set with
    rows, err := pool.Query(ctx, query, args...)
and then, BEFORE consuming it, issued a second statement on the same connection
    pool.QueryRow(ctx, "SELECT event_hash FROM ... WHERE seq = $2", startSeq-1)

pgx allows exactly ONE in-flight statement per connection. On a *dbtx.Pool whose
context carries the request transaction (which the commit gate opens for every
gated write route) both statements run on the SAME PostgreSQL connection, so the
nested read fails with pgconn's "conn busy", and the failing baseRows.Close()
marks the still-open statement invalidated in pgx's statement cache. The gate's
release re-read then cannot deallocate the invalidated cached statements and
fail-closes: 409 AUTHZ_WORKSPACE_SCOPE_STALE with exactly that gate_error.
The audit-anchor handler reaches the path through audit.VerifyAnchors ->
VerifyChain for the newest anchor, i.e. on EVERY legitimate request.

Only one of the four existing anchors triggers it (the newest one), which is why
the route looked intermittent in the matrix and deterministic on the probe.

### Fix (small ASCII patch, frozen previous SHA, local == VM byte-for-byte)
  internal/audit/verify.go        83d843eb... -> 35841348ed463d494fd42a9db0fb2b9ffe7a1f7521e2ff65ddd49dcf7a877eaf
      the predecessor hash is read BEFORE the range result set is opened; one
      result set per connection, always. No check was weakened, removed or
      reordered in a way that can allow anything: the release re-read is still the
      only authority and every error path still fails closed.
  internal/store/jobs.go          -> fe3ffcb9...  FindReusableJob closes its result
      set before the request-fingerprint UPDATE (same defect class, POST /api/v2/downloads).
  internal/api/sprint6_governance.go -> ecadb5f8...  handleAdminAuditExport closes
      its result set before the failure UPDATE on the scan-error path (same class).
  internal/audit/verify_single_resultset_contract_test.go  dadd90c6...  new source
      contract: the predecessor read must precede the range query and no statement
      may be issued between the range query and its consumption loop.
  internal/audit/verify_conn_live_test.go                 63f66128...  new live test
      (build tag live) driving the PRODUCTION function inside a real request
      transaction and then issuing the gate's release re-read on the same tx.
  scripts/s7/p4_nested_query_scan.py                      10624bd9...  new static
      scanner for the whole defect class.

### Negative and positive calibration of the tests
negcal-frozen-83d843eb.log (frozen pre-fix bytes restored temporarily):
  FAIL TestVerifyChainReadsThePredecessorBeforeOpeningTheRangeResultSet
       (predecessor at byte 3340, range query at byte 2959)
  FAIL TestVerifyChainIssuesNoStatementWhileTheRangeResultSetIsOpen
       (VerifyChain issues pool.QueryRow( between the range query and its consumption loop)
  FAIL TestVerifyChainKeepsTheRequestTransactionUsable
       (the handler statement and the gate's release re-read share one connection.
        VerifyChain err=read predecessor hash: conn busy)
poscal-fixed-35841348.log (fixed bytes):
  ok internal/audit 0.008s | ok internal/store 0.015s | ok internal/api 0.739s
  PASS all three tests; VerifyChain ok: checked=617 valid=true integrity=sealed_incident

### End-to-end proof on the frozen probe (same script sha, BIN overridden)
evidence: acceptance-evidence/dual-system-v1/sprint7/p4-anchorprobe-20260915T050936Z
  bin=bin.new/api-anchorfix-calib c8aac0b10f9621aa09b5c42d471551236f9c5a7b5f6358bf7304e521f3d78d5f
  positive_http=200  data.anchor {chain:audit, date:2026-09-15, from_seq:54035, to_seq:54399,
                                   event_count:365, object_ref:audit/audit/2026-09-15/anchor.json}
  negative_http=409  zero new names in every bucket, anchor_rows 10 -> 10
  audit trail seq 54400 success {"date":"2026-09-15","reason":"p4 anchor positive","created":true}
              seq 54401 success (negative request handler)   seq 54402 deny (gate refusal)

Real artifacts created by the legitimate route (both verified through the S3 API,
never from the data directory, and matched against audit_chain_anchors.content_hash):
  audit/audit/2026-09-15/anchor.json  496 B sha256 67f0a92a...  HASH_MATCH=1
  audit/login/2026-09-15/anchor.json  494 B sha256 a13f93c7...  HASH_MATCH=1
  (scripts/s7/p4_anchor_object_verify.py, new)

## P4-CH-01 -- a refused request still ran a 21 s ClickHouse scan

### Fix
  internal/api/scopecommit_gate.go  19ecf9f6... -> 3d760ee7df936dac99c5c9e14036f7af3c28f851bb9c5b67ed198fec35ab73b4
      new reusable scope recheck helper scopeCommitGateRecheck(ctx, principal):
      reads the caller session row (workspace_id / revoked_at / scope_epoch) on
      the RAW pool with dbtx.WithoutTx, so it sees the mutation the request
      transaction cannot see, and returns a typed staleness error. It is NOT a
      second enforcement point -- the gate's release re-read stays the only
      authority; the helper only decides whether external work is worth starting.
  internal/api/sprint5_downloads.go -> 7819644a...  downloadEstimate calls the
      recheck immediately before the ClickHouse FINAL scan and reports
      basis=scope_not_revalidated instead of querying for a request that is about
      to be refused.
  internal/api/download_scope_recheck_contract_test.go     new source contract:
      the recheck call must precede the first ClickHouse statement in
      downloadEstimate, and the helper must use WithoutTx + the gate token query.
  internal/api/p4_external_side_effect_gate_v6 evidence dir                (below)

### Gate rounds
v6 on the frozen r3 candidate (api-expcap-r3-calib 1829a1db...), port 18195, delay 8000 ms:
  evidence p4-ext3-p4ext6-20260915T051152Z     PASS=54 FAIL=1 NV=2   GATE_FAIL=1
  the single FAIL is line "FAIL 2: the refused preview issued ZERO ClickHouse queries (got=1 want=0)"
  => that is P4-CH-01 itself, measured on a binary that does not carry the fix.
     v5's second FAIL (a sliding-window measurement defect) is gone: the anchored
     export-row assertion replaced it, exactly as the directive asked.
v6 on the fixed candidate (api-p4fix-calib 180e4076...), port 18194: see
  p4-ext3-p4ext6-20260915T052220Z   (result recorded in the handover)

## Red lines
No deploy, no restart (API/worker PIDs and SHAs unchanged), no TRUNCATE/reset/
clean/drop/default delete, no object/bucket/evidence/binary/log/backup deleted,
object_ref never edited, no legacy SHA/seq backfilled, no audit history fabricated
(every audit row is the gates'/probes' own real write), local dirty worktree
untouched apart from the new/patched files, no credential printed.
