export type ControlState = {
  state: string; reason: string; since: string; automatic_protection: boolean
  kill_switch: boolean; background_paused: boolean; manual_until?: string | null
  window_requests: number; window_failures: number; error_rate: number; p95_ms: number
}

export type Overview = {
  window: { from: string; to: string }; freshness: string
  metrics: { logical_calls: number; provider_calls: number; errors: number; retries: number; cache_hits: number; success_rate: number; credits: number; estimated_cost_usd: number; p50_ms: number; p95_ms: number; p99_ms: number }
  trend: { time: string; requests: number; errors: number; cost_usd: number; p95_ms: number }[]
  control: ControlState
  sources: Record<string, string>
}

export type AggregateItem = { operation?: string; error_class?: string; usage_type?: string; provider?: string; requests: number; logical_calls: number; errors: number; avg_ms: number; p95_ms: number; credits: number; estimated_cost_usd: number; cost_source: string }
export type RequestRow = { id: string; provider: string; chain: string; method: string; method_class: string; usage_type: string; request_time: string; duration_ms: number; status: string; http_status: number; error_class: string; api_error: string; credits: number; cost_usd: number; trace_id: string; request_id: string; retry_attempt: number; cache_hit: boolean; singleflight_hit: boolean; method_calls: number; batch_size: number }

