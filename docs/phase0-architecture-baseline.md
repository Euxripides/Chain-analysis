# Investigation Platform Phase 0 architecture baseline

Deployed baseline version: `1.0`.

This release establishes stable extension contracts without rewriting the
existing download, provider, query, export, or RPC paths.

## Implemented progression beyond the baseline

The first durable Phase 1/2 vertical slice is now deployed:

- Investigation runs persist their stages, steps, checkpoints, capability
  version, output hash, and algorithm version.
- Indexed data can be reused without starting a duplicate download.
- Completed download events resume waiting investigations; startup recovery
  reconciles terminal download jobs without a polling loop.
- Deterministic one-hop flow analysis materializes one confirmed Finding,
  transaction-bundle Evidence, and a versioned Graph Patch in one database
  transaction.
- Human review can approve a `waiting_review` investigation; both the
  Investigation and active Run become `completed` while artifacts remain
  immutable and queryable.
- `GET /api/v1/investigations/{id}/artifacts` returns the durable findings,
  evidence bundles, and graph patches.
- Phase 3 now provides case-scoped main and branch canvases. Canvas revisions
  persist node/edge evidence references, groups, annotations, filters, layout,
  camera, and view state with optimistic concurrency.
- Canvas snapshots are immutable copies of one exact revision. The fund-flow
  workspace can create a case, save the current graph as a canvas, branch the
  current selection, and save a snapshot through real APIs.
- Phase 4 adds a provider-neutral semantic projection over normalized
  transactions and token transfers. The projection classifies `SWAP`,
  `BRIDGE`, `DEPOSIT`, `WITHDRAW`, `ADD_LIQUIDITY`, `REMOVE_LIQUIDITY`, and
  `CONTRACT` events, and records confidence, provenance, source references,
  projection version, algorithm version, data version, and computation time.
- Semantic projections are fully rebuildable from normalized storage without
  downloading source data again. Ordinary transfers are not promoted to
  protocol events unless explicit method evidence or a multi-asset flow
  pattern supports the classification.
- Investigation runs now persist a fourth `semantic_protocol` stage and a
  `protocol.semantic.decode` step after the one-hop flow artifact is committed.
  The investigation workbench exposes the resulting counts and evidence
  boundary and can trigger a real projection rebuild.
- Phase 5 adds an `analysis.advanced_patterns` capability and rebuildable
  `advanced_investigation` projection. It computes per-asset inflow, outflow,
  and net flow; deterministic `FAN_IN`, `FAN_OUT`, `CONSOLIDATION`,
  `DISTRIBUTION`, and `RAPID_FORWARD` signals; and separately marked
  direct-counterparty relationship heuristics.
- Net asset flow is stored as `NET_FLOW_ONLY` and is never presented as
  realized profit without historical price and acquisition-cost evidence.
  Related-wallet scores are marked `INFERRED`, not identity or common-control
  conclusions. Phase 5 signals become candidate Findings with their own
  Evidence and provenance records.
- Phase 6 report generation now renders a deterministic evidence-indexed
  Markdown artifact from durable Investigation, Finding, Evidence, Graph Patch,
  and Canvas Snapshot records. The report bytes live in the dedicated
  `blockchain-reports` object bucket; PostgreSQL stores only the object key,
  SHA-256 hash, size, revision, metadata, and an audit event.
- Phase 7 introduces provider-neutral multi-subject and entity-relationship
  persistence for `ADDRESS`, `DOMAIN`, `IP`, `SOCIAL_ACCOUNT`, `APK`,
  `ORGANIZATION`, `FILE`, `CONTRACT`, and `TRANSACTION`. Every subject and
  relationship records source, confidence, evidence references, and provenance;
  user-entered clues remain distinct from provider or OSINT verification.

This slice does not claim multi-hop tracing, entity/label attribution,
protocol-specific ABI coverage for every DEX or bridge, cross-chain message
correlation, report generation, or OSINT support. Phase 3 group/annotation
persistence exists; direct visual editing tools for those two structures
remain a later UI increment.

## Stable contracts

The provider-neutral definitions live in
`backend/internal/platform/contract`:

- `SubjectRef`, `RequestContext`, `DataRequirement`
- `SemanticEvent`, `EventEnvelope`
- `GraphNode`, `GraphEdge`, `GraphPatch`
- `Finding`, `Evidence`
- `Capability`, `Job`
- `ChainAdapter` is represented as an interface boundary; the existing chain
  registry/read services remain its production implementation path.

All contracts start at version `1.0`. Future changes must add optional fields,
new versions, or new implementations rather than mutate existing semantics.

## Runtime baseline

- Version-aware, provider-neutral `CapabilityRegistry`.
- Durable Investigation state with revision-based optimistic concurrency.
- Idempotent Investigation creation through `Idempotency-Key`.
- Transactional Outbox with JetStream message-ID deduplication and retry.
- Unified background-job storage contract (`platform_jobs`) with priority,
  resource class, leases, retries, checkpoints, and request context.
- Schema and Projection registries, including rebuild checkpoints.
- Durable Finding, Evidence, and Graph Patch stores.

## API

- `GET /api/v1/system/architecture`
- `GET /api/v1/capabilities`
- `POST /api/v1/investigations`
- `GET /api/v1/investigations/{id}`
- `POST /api/v1/investigations/{id}/start|pause|resume|cancel`
- `GET|POST /api/v1/cases/{id}/canvases`
- `GET|PATCH /api/v1/canvases/{id}`
- `POST /api/v1/canvases/{id}/branch`
- `GET|POST /api/v1/canvases/{id}/snapshots`
- `GET /api/v1/analysis/addresses/{chain}/{address}/semantic-events`
- `POST /api/v1/analysis/addresses/{chain}/{address}/semantic-events/rebuild`
- `GET /api/v1/analysis/addresses/{chain}/{address}/advanced`
- `POST /api/v1/analysis/addresses/{chain}/{address}/advanced/rebuild`
- `GET|POST /api/v1/investigations/{id}/reports`
- `GET /api/v1/reports/{id}/download`
- `GET|POST /api/v1/investigations/{id}/subjects`
- `POST /api/v1/investigations/{id}/relationships`

State-changing requests may send the current revision as a standard quoted
`If-Match` value. Investigation creation accepts `Idempotency-Key`.

## Migration

`postgres/0028_investigation_phase0.sql` is additive and online. It does not
delete or rewrite existing download jobs, coverage, raw objects, normalized
data, or provider configuration.

`postgres/0030_canvas_phase3.sql` is also additive. It adds durable canvas and
snapshot tables plus `canvas.state` and `canvas.snapshot` schema registrations.

`postgres/0031_semantic_engine.sql` is additive. It adds the rebuildable
semantic-event projection and registers the `protocol.semantic_event` schema
and `protocol_semantics` projection at version `1.0`.

`postgres/0032_semantic_projection_state.sql` persists projection metadata
independently from event rows, so an evidence-backed zero-event result still
retains its data version, algorithm version, event count, and computation time
across process restarts.

`postgres/0033_advanced_investigation.sql` adds the rebuildable Phase 5 result
projection and registers `analysis.advanced_result` version `1.0`.

`postgres/0034_investigation_reports.sql` adds versioned report metadata while
the report body remains in object storage, as required by the storage boundary.

`postgres/0035_multi_subject_osint.sql` adds typed investigation subjects and
evidence-bounded relationships without changing Graph Core.

The duplicate legacy performance migration was also made order-independent:
`ankr_scheduler_state` remains owned by the per-worker telemetry migration,
while SAFE/FAST/TURBO controls remain in `ankr_performance_control`.

## Explicit boundary

This architecture baseline now includes the deployed Phase 1 durable runtime,
Phase 2 one-hop artifacts, Phase 3 case canvases, and the first provider-neutral
Phase 4 semantic projection, and the first evidence-bounded Phase 5 advanced
analysis slice. Multi-hop peel-chain confirmation, label-backed exposure,
price/cost-basis profit, reporting, and OSINT remain on the same stable
contracts and persistence extension points.
