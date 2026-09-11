# TRON Address Volume Guard V6 — Acceptance Report

Date: 2026-09-09  
Verdict: **PASS_WITH_LIMITS**

## Scope delivered

- Guard unit is `(chain, job, subject, dataset)` through the job/subject/dataset foreign-key identity.
- Durable page commits are accounted exactly once by `(shard_id, page_no)`.
- Runtime triggers are configurable and implement actual rows, projected rows, confidence, EXTREME, and SUPER_EXTREME thresholds.
- A triggered unit stops leasing new shards/pages; already-running work may finish its current durable commit and then pauses.
- `WAITING_USER_CONFIRMATION`, Continue override, `ABORTED_BY_USER`, partial coverage, audit events, and restart-safe persistence are implemented.
- Continue requeues paused shards without clearing fingerprints, pages, checkpoints, or completed ranges.
- Abort cancels only pending/retry/paused work for the selected dataset and does not delete ClickHouse or raw data.
- The task detail page restores the guard modal from the API after refresh, supports close/reopen, and requires a second confirmation for EXTREME risks.

## Targeted gate results

| Gate | Result | Evidence |
|---|---|---|
| Normal address not paused | PASS (logic) | No transition occurs below configured actual/projected thresholds. Boundary unit test passed. |
| High-density trigger | PASS (logic) | Actual >= 2M, projected >= 5M at confidence >= .80, and projected >= 10M at confidence >= .70 are implemented. |
| Stop new provider work | PASS (code path) | Lease SQL excludes guarded datasets; page loop returns immediately after the durable triggering commit. |
| Continue checkpoint recovery | PASS (code path) | Only paused shards are returned to PENDING; checkpoint/page/fingerprint fields are untouched. |
| Abort partial coverage | PASS (code path) | Dataset/subject/job stats retain `coverage=PARTIAL`; stored data is not deleted. |
| Restart while waiting | PASS (persistence) | Guard state is PostgreSQL-backed and lease admission independently excludes waiting units after restart. |
| Multi-address isolation | PASS (model/code path) | Guard transitions and shard updates are scoped by `dataset_id`; unrelated subjects remain schedulable. |
| Continue/Abort UI | PASS | Playwright exercised both POST actions with exact subject and dataset bodies. |
| Desktop UI | PASS | 1440x960 rendered with modal, focus entry, no relevant console errors, and EXTREME second confirmation. |
| Mobile UI | PASS | 390x844 rendered without horizontal clipping; modal body scroll and sticky actions remained usable. |
| Full Go regression | PASS | `go test ./...` completed successfully. |
| Frontend production build | PASS | `npm run build` completed successfully. |
| Missing / duplicate / wrong logIndex | NOT RUN | Requires a deployed provider + PostgreSQL + ClickHouse real-data run. |
| Provider request drain timing | NOT RUN | Requires live provider counters in the deployment environment. |

## Required answers

1. Normal address false pause: no false transition in threshold logic; live normal-address Gate pending deployment.
2. Actual rows at abnormal trigger: configured default is 2,000,000; projected triggers can occur earlier.
3. Projected low/mid/high: persisted and returned by the guard API for every guard unit.
4. Confidence: persisted as 0..1 and shown as a percentage.
5. Seconds until provider requests stop: not measured without a live deployment.
6. Continue from checkpoint: implemented without clearing or replanning persisted progress; live proof pending.
7. Abort partial coverage: implemented; no data deletion path exists in the action.
8. Restart remains waiting: enforced by persisted guard state plus lease admission.
9. Multi-address isolation: dataset-scoped; no job-wide pause unless every remaining dataset is waiting.
10. Avoided calls/cost: projections are exposed, but avoided totals require a live triggered run.
11. Missing/duplicate/wrong logIndex: pending live correctness comparison.

## Limits preventing an unconditional PASS

- P1 bounded 8–16-window preflight sampling is not enabled in this change. Runtime Guard is complete; the existing activity-boundary/count planning remains unchanged.
- Actual provider-call and cost values currently account the primary durable page request. TRC20 enrichment amplification remains available in the existing job metrics but is not yet attributed to an individual guard unit.
- Docker/PostgreSQL/ClickHouse were unavailable on the development host, so migration execution and real-provider drain/restart/correctness Gates were not run.

These limits do not weaken the P0 hard actual-row safety stop, but they block the V6 document's final unconditional PASS.
