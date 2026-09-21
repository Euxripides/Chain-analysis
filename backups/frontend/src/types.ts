export interface Job {
  id: string
  job_no: string
  status: string
  priority: number
  source: string
  chain: string
  range_type: string
  from_block?: number | null
  to_block?: number | null
  from_timestamp?: string | null
  to_timestamp?: string | null
  datasets: string[]
  reuse_policy: string
	head_block: number
	requested_mode: PerformanceMode
	effective_mode: PerformanceMode
	mode_reason: string
	mode_changed_at?: string | null
  error: string
  stats: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface Subject {
  id: string
  job_id: string
  subject_type: string
  subject: string
  status: string
  progress: number
  stats: Record<string, unknown>
}

export interface DatasetItem {
  id: string
  job_id: string
  subject_id: string
  dataset: string
  status: string
  progress: number
  weight: number
  stats: Record<string, unknown>
}

export interface Shard {
  id: string
  job_id: string
  subject_id: string
  dataset_id: string
  dataset: string
  from_block: number
  to_block: number
  page_token: string
  page_no: number
  page_size?: number
  status: string
  priority: number
  retries: number
  max_retries: number
  downloaded_rows: number
  downloaded_bytes: number
  raw_object: string
  error: string
  updated_at: string
  // V1.1（§40 Range Detail）
  page_count?: number
  wire_bytes?: number
  decoded_bytes?: number
  page_gap_avg_ms?: number
  fetch_duration_ms?: number
  ingest_duration_ms?: number
}

export interface JobDetail {
  job: Job
  subjects: Subject[]
  datasets: DatasetItem[]
  shards: Shard[]
	progress: Snapshot | null
	performance?: PerformanceModeState
  planning_api_requests?: number
  planning_api_cost_usd?: number
  acquisition_api_cost_usd?: number
}

export type PerformanceMode = 'safe' | 'fast' | 'turbo'

export interface AnkrProductionProfile {
	version: string
	default_mode: PerformanceMode
	hard_ceiling: number
	reject_boundary: number
	memory_budget_bytes: number
	absolute_rss_limit_bytes: number
	page_size_default: number
	request_timeout_seconds: number
	modes: Record<PerformanceMode, { target_concurrency: number }>
}

export interface PerformanceModeState {
	requested_mode: PerformanceMode
	effective_mode: PerformanceMode
	target_concurrency: number
	active_concurrency?: number
	hard_ceiling?: number
	mode_reason?: string
	mode_changed_at?: string
}

export interface CountOracleStatus {
  oracle_enabled: boolean
  key_configured: boolean
  count_aware_sharding_enabled: boolean
  shadow_mode: boolean
  planning_deadline_ms: number
  count_timeout_ms: number
  count_workers: number
  endpoint: string
  zero_verify_enabled: boolean
  calibration_enabled: boolean
  health?: {
    provider: string
    circuit_state: string
    requests: number
    success_count: number
    p50_ms: number
    p95_ms: number
    success_rate: number
    cache_hit_rate: number
    quota_exhausted: boolean
    last_error: string
    false_zero_count: number
    prediction_error_p50: number
    updated_at: string
  }
}

export interface SchedulerState {
  worker_id: string
  active_slots: number
  weighted_slots: number
  concurrency_target: number
  hard_ceiling: number
  ramp_level: number
  ramp_phase: string
  control_state: string
  control_reason: string
  goodput: number
  ttfb_p50_ms: number
  mem_used_bytes: number
  spool_used_bytes: number
  time_to_full_safe_concurrency_ms: number
  updated_at: string
}

export interface GlobalConcurrencyInfo {
  hard_ceiling: number
  auto_tune: boolean
  ramp_steps: number[]
  normalization_enabled: boolean
  state?: SchedulerState | null
  profile?: {
    preferred_global_concurrency: number
    p50_ms: number
    p95_ms: number
    goodput: number
    updated_at: string
  } | null
}

export interface Snapshot {
  job_id: string
  status: string
  progress: number
  downloaded_rows: number
  downloaded_bytes: number
  api_requests: number
  api_cost_usd: number
  rows_per_sec?: number
  write_rows_per_sec?: number
  bytes_per_sec?: number
  req_per_sec?: number
  coverage_hit_ratio?: number
  elapsed_seconds?: number
  eta_seconds?: number
  total_shards?: number
  done_shards?: number
  ts: string
  // V1.1 三阶段进度 + 控制状态 + 瓶颈（§29-§38）
  fetched_rows?: number
  ingested_rows?: number
  fetched_shards?: number
  ingested_shards?: number
  validated_rows?: number
  validated_shards?: number
  validated_pages?: number
  validation_status?: string
  control_state?: string
  control_reason?: string
  bottleneck?: string
  bottleneck_detail?: string
  provider_latency_avg_ms?: number
  page_gap_avg_ms?: number
  page_size_current?: number
  tail_eta_seconds?: number
  retry_amp?: number
  active_shards?: number

  // V1.2（§74/§75/§80/§85）：全局并发 + 规划/采集成本分账
  global_active_slots?: number
  global_weighted_slots?: number
  global_concurrency_target?: number
  global_ceiling?: number
  global_ramp_phase?: string
  planning_api_requests?: number
  planning_api_cost_usd?: number
  acquisition_api_cost_usd?: number
  time_to_first_fetch_ms?: number
  time_to_full_safe_concurrency_ms?: number
	provider_usage_60s?: ProviderUsageWindow[]
	provider_usage_5m?: ProviderUsageWindow[]
}

export interface ProviderUsageWindow {
	provider: string
	usage_type: string
	requests: number
	rows: number
	qps: number
	p50_ms: number
	p95_ms: number
	p99_ms: number
	errors: number
	rate_limits: number
	retries: number
}

// V1.2 Planner Card 类型（§76/§77/§79：dataset.stats 中的 estimate 对象）
export interface DatasetEstimate {
  source?: string
  predicted_rows?: number
  predicted_pages?: number
  profile_hit?: boolean
  preferred_shards?: number
  initial_shards?: number
  decision_reason?: string
  ts?: string
}
export interface DatasetActual {
  actual_rows?: number
  deviation_pct?: number
  actual_recorded_at?: string
}

export interface CoveragePreview {
  chain: string
  datasets: string[]
  items: { subject: string; dataset: string; covered_blocks: number; total_blocks: number; ratio: number }[]
  overall_ratio: number
}

export interface ExportRow {
  id: string
  dataset: string
  format: string
  status: string
  object_ref: string
  rows: number
  error: string
  created_at: string
}

export interface BenchmarkRun {
  id: string
  chain: string
  dataset: string
  address_hash: string
  target_block: number
  shard_count: number
  page_size: number
  job_id?: string
  status: string
  wall_clock_ms: number
  rows: number
  unique_rows: number
  requests: number
  attempts: number
  credits: number
  cost: number
  p50_ms: number
  p95_ms: number
  p99_ms: number
  page_gap_avg_ms: number
  page_gap_p95_ms: number
  peak_active_shards: number
  retry_rate: number
  timeout_rate: number
  rate_limit_rate: number
  error_429: number
  error_conn_limit: number
  error_timeout: number
  error_oversized: number
  peak_rss: number
  spool_peak_bytes: number
  ch_p95_ms: number
  raw_overlap_rows: number
  boundary_missing: number
  boundary_evidence: { ranges?: { range_start: number; range_end: number; first_block: number; last_block: number; rows: number; pages: number; status: string; first_tx_hash?: string; last_tx_hash?: string }[] }
  integrity_status: string
  excluded_from_learning: boolean
  error: string
  created_at: string
  updated_at: string
  // V1.2（§55/§103-§105）：Provider Latency Context + 归一化
  provider_latency_p50_ms?: number
  provider_latency_p95_ms?: number
  provider_ttfb_p50_ms?: number
  provider_ttfb_p95_ms?: number
  provider_body_p50_ms?: number
  provider_body_p95_ms?: number
  normalized_wall_clock_ms?: number
  provider_window_ratio?: number
  exclude_reason?: string
}

const EVM_CHAINS = ['bsc', 'eth', 'polygon', 'arbitrum', 'base', 'avalanche', 'optimism']
export const CHAINS = import.meta.env.VITE_TRON_ENABLED === 'true' ? [...EVM_CHAINS, 'tron'] : EVM_CHAINS
export const DATASETS = ['transactions', 'token_transfers', 'nft_transfers', 'logs', 'balances', 'internal_transactions']

export const STATUS_LABEL: Record<string, string> = {
  CREATED: '已创建', PLANNING: '规划中', QUEUED: '排队中', RUNNING: '运行中',
  PAUSE_REQUESTED: '暂停请求', PAUSED: '已暂停', RESUMING: '恢复中',
	PAUSED_FALLBACK_TOO_EXPENSIVE: '备用范围过大，已安全暂停',
  CANCEL_REQUESTED: '取消请求', CANCELLED: '已取消',
  COMPLETED: '已完成', PARTIAL_FAILED: '部分失败', FAILED: '失败',
  PENDING: '待处理', LEASED: '已租约', FAILED_RETRYABLE: '失败(重试)', FAILED_FINAL: '失败',
  SKIPPED: '已复用',
  FETCHED: '拉取完成', INGESTED: '入库中', VALIDATING: '校验中',
}

export function statusLabel(s: string): string {
  return STATUS_LABEL[s] ?? s
}

// V1.1：自动可恢复错误友好文案（§39）
export function friendlyError(s: string): string {
  if (s.startsWith('[response_too_large]')) return '单次返回数据较大，系统已自动降低单页数据量并继续下载'
  if (s.startsWith('[request_timeout]')) return '单次查询耗时较长，系统正在自动减小请求规模'
  if (s.startsWith('[rate_limit]')) return 'Provider 请求频率受限，系统正在自动降速重试'
  if (s.startsWith('[connection_limit]')) return 'Provider 并发连接达到限制，系统正在自动降低并发'
  if (s.startsWith('[split_range]')) return '请求区间过重，系统已自动拆分范围重试'
  return s
}

export function fmtBytes(n: number): string {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return v.toFixed(1) + ' ' + u[i]
}

// fmtUsageBytes 用量字节：小于 1G 用 MB，大于等于 1G 用 GB
export function fmtUsageBytes(n: number): string {
  if (!n) return '0 MB'
  const gb = 1024 * 1024 * 1024
  if (n >= gb) return (n / gb).toFixed(2) + ' GB'
  return (n / (1024 * 1024)).toFixed(2) + ' MB'
}

export function fmtDuration(sec: number): string {
  if (!sec || sec < 0) return '—'
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = Math.floor(sec % 60)
  const pad = (x: number) => String(x).padStart(2, '0')
  return h > 0 ? pad(h) + ':' + pad(m) + ':' + pad(s) : pad(m) + ':' + pad(s)
}
