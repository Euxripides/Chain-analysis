# DSH handover 2026-09-15 12:08 +08 — the three known P4 gate defects are FIXED and the first real 44-route matrix round is on record (12 PASS / 0 FAIL / 32 NOT_VERIFIED); two NEW real defects found (a refused request still runs a 21 s ClickHouse scan; the audit-anchor admin route always fails 409 with a gate "conn busy"); production still frozen NO-GO

> Session: started 2026-09-15 10:40 +08 from `prompt-20260915-1000-resume-p4-bus-ch-provider-full.txt`. Sole DSH writer. **No goal created. No subagents created.** Serial work throughout (one VM gate at a time). VM timezone UTC+7; local Windows +08 (Z is UTC).

## 0. One-line status

The three gate defects the previous handover named are fixed with measured evidence, not argument: the **bus** negative window now starts **after** the invalidation (v5 shows the polluted pre-mutation window next to the clean post-mutation one), the **ClickHouse** round is driven by an input that really makes the handler query ClickHouse and by a counter that flushes `system.query_log` before counting, and the **provider** question is answered by measurement (the outbound-call instrument is provably live; no gated write route in the frozen matrix makes a synchronous outbound provider call, so its provider aspect is NOT_VERIFIED). Gate lineage: v3 `PASS=44 FAIL=0 NV=8` (two measurement defects of its own, preserved) -> v4 `PASS=46 FAIL=2 NV=5` -> **v5 `PASS=53 FAIL=2 NV=2`**. The first real **44-route matrix** round is on record: **44/44 attempted, PASS=12 FAIL=0 NOT_VERIFIED=32**. Two NEW real defects were found and are OPEN: **P4-CH-01** a refused (409) request still issues a 21-second `SELECT count() ... FINAL` against production ClickHouse, and **P4-ANCHOR-01** `POST /api/admin/v1/audit/anchors` refuses every legitimate admin request with 409 and `gate_error: failed to deallocate cached statement(s): conn busy`. **RELEASE-DECISION remains NO-GO**; nothing was deployed.

## 1. Runtime identity (measured at delivery)

    API_PID=1922757   API proc SHA == disk SHA 910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38   (UNCHANGED, not deployed)
    WORKER_PID=4011175  WORKER SHA 513e67bd7e9d4a9f96aeb659a237ca2399e7fc2074fdfe0978d0f6d8e6dbdb48        (UNCHANGED)
    units active active ; /health/ready = 200 ; deploy-nats-1 paused=false
    candidate processes 0 ; listeners on 1819x = 0 ; exports rows 350 (was 346 at session start)
    provider_descriptors ankr-multichain priority=10 revision=19   (restored, see 3.3)
    local dirty worktree preserved: 249 status lines, HEAD 51596f7, nothing committed/reset/checked out
    NOTE: scripts/s7/ is entirely untracked locally (`?? scripts/s7/`), so every script below is a new file.

## 2. Independent re-read of the two items the previous session closed (raw evidence, not trusted)

Nothing was redone and nothing was rewritten; the raw artifacts were re-read on the VM.

**(1) Export-pipeline capacity — CLOSED, re-verified.** The heavy multi-dataset ZIP object was re-read **through the S3 API with boto3**, not from the data directory:

    exports/b3923b12-094e-4ef4-a786-fc8d2bd9b51b.zip   content_length=77424289  etag="6538e02daab4fe65e9bdcf8fe1545be1-1"  head4=504b0304
    read_bytes=77424289  sha256=a9e6ac3d890e1dcebc0ac5b9c612989aa8c0a5e1d282a20cb05c0ea00bf70023
    exports row: b3923b12-... | COMPLETED | exports/b3923b12-....zip | 2000000 | a9e6ac3d890e1dcebc0ac5b9c612989aa8c0a5e1d282a20cb05c0ea00bf70023 | zip=t
    => the S3 view and the PostgreSQL `exports.sha256` MATCH.
    entries: manifest.json 392 B crc 7cd10674 ; addresses/0x..01/transactions.csv 0 B crc 00000000 ;
             addresses/0x..01/token_transfers.csv 844692978 B crc 9bdf952a ; testzip=None ; every entry re-read CRC-OK

**(2) ZIP pre-Put intent race — CLOSED, re-verified.** v5 `PASS=108 FAIL=3` and v6 `PASS=111 FAIL=0 NV=0` both still on disk; the v6 tail re-read (heavy positive COMPLETED at 145 s, real `.zip` object, negative 409 with delta=0 / new_names=0 / rows 346->346 / deny 47->48, `pipeline_aborts_logged=9`).

Source SHAs re-checked and identical to the previous handover table: `ch.go` 0ea50bf8..., `query.go` 9b609b4a..., `export.go` d50bdbee..., `export_v2.go` 43617740..., `export_object_store.go` 876c768a..., `export_intent_contract_test.go` 8d944695..., `ch_export_bound_test.go` c2308b7d..., `export_scan_slot_test.go` 9d2ae088..., gate v6 d99edda4..., gate v5 4a0bf47e..., NATS gate v3 b7729549....

## 3. The three known gate defects — FIXED, with the instrument facts established first

Before touching the gate, three bounded probes were run and their evidence kept (no product code changed by any of them):

| probe | script sha256 | evidence | what it established |
| --- | --- | --- | --- |
| ClickHouse driver | 8140f4c65f101ae8915a601e70cc49d17e266ee40ebf0464b7a2a90d04c87b31 | `p4-chprobe-20260915T030331Z` | the covered preview issues **0** CH queries; an uncovered address **with the default (unbounded) range** also issues 0, because `wantInterval` returns bounded=false and no gap is computed |
| instruments | 196b380ed61769271576ae2cd8edb8bc3cbe3bdcc519c70c63acc91c1da914cf | `p4-instrprobe-20260915T030554Z` | **an uncovered address plus `range.type=block` issues the real query** `SELECT count() FROM blockchain.token_transfers FINAL WHERE chain='bsc' AND (from_address IN (['0x..ff']) OR ...)`; missing_blocks=1000001 vs 0 for a covered address with the same range |
| provider | 3c294c35e8d0e4d35f6b840c749d5ea9ddbc67340c6a7979c87110cdffb48660 | `p4-provprobe-20260915T030809Z` | the counter is live (idle +8/20 s; `GET /api/v1/addresses/bsc/<fresh>/runtime` +6) and the provider-classified WRITE routes measure **0** with the same live instrument |

### 3.1 Bus — the counter window now starts after the invalidation

v2's negative charged the refused request with the **invalidation's own publish** (self-service logout publishes on `events.sse.scope_invalidated`). v5 opens two windows on the same negative and asserts only on the second:

    r1_positive http=200 revoked=1 scope_subject_messages=1
    r1_negative caller_logout=200 http=409 caller_revoked=1 victim_revoked=0
    r1_negative window_whole=1 window_after_invalidation=0
    r1_negative timings req_start=1789442324113 req_done=1789442332138 invalidated_at=1789442325130 req_elapsed_ms=8025
    PASS 1: the corrected window opened before the refused request returned (it could have observed a leak)
    PASS 1: the refused request published ZERO messages on events.sse.scope_invalidated after the mutation
    PASS 1: the bus counter discriminates (positive 1, negative-after-mutation 0)
    PASS 1: the pre-mutation window is measurably polluted by the invalidation own publish (1)

### 3.2 ClickHouse — driver fixed by the INPUT, counter fixed by SYSTEM FLUSH LOGS

The route was right and the input was wrong: `POST /api/v2/downloads/preview` only reaches `downloadEstimate` (the only ClickHouse query on that path) when `missing_blocks > 0`, and `missing_blocks` is only computed for a **bounded** range. v5 sends `range.block(1000000..2000000)` for an address with no local coverage:

    r2_positive http=200 missing_blocks=1000001 estimate_basis=local_index_density + historical_provider_usage ch_select_queries=0->1 delta=1
    --- positive window queries ---
    2026-09-15 03:26:06  24281  SELECT count() FROM blockchain.token_transfers FINAL WHERE chain='bsc' AND (from_address IN (['0x..ff']) OR ...
    r2_control http=200 missing_blocks=0 ch_select_queries=0->0 delta=0        (instrument discriminates)
    r2_negative switch=200 http=409 ch_select_queries=0->1 delta=1             (real finding, see 4.1)

The counter itself was also defective: `system.query_log` is flushed **asynchronously** (default 7500 ms), so v3 counted before the row was visible and reported delta=0 -> NOT_VERIFIED. v4/v5 run `SYSTEM FLUSH LOGS` before every count.

### 3.3 Provider — the honest answer is that no gated write route calls out

The counter now reads HTTP-level series only (`ankr_request_duration_seconds_count` + `rpc_batch_requests_total` + `rpc_duration_seconds_count`; `rpc_batch_calls_total` is per-method, not per-request, and is printed as context). v5:

    r4_calibration_receipt http=404 provider_calls=30->31 delta=1     (GET /api/v1/transactions/bsc/<random 32B hash>/receipt: a real cache-missing outbound call)
    r4_calibration_runtime http=200 delta=3                           (GET /api/v1/addresses/bsc/<fresh>/runtime: Head + AddressType + BalanceRead)
    r4_repeat http=200 delta=1
    r4_write_route_positive http=200 provider_calls=35->35 delta=0    (POST /api/v2/downloads/validate-addresses)
    NOT_VERIFIED 4: POST /api/v2/downloads/validate-addresses makes no outbound provider call (measured 0 with a live, calibrated instrument
                   and a cache-missing address): its provider-call classification in the frozen matrix is static reachability, not a call

The same measurement was taken for the other provider-classified write routes in `p4-provprobe-20260915T030809Z`:

    POST /api/v1/admin/tokens/bsc/<fresh>/resolve   -> 202, delta 0
    POST /api/v1/admin/methods/signatures           -> 201, delta 0
    PATCH /api/admin/v1/providers/ankr-multichain   -> 200, delta 0
    POST /api/v2/downloads/validate-addresses       -> 200, delta 0

Code reading agrees: inside `backend/internal/api` the only provider network calls are `s.deps.Head.Head`, `s.deps.AddressType.*`, `s.deps.BalanceRead.Native`, `s.deps.ChainRead.Receipt`, `s.deps.Trace.*` (rpc.go / actions.go — all GET routes) and `MethodData.DecodedInputsByTxHashes` (a ClickHouse store, not a provider). The provider-classified write handlers only touch local state: `TokenMeta.Enqueue` is a single `INSERT ... ON CONFLICT` (tokenmeta/service.go:209), `Registry.Refresh` reads the local store, `provider_descriptors` is a database row. **The matrix "provider-call" token is static reachability of `s.deps.TokenMeta`/`s.deps.Registry`/... and reachability is not a call.**

**Production state change made by the probe, and restored:** the write-route measurement had to exercise a real route, so `PATCH /api/admin/v1/providers/ankr-multichain` changed **global** control-plane state (priority 10 -> 1, revision 17 -> 18, audit seq 54267). It was restored through the same real route (priority 10, revision 19, audit seq 54268) with `scripts/s7/p4-provider-priority-restore-v1.sh` **13a5c4b9c82afe7fa504689b63a9ad7e31b53adaba4c9616e95f804a592c4673**; evidence `p4-provrestore-20260915T031025Z`. The audit trail shows both the change and the restore; nothing was deleted or rewritten.

## 4. Two NEW real defects found by the fixed instruments (OPEN, not fixed)

### 4.1 P4-CH-01 — a REFUSED request still issues a 21-second ClickHouse scan

Reproduced in v4 and v5 (identical numbers both times). The refused (409, rolled back in PostgreSQL) preview still runs `downloadEstimate`'s ClickHouse query:

    r2_negative switch=200 http=409 ch_select_queries=0->1 delta=1
    --- negative window queries (a refused request must produce NONE) ---
    2026-09-15 03:26:55  21177  SELECT count() FROM blockchain.token_transfers FINAL WHERE chain='bsc'
                                        AND (from_address IN (['0x00000000000000000000000000000000000000ff']) OR to_address IN (...))
    FAIL 2: the refused preview issued ZERO ClickHouse queries (got=1 want=0)

The query text names the **exact address of the refused request**, and its `query_duration_ms` is **21177** — a 21-second `FINAL` scan over an 11.9 M-row table, charged to a request the platform refused. This is a read, not a state change; the defect is unbounded external work attributable to a refused request, and it is exactly the "gate-recheck" mitigation the matrix already flags as absent for these routes. Fix direction: revalidate the session scope immediately before the estimate query (the gate has no reusable recheck helper yet — `scopeCommitGateRecheck`/`reverifyScopeToken` appear in the matrix tokens but **exist nowhere in the code**).

### 4.2 P4-ANCHOR-01 — `POST /api/admin/v1/audit/anchors` refuses every legitimate request

Found while isolating a suspected object leak on matrix route 18. Two dedicated probe rounds, the second with the calibration seam **disabled for that route** (`CALIB_ROUTE=/api/v1/__no_such_route__`), both behave identically:

    positive_http=409  {"error":{"code":"AUTHZ_WORKSPACE_SCOPE_STALE", ...}}
    negative_http=409
    before/after object counts: blockchain-audit 57/57, blockchain-exports 1572/1572, blockchain-raw 597699/597699  (NO object written either way)
    anchor_rows_before=8  anchor_rows_after=8

The denial reason is in the audit trail (5 occurrences in 6 hours, **all** on this route):

    seq 54387..54390 | authorization.denied | /api/admin/v1/audit/anchors | session_scope_unverifiable
      detail: {"gate":"commit","reason":"session_scope_unverifiable",
               "gate_error":"failed to deallocate cached statement(s): conn busy",
               "old_workspace_id":"49a4ea85-268d-4ca3-b211-cc08cf3b6631"}

So a legitimate owner/platform_admin request is refused 100 % of the time, and the refusal is a fail-closed gate refusal whose underlying error is a pgx connection-busy condition (`Conn.Deallocate` while the connection still has a statement in flight). Root cause is **not yet proven**; the supported hypothesis is that the handler's audit archiver (`audit.NewArchiver(s.deps.Store.Pool, s.deps.Raw, ...)`, no goroutines of its own) leaves rows/a statement in flight on the gate's request transaction, so the gate's release read cannot deallocate and fail-closes. This also means the audit-anchor feature (ADMIN-008 / §27.5 evidence) is currently **unusable**, and it is a P1-class availability defect.

The matrix object counter had also reported `neg_delta obj=10` for route 18; the isolated probe shows **zero** object change in every bucket, and the buckets were flat (1572/597699/57) over a 25 s control window, so that +10 was background traffic counted across three buckets (the matrix counter sums `blockchain-raw`, which the worker writes continuously). Next round: count per bucket and exclude `blockchain-raw`, and record object **names**, not counts.

## 5b. P2 — the B and E segments are now measured, and the routing budget is real

Gate `scripts/s7/c1-auth007-setlocal-timeout-gate-v3.sh` **59229f0e216ac3bc128080369c7852c7a04032b4074b80f7ff58a7b27ffcc5fe** (local == VM, `bash -n` rc=0), evidence `acceptance-evidence/dual-system-v1/sprint7/c1-auth007-setlocal3-<ts>`. Result **PASS=60 FAIL=1 NV=2**. The v2 round (PASS=40 FAIL=7 NV=1) is preserved unchanged.

**A (lock_timeout) — CLOSED again, unchanged in substance:** `A code=409 elapsed_ms=1577 created=0`, audit row `AUTHZ_WORKSPACE_SCOPE_STALE | session_scope_unverifiable | ERROR: canceling statement due to lock timeout (SQLSTATE 55P03)`, and the waiting backend was attributed with `application_name=bpgate:<request id>` and observed in a `Lock` wait.

**B (statement_timeout) — the object of the assertion was wrong in v2 and is now right.** v2 took `LOCK TABLE cases IN ACCESS EXCLUSIVE MODE` **before** the request, so the blocked statement was the pre-gate authorization read. v3 starts the request first with the calibration seam at phase `post` (9 s), proves **no** ACCESS EXCLUSIVE lock is held at that moment, then takes the lock while the request sits inside the gate critical section, and proves the lock is held. Measured:

    B code=500 elapsed_ms=10539 created=0 calib_delay_ms=9000 statement_bound_ms=1500
    PASS B: a handler statement past the bound is refused fail-closed (500)
    PASS B: the request lasted the 9 s seam plus about the configured 1500 ms statement bound (10539 ms), not the 60 s lock bound
    PASS B: the timed-out statement left no case row behind (fail closed)
    PASS B: the waiting handler backend was attributed to the request while it waited
    PASS B: the attributed backend was observed in a Lock wait

The one FAIL (`B: deny audit row is wrong (0|||)`) is a **gate assertion defect, not a product failure**: a statement timeout inside the handler surfaces as `500 INTERNAL_ERROR`, which is not an authorization denial, so no `authorization.denied` row exists. The next round should accept "no deny row for a fail-closed 500" instead of demanding one (the same assertion is still correct for A's 409).

**C (idle_in_transaction) — CLOSED:** `C code=409 elapsed_ms=9065 created=0`, the gate backend was observed `idle in transaction` and was gone afterwards, audit gate_error `conn closed`.

**E (real routing budget) — FIXED and measured.** The fixture now carries a `platform_admin` identity (owner/platform_admin hold all four permissions the segment needs) and the port is re-checked before the candidate starts:

    admin_audience_login_status=200 identity=platform_admin
    route=audit/verify                  status=200 elapsed_ms=3303  statement_budget_ms=60000
    route=audit/integrity/preview       status=200 elapsed_ms=38
    route=restores/preview              status=400 elapsed_ms=16    (needs parameters the gate did not send)
    route=control/kill-switch/preview   status=409 elapsed_ms=31    (an engage kill switch is already active)
    measured_max_ok_route_ms=3303

So a **real routing budget exists now**: the slowest measurable long admin route is `POST /api/admin/v1/audit/verify` at **3303 ms** against the 60 s gate statement bound; the other two need a valid body / an inactive kill switch before their budget can be derived.

**F (new, availability) — MEASURED, and it confirms the open problem.** With no calibration seam at all, an ordinary case creation was blocked by an external `LOCK TABLE cases IN ACCESS EXCLUSIVE MODE`:

    F code=201 elapsed_ms=9023 created=1
    PASS F: the request eventually succeeded once the lock was released
    PASS F: the request was postponed by the ACCESS EXCLUSIVE lock (9023 ms) instead of failing fast
    NOT_VERIFIED F: no sample caught the pre-gate Lock wait (sampling only)   <- the sampler only records bpgate:* backends
    F_observation: the blocked statement is outside the gate transaction, so the gate statement_timeout cannot bound it;
                   the application pool carries no statement_timeout and PostgreSQL's own default is 0

That is a **real availability weakness, measured**: any long `ACCESS EXCLUSIVE` lock on `cases` postpones ordinary requests by the whole lock duration and they then commit normally. The next round needs a sampler that records every non-idle backend (the v2 diagnostic script `setlocal-b-blocker-diag-v1.sh`, sha 5546ae5d..., already samples all backends) so the exact blocked statement text is captured as evidence rather than inferred.

## 5. The 44-route matrix — first real round

Script `scripts/s7/p4_route_matrix_gate_v1.py` **f21609cc98d12490cb1c0cfb80ea33fd5faf6b6db6221632d9b86487b98e0021** (`py_compile` clean, local == VM), candidate `api-expcap-r3-calib` 1829a1db..., port 18196, calibration seam phase=post / 4000 ms. Evidence `acceptance-evidence/dual-system-v1/sprint7/p4-routes-p4routes-20260915T033722Z` (`matrix-console.log`, `results.json`, `candidate.log`).

    ROUTES_TOTAL=44 PASS=12 FAIL=0 NOT_VERIFIED=32
    production_api_sha_after=910b7ca2...  production_ready_after=200

Per route it runs a POSITIVE (real request, real effect measured across bus / ClickHouse select+insert / object / provider) and then a NEGATIVE (same request, caller scope invalidated during the seam, bus counter window re-opened **after** the mutation). A route that cannot be exercised is NOT_VERIFIED with the exact reason; a route whose positive moved a class and whose negative moved the same class is FAIL.

**PASS (12) — real positive and a refused negative with zero delta on every class that moved:**

| # | route | positive | refused | positive effects observed |
| --- | --- | --- | --- | --- |
| 0 | POST /api/v1/download-jobs/{id}/pause | 200 | 409 | none (bus 0) |
| 1 | POST /api/v1/download-jobs/{id}/resume | 200 | 409 | none |
| 8 | POST /api/v1/exports | 202 | 409 | **object +7**, then negative delta 0 |
| 11 | POST /api/v1/admin/methods/signatures | 201 | 409 | none |
| 12 | POST /api/v1/admin/contracts/{chain}/{addr}/abi | 201 | 409 | none |
| 25 | POST /api/admin/v1/feature-flags/{key}/commands/evaluate | 200 | 409 | none |
| 28 | POST /api/admin/v1/deletion-requests | 201 | 409 | none |
| 30 | POST /api/admin/v1/backups (target=config, drill) | 201 | 409 | none |
| 35 | POST /api/admin/v1/budgets/{id}/commands/evaluate | 200 | 409 | none |
| 39 | POST /api/v2/downloads/validate-addresses | 200 | 409 | none |
| 40 | POST /api/v2/downloads/preview | 200 | 409 | none |
| 43 | POST /api/v2/exports/{id}/download-ticket | 200 | 409 | none |

**NOT_VERIFIED (32), by reason group:**

* *deliberately negative-only* (the positive would perform an irreversible or global production action; the refused control still ran and still returned 409): 2 cancel, 3 guard/continue, 4 guard/abort, 6 performance-mode, 7 ankr benchmark (real provider quota), 14-17 audit-integrity request/approve/reject/execute (audit-history state), 18 audit anchor, 20/21 kill-switch approve/reject, 22 execute (would stop production jobs), 23 restore, 29 deletion approve (would enable real deletion), 33 restores create, 37 api-key revoke (would break the MCP/GPT Actions key), 41 POST /api/v2/downloads (would spend real provider quota in the worker).
* *request defect in the runner (fixable next round)*: 5 (the `subject` path parameter must be a **subject UUID**, not an address — the handler returned `invalid input syntax for type uuid`), 9 + 10 (`{fresh_token}`/`{fresh_contract}` placeholders were never resolved by the runner), 13 (`{victim_session}` placeholder unresolved -> 404), 27 + 38 (428 `If-Match` revision required), 36 (`priority` was sent as a JSON string -> 400), 42 (v2 exports body shape -> 400).
* *genuine external blocker*: 19 (an engage kill switch is already **active**, so a new engage request is correctly refused 409 CONFLICT_STATE), 24 (no row exists in `api_control_alerts`), 31 (`backup_jobs` is empty), 32 (`restores/preview` needs parameters the runner did not send -> 400), 34 (`audit/export` -> 403).

Runner defects that were found and fixed inside the session (both preserved as evidence):

* `p4-routes-p4routes-20260915T033517Z` is the aborted first attempt: the runner parsed `deploy/.env` line by line in Python, but the file contains a backslash-continued value, so `ANKR_MANAGEMENT_ENCRYPTION_KEY` arrived 1 character long instead of 36 and the candidate refused to boot (`config invalid`). The runner now launches the candidate through `bash -c 'set -a; . deploy/.env; set +a; ...'`, exactly like the bash gates.
* the matrix object counter sums three buckets, so `blockchain-raw` worker traffic contaminates every object delta (see 4.2).

## 6. Red lines respected (measured)

Not deployed, not restarted: API/worker PIDs and SHAs unchanged; no TRUNCATE / reset / clean / drop / default delete; no object, bucket, evidence directory, old binary, log or backup deleted (the leaked object `exports/4a380557-...zip` is still in place); `object_ref` never edited; no legacy SHA/seq backfilled; no audit history fabricated (every audit delta is the gates'/fixtures'/probes' own real write: `audit_events_v2` 52047 rows, max seq 54390); the local dirty worktree is untouched apart from the new untracked scripts; no credential, Cookie, CSRF token or DSN printed anywhere; old sessions not woken; no goal and no subagent created.
Production-dependency actions this session: read-only ClickHouse queries (`SYSTEM FLUSH LOGS`, `system.query_log` counts) and read-only S3 GETs; the real `docker exec` psql reads; and exactly **two** real control-plane writes, both fixture/global-config: the provider priority change and its restore (§3.3). **No production dependency was paused, stopped or restarted this session.** NATS `paused=false` at delivery.

## 7. Artifacts added this session (local == VM byte-for-byte; all under `scripts/s7/`, which is untracked)

    p4-ch-driver-probe-v1.sh                    8140f4c65f101ae8915a601e70cc49d17e266ee40ebf0464b7a2a90d04c87b31   (new)
    p4-instrument-probe-v2.sh                   196b380ed61769271576ae2cd8edb8bc3cbe3bdcc519c70c63acc91c1da914cf   (new)
    p4-provider-probe-v1.sh                     3c294c35e8d0e4d35f6b840c749d5ea9ddbc67340c6a7979c87110cdffb48660   (new)
    p4-provider-priority-restore-v1.sh          13a5c4b9c82afe7fa504689b63a9ad7e31b53adaba4c9616e95f804a592c4673   (new)
    p4-external-side-effect-gate-v3.sh          fd6fab73cbd11bed39bedcd8757377ea997f7d241762d3d2180c285a3ab4d12a   (new, PASS=44 FAIL=0 NV=8; two measurement defects preserved)
    p4-external-side-effect-gate-v4.sh          4d93bb544c90d0c83f4eb122c485289465225f51b00d112fd3719ec4c7265f1b   (new, PASS=46 FAIL=2 NV=5)
    p4-external-side-effect-gate-v5.sh          0f7cba44f81ca1c280696a3bf1dd64927760ce44b037863b47b90668e0d0d74c   (new, **PASS=53 FAIL=2 NV=2**)
    p4-external-side-effect-gate-v6.sh          10304cb0d1080efe915f0c605bb8e368d5a2457d29d9b7a10254bbed0ff8d95d   (new, NOT YET RUN: anchors the export-row assertion; v5's FAIL 3 was a sliding-window measurement defect)
    p4_route_matrix_gate_v1.py                  f21609cc98d12490cb1c0cfb80ea33fd5faf6b6db6221632d9b86487b98e0021   (new, **44 routes: PASS=12 FAIL=0 NV=32**)
    p4-audit-anchor-negative-probe-v1.sh        08825103e409577c50bffdab6873fd5059d6f80920fdcb0ecf2cd33158cbeb23   (new, found P4-ANCHOR-01)
    c1-auth007-setlocal-timeout-gate-v3.sh      59229f0e216ac3bc128080369c7852c7a04032b4074b80f7ff58a7b27ffcc5fe   (new, P2: **PASS=60 FAIL=1 NV=2**; B now measures the handler statement bound, E derives a real budget, F measures the availability window)

**No product source file was changed this session.** Every frozen source SHA re-checked in §2 is unchanged, and `git` was not committed/reset/checked out.

Frozen evidence directories (all preserved, none rewritten):
`p4-chprobe-20260915T030331Z`, `p4-instrprobe-20260915T030554Z`, `p4-provprobe-20260915T030809Z`, `p4-provrestore-20260915T031025Z`,
`p4-ext3-p4ext3-20260915T031216Z` (v3), `p4-ext3-p4ext4-20260915T031817Z` (v4), `p4-ext3-p4ext5-20260915T032450Z` (v5),
`p4-routes-p4routes-20260915T033517Z` (aborted bootstrap), `p4-routes-p4routes-20260915T033722Z` (matrix v1),
`p4-anchorprobe-20260915T040351Z` (anchor probe, seam on), `p4-anchorprobe-20260915T040537Z` (anchor probe, seam off),
c1-auth007-setlocal3-20260915T041107Z (P2 v3: PASS=60 FAIL=1 NV=2, real routing budget 3303 ms, availability window 9023 ms).

## 8. Lifecycle state at delivery

    goal = null ; subagents = 0 ; background jobs = 0 (every SSH/pwsh call was foreground and has returned)
    candidate processes = 0 ; listeners 1819x = 0 ; ports 18195/18196/18197 released ; NATS paused=false ; ready = 200
    API 1922757 / 910b7ca2... ; worker 4011175 / 513e67bd... ; units active active
    exports rows 350 ; audit_events_v2 52047 rows / max seq 54390 ; provider priority 10 revision 19

## 9. What the next session must do (priority order)

1. **P4-CH-01 (§4.1) and P4-ANCHOR-01 (§4.2) are the two open real defects.** P4-ANCHOR-01 first: it is a deterministic 100 % refuser of a legitimate admin route, it is cheap to reproduce (`scripts/s7/p4-audit-anchor-negative-probe-v1.sh` with `CALIB_ROUTE` unset or set to a non-matching path), and its mechanism (`conn busy` inside the gate's release read) is a candidate root cause for anything else that streams on the request transaction. Then P4-CH-01: make the scope revalidation happen before the estimate query (no helper exists yet; adding one is the "gate-recheck" mitigation the frozen matrix counts as absent for 18 routes) — or record it as an accepted read-side cost with the supervisor's decision.
2. **Run gate `p4-external-side-effect-gate-v6.sh`** (already written and syntax-checked, sha 10304cb0...) to replace v5's last measurement-defect FAIL with the anchored export-row assertion, and keep v5 as the preserved round.
3. **Matrix round 2.** Fix the runner defects listed in §5 (subject UUID, `fresh_token`/`fresh_contract`/`victim_session` placeholders, `If-Match` revisions for the two PATCH routes, provider `priority` as a number, `restores/preview` parameters, v2 exports body) and re-run; that should move a large part of the 32 NOT_VERIFIED rows to PASS. Count objects **per bucket** and record names; never sum `blockchain-raw`. Where the positive really is unsafe (cancel, guard, execute, approve, revoke, create-job, benchmark), keep it NOT_VERIFIED and say why.
4. **P2 remainder** (§5b): B and E are now measured (B's statement bound fires at the configured 1500 ms after the seam and commits nothing; E derived 3303 ms for `audit/verify`). Still open: B's deny-audit-row assertion is wrong for a fail-closed 500; `restores/preview` needs a body and `kill-switch/preview` needs an inactive kill switch before their budgets can be derived; and F needs the all-backend sampler to capture the exact blocked statement. The availability weakness F measured (9.0 s postponement, then a normal 201+commit) is a **measured, unfixed** product-level issue: the application pool has no `statement_timeout` and the pre-gate authorization read cannot be bounded by the gate's own statement bound.
5. **P3 remainder** beyond the closed seam: long transactions, vacuum, bloat, spool, RSS, disk, ready.
6. **A5** browser React Query / multi-member / CSRF / revocation / four breakpoints; **ClickHouse P1 structural**; **auditverify DR**; **PostgreSQL / ClickHouse / MinIO / NATS backup-restore and kill-9 RPO/RTO**; **exports/provider/pipeline**.
7. **Plan sections 27, 28.9 and 31 and all of Sprint 7** (headings at plan lines 2092, 2369, 2478).
8. **Deployment: NO-GO.** P0/P1 are not provably 0: P4 has two open real defects, 32 of 44 routes are NOT_VERIFIED, and P2/P3/A5/CH-P1/DR/backups/Sprint 7 are untouched.

### Suggested first commands (all read-only)

    AP=$(systemctl --user show blockchain-api -p MainPID --value)
    sha256sum /proc/$AP/exe                      # must still be 910b7ca2...
    curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/health/ready
    pgrep -af '[a]pi-expcap' ; ss -ltn | grep -E ':1819[0-9] '
    cd /home/euripides/projects/blockchain-platform && sha256sum backend/internal/clickhouse/ch.go backend/internal/query/query.go \
      backend/internal/api/export.go backend/internal/api/export_v2.go
    docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc \
      "select provider_key||'|'||priority||'|'||revision from provider_descriptors"

## 10. Pitfalls confirmed this session (read before working)

* **`system.query_log` is flushed asynchronously** (default `flush_interval_milliseconds` 7500). Count it only after `SYSTEM FLUSH LOGS`, otherwise a query that really ran looks like delta 0 and the round becomes a false NOT_VERIFIED. This is exactly what v3 hit.
* **The ClickHouse counter must filter on the driver and the query text**: `type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query_kind='Select'` plus a dataset token. The worker's 320 inserts/10 min and the readiness probe's `SELECT 1` are the background traffic that makes a raw `query_log` count non-discriminating.
* **`POST /api/v2/downloads/preview` only queries ClickHouse when the request carries a BOUNDED block range.** `wantInterval` returns bounded=false for the default/auto range, `missingBlocksFromPreview` then sums zero gaps, `downloadEstimate` early-returns `basis=no_missing_blocks` — a fully uncovered address with no range still produces **0** ClickHouse queries. Always drive this route with `range.type=block` plus both block numbers.
* **A sliding `now() - interval '10 minutes'` window is not monotonic** and will make an equality assertion fail when a row ages out. Anchor the window with an explicit timestamp (fixed in gate v6).
* **The export create response carries a top-level `id`**, not `data.id` (the preview response *is* wrapped in `data`). Reading one shape for both silently yields `__ERR__`, and the poll loop then measures nothing while the export really completes.
* **`deploy/.env` contains a backslash-continued value**, so a naive line-by-line Python parse truncates `ANKR_MANAGEMENT_ENCRYPTION_KEY` to 1 character and the candidate refuses to boot. Launch candidates through `bash -c 'set -a; . deploy/.env; set +a; ...'`.
* **The provider counter series are not interchangeable**: `rpc_batch_calls_total` counts methods **inside** a batch, while `rpc_batch_requests_total` / `ankr_request_duration_seconds_count` / `rpc_duration_seconds_count` count outbound HTTP requests. Use the HTTP-level series for "did a call go out".
* **A cache-hit control is not "delta 0 on the second call"**: the chain-reader head refresh makes an immediate repeat call out again (measured +1). Only assert that the instrument moved at all, and report the repeat as an observation.
* **The commit gate's fail-closed refusal is 409 `AUTHZ_WORKSPACE_SCOPE_STALE` and its `detail` carries the real cause** (`gate_error`). Always read `audit_events_v2.detail` before interpreting a 409 — that is how `conn busy` was found. The `audit_events_v2` columns are `occurred_at` / `route_template` (there is no `created_at`).
* **The MinIO object counter must be per bucket.** `blockchain-raw` is written continuously by the worker (597 699 entries at the time of writing), so summing buckets fabricates deltas on routes that never touch object storage.
* `fixture-membership` resets the fixture passwords on every run; a probe that logs in must either re-read the creds file or reuse the password it just set (the gates' `clear_flag` pattern).
* The local/VM SHA parity check must tolerate the local file's trailing newline: uploading the read-back lines **without** an added newline matched byte-for-byte in most cases here, and one file needed the extra newline — verify with `sha256sum` after every upload instead of assuming.

---

This file is a new handover snapshot. No historical handover file was rewritten.