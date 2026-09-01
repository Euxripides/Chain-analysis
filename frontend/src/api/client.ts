import type { CoveragePreview, ExportRow, Job, JobDetail, Snapshot } from '../types'

export type TokenMetadata = { chain: string; contract_address: string; name?: string; symbol?: string; decimals: number | null; token_type?: string; logo_url?: string; verified: boolean; metadata_source?: string; confidence: string; metadata_status: string }

const BASE = '/api/v1'

async function http<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!res.ok) {
    let msg = res.statusText
    try {
      const body = await res.json()
      msg = body.error ?? msg
    } catch { /* ignore */ }
    throw new Error(msg)
  }
  return res.json() as Promise<T>
}

export const apiControlHttp = <T,>(path: string, init?: RequestInit) => http<T>('/api-control' + path, init)

export const api = {
  ensure: (payload: unknown) => http<{ job_id: string | null; coverage: number; status: string }>('/datasets/ensure', { method: 'POST', body: JSON.stringify(payload) }),
  preview: (payload: unknown) => http<CoveragePreview>('/datasets/coverage/preview', { method: 'POST', body: JSON.stringify(payload) }),
  listJobs: (status = '') => http<{ jobs: Job[] }>(`/download-jobs?limit=100${status ? '&status=' + status : ''}`),
  getJob: (id: string) => http<JobDetail>(`/download-jobs/${id}`),
  pause: (id: string) => http<{ status: string }>(`/download-jobs/${id}/pause`, { method: 'POST' }),
  resume: (id: string) => http<{ status: string }>(`/download-jobs/${id}/resume`, { method: 'POST' }),
  cancel: (id: string) => http<{ status: string }>(`/download-jobs/${id}/cancel`, { method: 'POST' }),
  providerStatus: () => http<Record<string, unknown>>('/providers/ankr/status'),
  providerUsage: (hours = 24) => http<{ since: string; by_dataset: Record<string, { requests: number; credits: number; cost_usd: number; rows: number; bytes: number }> }>(`/providers/ankr/usage?hours=${hours}`),
  createExport: (jobId: string, dataset: string, format: string) => http<ExportRow>('/exports', { method: 'POST', body: JSON.stringify({ job_id: jobId, dataset, format }) }),
  createExportBySubject: (dataset: string, subject: string, chain: string) => http<ExportRow>('/exports', { method: 'POST', body: JSON.stringify({ dataset, format: 'csv', subject, chain }) }),
  getExport: (id: string) => http<ExportRow>(`/exports/${id}`),
  snapshot: (id: string) => http<JobDetail>(`/download-jobs/${id}`).then((d) => d.progress as Snapshot),
  queryRows: (dataset: string, chain: string, subject: string, limit = 100, offset = 0) =>
    http<{ dataset: string; rows: Record<string, unknown>[]; count: number }>(
      `/datasets/${dataset}?chain=${chain}&subject=${subject}&limit=${limit}&offset=${offset}`),

  // ---- V1.2 ----
  listCases: () => http<{ cases: { id: string; name: string; color?: string }[] }>('/cases'),
  createCase: (name: string) => http<{ id: string; name: string }>('/cases', { method: 'POST', body: JSON.stringify({ name }) }),
  listTags: () => http<{ tags: { id: string; name: string; color?: string }[] }>('/tags'),
  createTag: (name: string) => http<{ id: string; name: string }>('/tags', { method: 'POST', body: JSON.stringify({ name }) }),
  updateAddressMetadata: (chain: string, address: string, payload: unknown) =>
    http<{ address: { case_names: string[]; tag_names: string[]; total_activity_count?: number; last_data_block?: number; last_sync_at?: string } }>(
      `/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/metadata`,
      { method: 'PATCH', body: JSON.stringify(payload) },
    ),
  explorerAddresses: (params: string) => http<{ addresses: unknown[]; next_cursor: string }>('/data-explorer/addresses?' + params),
  explorerQuery: (payload: unknown) => http<{ rows: Record<string, unknown>[]; next_cursor: string; count: number }>('/data-explorer/query', { method: 'POST', body: JSON.stringify(payload) }),
  updatePreview: (payload: unknown) => http<Record<string, unknown>>('/updates/preview', { method: 'POST', body: JSON.stringify(payload) }),
  updateConfirm: (previewId: string) => http<{ job_ids: string[] }>('/updates/' + previewId + '/confirm', { method: 'POST', body: '{}' }),
  exportV2: (payload: unknown) => http<ExportRow>('/exports', { method: 'POST', body: JSON.stringify(payload) }),
  exportTicket: (id: string) => http<{ ticket_id: string; url: string }>('/exports/' + id + '/download-ticket', { method: 'POST', body: '{}' }),
  tokenSearch: (chain: string, query: string, limit = 30) => http<{ tokens: TokenMetadata[] }>(`/tokens/search?chain=${encodeURIComponent(chain)}&q=${encodeURIComponent(query)}&limit=${limit}`),
  tokenEnrichment: () => http<{ token_contracts: number; complete: number; partial: number; unknown: number; conflict: number; failed: number; logo_available: number; queue_depth: number }>('/tokens/enrichment'),
}
