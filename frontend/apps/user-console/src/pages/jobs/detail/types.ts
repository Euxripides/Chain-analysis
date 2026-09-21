import { progressStateFor } from "@chain/ui";
import type { ProgressState } from "@chain/ui";
import type { ProviderRuntimeState } from "../../downloads/download.types";

/**
 * Job Detail 的类型与纯函数（用户端重构规范 V4 §28 / §47）
 *
 * 从原 `JobDetailPage.tsx` 原样抽出，**字段名、口径与派生规则一个都没改**：
 * 这些接口直接对应 `/api/v2/jobs/{id}` 的响应，任何字段改名都等于改契约。
 */

export interface JobRecord {
  id: string;
  job_no: string;
  status: string;
  source: string;
  requested_mode: string;
  effective_mode: string;
  mode_reason: string;
  chain: string;
  range_type: string;
  from_block: number | null;
  to_block: number | null;
  from_timestamp: string | null;
  to_timestamp: string | null;
  datasets: string[];
  reuse_policy: string;
  head_block: number;
  created_at: string;
  updated_at: string;
  resolved_at: string | null;
  error: string;
  stats: Record<string, unknown>;
}

export interface SuccessfulCursor {
  tron_last_timestamp_ms: number | null;
  checkpoint_at: string | null;
  definition: string;
}

export interface AddressLayer {
  subject_id: string;
  address: string;
  status: string;
  progress: number;
  updated_at: string;
  successful_cursor: SuccessfulCursor;
}

export interface DatasetLayer {
  dataset_id: string;
  subject_id: string;
  dataset: string;
  status: string;
  progress: number;
  shards: { total: number; completed: number; failed: number };
  updated_at: string;
}

export interface ShardLayer {
  shard_id: string;
  subject_id: string;
  dataset: string;
  from_block: number;
  to_block: number;
  status: string;
  retries: number;
  downloaded_rows: number;
  downloaded_bytes: number;
  raw_object: string;
  error_safe: string;
  updated_at: string;
  checkpoint?: {
    next_page_token?: string;
    page_no?: number;
    downloaded_rows?: number;
    downloaded_bytes?: number;
    last_success_at?: string;
  };
}

export interface JobProgress {
  status: string;
  progress: number;
  downloaded_rows: number;
  downloaded_bytes: number;
  api_requests: number;
  api_cost_usd: number;
  total_shards: number;
  done_shards: number;
  elapsed_seconds: number;
  eta_seconds: number;
  ts: string;
  coverage_hit_ratio?: number;
}

export interface JobExport {
  id: string;
  format: string;
  zip: boolean;
  status: string;
  rows: number;
  sha256: string;
  error_safe: string;
  object_ref: string;
  expires_at: string | null;
  created_at: string;
}

export interface JobDetailData {
  job: JobRecord;
  subjects: unknown[];
  datasets: unknown[];
  shards: unknown[];
  progress: JobProgress;
  performance: { requested_mode: string; effective_mode: string; mode_reason: string };
  layers: {
    job: { status: string; updated_at: string; error_safe: string };
    addresses: AddressLayer[];
    datasets: DatasetLayer[];
    shards: ShardLayer[];
  };
  exports: JobExport[];
  /**
   * 每个 Provider 通道的运行时档位（开发文档 §6.2 / §10.2）。
   *
   * 与 downloads/download.types 共用同一份 `ProviderRuntimeState`：这两个界面读的是
   * 同一个服务端字段，类型只应有一份定义（避免两处口径漂移）。
   * 旧后端不返回该数组时为 undefined —— 技术详情显示空态，不伪造运行状态。
   */
  provider_runtime_states?: ProviderRuntimeState[];
  source: { control: string; usage: string; as_of: string };
}

export const TERMINAL_STATUSES = new Set(["COMPLETED", "CANCELLED", "FAILED", "PARTIAL_FAILED", "ABORTED_BY_USER"]);

export function percent(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return "—";
  return `${(value * 100).toFixed(value >= 1 ? 0 : 1)}%`;
}

/** 服务端秒数的可读展示；缺失或非法值显示 —，不猜测。 */
export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return "—";
  const total = Math.round(seconds);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const rest = total % 60;
  if (hours > 0) return hours + " 小时 " + minutes + " 分";
  if (minutes > 0) return minutes + " 分 " + rest + " 秒";
  return rest + " 秒";
}

/**
 * 展示用的分层状态归并（Pipeline 阶段字形）。
 *
 * 这里**只把服务端各自返回的 status 归并成一个状态字形**，不产生任何数值：
 * 真实计数一律放在阶段的 meta 里原样呈现，前端不平均、不推算百分比。
 */
export function rollupState(values: string[]): ProgressState {
  if (values.length === 0) return "queued";
  const states = values.map((value) => progressStateFor(value));
  if (states.some((state) => state === "running")) return "running";
  if (states.some((state) => state === "failed")) return "failed";
  if (states.some((state) => state === "paused")) return "paused";
  if (states.some((state) => state === "waiting")) return "waiting";
  if (states.every((state) => state === "completed")) return "completed";
  if (states.every((state) => state === "cancelled")) return "cancelled";
  return "running";
}

/** 与 `@chain/ui` 的状态机保持同一份实现，避免两处口径漂移。 */
export { progressStateFor };
