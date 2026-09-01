// V1.3 交易详情 / Method 查询 API（方案 §25/§52/§53/§54）。
const BASE = '/api/v1'

export type DecodeMode = 'local_only' | 'auto' | 'force_refresh'

export interface MethodCandidate {
  name: string
  signature: string
  source: string
  verified: boolean
}

export interface MethodInfo {
  id: string
  name: string
  signature: string
  source: string
  verified: boolean
  confidence: string
  method_type: string
  category: string
  risk: boolean
  candidates?: MethodCandidate[]
}

export interface DecodedParam {
  name: string
  type: string
  value: unknown
}

export interface DecodedLog {
  log_index: number
  address: string
  topic0: string
  event_name: string
  event_signature: string
  event_source: string
  event_verified: boolean
  params?: DecodedParam[]
}

export interface TransactionDetail {
  chain: string
  tx_hash: string
  transaction: Record<string, unknown>
  input: string
  method: MethodInfo
  decoded_input?: Record<string, unknown>
  decoded_params?: DecodedParam[]
  decode_source: string
  decode_verified: boolean
  decode_cache_hit: boolean
  decoded_at?: string
  logs: Record<string, unknown>[]
  decoded_logs?: DecodedLog[]
  decode_error?: string
}

export interface MethodLookup {
  selector: string
  candidates: MethodCandidate[]
  resolved: boolean
  resolution: MethodInfo
  collision: boolean
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(BASE + path, { headers: { 'Content-Type': 'application/json' }, ...init })
  if (!response.ok) {
    let message = response.statusText
    try {
      const body = await response.json()
      message = body.error ?? message
    } catch { /* 保留 statusText */ }
    throw new Error(message)
  }
  return response.json() as Promise<T>
}

// fetchTransactionDetail 交易详情。
// 默认 auto：本地已有解码结果直接返回，不会重复消耗 Provider 额度（§23）。
export function fetchTransactionDetail(chain: string, txHash: string, decode: DecodeMode = 'auto') {
  return request<TransactionDetail>(`/transactions/${chain}/${txHash}?decode=${decode}`)
}

// lookupMethod Selector 查询：必须展示全部候选（§10/§53）。
export function lookupMethod(selector: string) {
  return request<MethodLookup>(`/methods/${selector}`)
}

// searchMethods Method 名称 / 签名 / Selector 搜索（§28）。
export function searchMethods(query: string, limit = 20) {
  return request<{ query: string; results: { selector: string; method_name: string; signature: string; source: string; verified: boolean }[]; count: number }>(
    `/methods/search?q=${encodeURIComponent(query)}&limit=${limit}`)
}

// fetchMethodEnrichment Method 解析覆盖率（§37，与下载 Coverage 解耦）。
export function fetchMethodEnrichment(chain: string) {
  return request<{
    enrichment: { chain: string; total: number; resolved: number; abi_verified: number; unknown: number; resolved_rate: number; verified_rate: number; unknown_rate: number }
    resolve_queue_depth: number
  }>(`/methods/enrichment?chain=${encodeURIComponent(chain)}`)
}
