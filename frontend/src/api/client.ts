import type { CoveragePreview, ExportRow, Job, JobDetail, Snapshot } from '../types'

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
}
