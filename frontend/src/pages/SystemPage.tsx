import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { fmtUsageBytes } from '../types'

export default function SystemPage() {
  const { data: status } = useQuery({ queryKey: ['provider-status'], queryFn: api.providerStatus, refetchInterval: 10000 })
  const { data: usage } = useQuery({ queryKey: ['provider-usage'], queryFn: () => api.providerUsage(24), refetchInterval: 10000 })
  const { data: cov } = useQuery({
    queryKey: ['coverage-stats'],
    queryFn: async () => {
      const res = await fetch('/api/v1/coverage/stats')
      return (await res.json()) as { stats: Record<string, number> }
    },
    refetchInterval: 10000,
  })
  const { data: tokens } = useQuery({ queryKey: ['token-enrichment'], queryFn: api.tokenEnrichment, refetchInterval: 10000 })

  const datasets = Object.keys(usage?.by_dataset ?? {})
  const totalCost = datasets.reduce((s, d) => s + (usage?.by_dataset[d]?.cost_usd ?? 0), 0)
  const totalRows = datasets.reduce((s, d) => s + (usage?.by_dataset[d]?.rows ?? 0), 0)

  return (
    <div className="page">
      <h1>状态总览</h1>

      <div className="card">
        <div className="card-title">Provider 状态</div>
        <div className="row gap wrap">
          <div className="metric"><label>Provider</label><b>{String(status?.provider ?? '—')}</b></div>
          <div className="metric"><label>状态</label><b>{String(status?.state ?? '—')}</b></div>
          <div className="metric"><label>近期错误率</label><b>{((Number(status?.recent_error_rate) || 0) * 100).toFixed(1)}%</b></div>
          <div className="metric"><label>连续失败</label><b>{String(status?.consecutive_failures ?? 0)}</b></div>
          <div className="metric"><label>最近错误</label><b className="muted">{String(status?.error || '—').slice(0, 60)}</b></div>
        </div>
      </div>

      <div className="card">
        <div className="card-title">Token Metadata</div>
        <div className="metrics token-metadata-metrics">
          <div className="metric"><label>Token Contracts</label><b>{(tokens?.token_contracts ?? 0).toLocaleString()}</b></div>
          <div className="metric"><label>完整</label><b className="metric-good">{(tokens?.complete ?? 0).toLocaleString()}</b></div>
          <div className="metric"><label>部分收录</label><b>{(tokens?.partial ?? 0).toLocaleString()}</b></div>
          <div className="metric"><label>未知</label><b>{(tokens?.unknown ?? 0).toLocaleString()}</b></div>
          <div className="metric"><label>冲突</label><b className="metric-danger">{(tokens?.conflict ?? 0).toLocaleString()}</b></div>
          <div className="metric"><label>解析队列</label><b>{(tokens?.queue_depth ?? 0).toLocaleString()}</b></div>
        </div>
      </div>

      <div className="card">
        <div className="card-title">Ankr API 用量（最近 24h）</div>
        <div className="metrics">
          <div className="metric"><label>总请求</label><b>{datasets.reduce((s, d) => s + (usage?.by_dataset[d]?.requests ?? 0), 0)}</b></div>
          <div className="metric"><label>总行数</label><b>{totalRows.toLocaleString()}</b></div>
          <div className="metric"><label>总成本</label><b>{'$' + totalCost.toFixed(4)}</b></div>
          <div className="metric"><label>总字节</label><b>{fmtUsageBytes(datasets.reduce((s, d) => s + (usage?.by_dataset[d]?.bytes ?? 0), 0))}</b></div>
        </div>
        <table className="table" style={{ marginTop: 10 }}>
          <thead><tr><th>Dataset</th><th>请求</th><th>Credits</th><th>成本</th><th>行数</th><th>字节</th></tr></thead>
          <tbody>
            {datasets.map((d) => (
              <tr key={d}>
                <td>{d}</td>
                <td>{usage?.by_dataset[d]?.requests}</td>
                <td>{usage?.by_dataset[d]?.credits}</td>
                <td>{'$' + (usage?.by_dataset[d]?.cost_usd ?? 0).toFixed(5)}</td>
                <td>{usage?.by_dataset[d]?.rows.toLocaleString()}</td>
                <td>{fmtUsageBytes(usage?.by_dataset[d]?.bytes ?? 0)}</td>
              </tr>
            ))}
            {datasets.length === 0 && <tr><td colSpan={6} className="muted center">暂无请求</td></tr>}
          </tbody>
        </table>
      </div>

      <div className="card">
        <div className="card-title">本地 Coverage</div>
        <table className="table">
          <thead><tr><th>Dataset</th><th>区间数</th><th>覆盖块数</th><th>覆盖行数</th></tr></thead>
          <tbody>
            {Object.keys(cov?.stats ?? {}).filter((k) => k.endsWith(':ranges')).map((k) => {
              const d = k.replace(':ranges', '')
              return (
                <tr key={d}>
                  <td>{d}</td>
                  <td>{cov?.stats[d + ':ranges'] ?? 0}</td>
                  <td>{(cov?.stats[d + ':blocks'] ?? 0).toLocaleString()}</td>
                  <td>{(cov?.stats[d + ':rows'] ?? 0).toLocaleString()}</td>
                </tr>
              )
            })}
            {Object.keys(cov?.stats ?? {}).length === 0 && <tr><td colSpan={4} className="muted center">暂无 Coverage</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  )
}
