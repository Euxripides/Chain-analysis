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
  status: string
  priority: number
  retries: number
  max_retries: number
  downloaded_rows: number
  downloaded_bytes: number
  raw_object: string
  error: string
  updated_at: string
}

export interface JobDetail {
  job: Job
  subjects: Subject[]
  datasets: DatasetItem[]
  shards: Shard[]
  progress: Snapshot | null
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

export const CHAINS = ['bsc', 'eth', 'polygon', 'arbitrum', 'base', 'avalanche', 'optimism']
export const DATASETS = ['transactions', 'token_transfers', 'nft_transfers', 'logs', 'balances']

export const STATUS_LABEL: Record<string, string> = {
  CREATED: '已创建', PLANNING: '规划中', QUEUED: '排队中', RUNNING: '运行中',
  PAUSE_REQUESTED: '暂停请求', PAUSED: '已暂停', RESUMING: '恢复中',
  CANCEL_REQUESTED: '取消请求', CANCELLED: '已取消',
  COMPLETED: '已完成', PARTIAL_FAILED: '部分失败', FAILED: '失败',
  PENDING: '待处理', LEASED: '已租约', FAILED_RETRYABLE: '失败(重试)', FAILED_FINAL: '失败',
  SKIPPED: '已复用',
}

export function statusLabel(s: string): string {
  return STATUS_LABEL[s] ?? s
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
