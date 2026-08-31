import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { fmtBytes, fmtDuration, friendlyError, statusLabel, type Snapshot } from '../types'

export default function JobDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [live, setLive] = useState<Snapshot | null>(null)
  const [exporting, setExporting] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['job', id],
    queryFn: () => api.getJob(id!),
    refetchInterval: 3000,
    enabled: !!id,
  })

  useEffect(() => {
    if (!id) return
    const es = new EventSource('/api/v1/download-jobs/' + id + '/events')
    es.onmessage = (ev) => {
      try { setLive(JSON.parse(ev.data)) } catch { /* ignore */ }
    }
    return () => es.close()
  }, [id])

  if (!id) return null
  if (isLoading && !data) return <p>加载中…</p>
  if (!data) return <p>任务不存在</p>

  const { job, subjects, datasets, shards } = data
  const snap = live ?? data.progress ?? null

  async function doExport(dataset: string, format: 'csv' | 'xlsx') {
    setExporting(dataset + ':' + format)
    try {
      const exp = await api.createExport(id!, dataset, format)
      const timer = setInterval(async () => {
        try {
          const cur = await api.getExport(exp.id)
          if (cur.status === 'COMPLETED') {
            clearInterval(timer)
            setExporting('')
            window.open('/api/v1/exports/' + exp.id + '/download', '_blank')
          } else if (cur.status === 'FAILED') {
            clearInterval(timer)
            setExporting('')
            alert('导出失败: ' + cur.error)
          }
        } catch { /* retry */ }
      }, 1000)
    } catch (e) {
      setExporting('')
      alert(String(e))
    }
  }

  const pct = ((snap?.progress ?? 0) * 100).toFixed(1)

  return (
    <div className="page">
      <div className="row between">
        <div>
          <h1>{job.job_no}</h1>
          <p className="sub mono">id: {job.id}</p>
        </div>
        <div className="row gap">
          <span className={'badge b-' + job.status}>{statusLabel(job.status)}</span>
          <Link to="/jobs" className="ghost">← 任务中心</Link>
        </div>
      </div>

      <div className="metrics">
        <div className="metric"><label>进度</label><b>{pct}%</b></div>
        <div className="metric"><label>已下载行数</label><b>{(snap?.downloaded_rows ?? 0).toLocaleString()}</b></div>
        <div className="metric"><label>下载速率</label><b>{(snap?.rows_per_sec ?? 0).toLocaleString()} rows/s</b></div>
        <div className="metric"><label>网络</label><b>{fmtBytes(snap?.bytes_per_sec ?? 0)}/s</b></div>
        <div className="metric"><label>API 请求</label><b>{snap?.api_requests ?? 0}</b></div>
        <div className="metric"><label>API 成本</label><b>{'$' + (snap?.api_cost_usd ?? 0).toFixed(5)}</b></div>
        <div className="metric"><label>本地命中</label><b>{((snap?.coverage_hit_ratio ?? 0) * 100).toFixed(1)}%</b></div>
        <div className="metric"><label>已用时间</label><b>{fmtDuration(snap?.elapsed_seconds ?? 0)}</b></div>
        <div className="metric"><label>预计剩余</label><b>~{fmtDuration(snap?.eta_seconds ?? 0)}</b></div>
        <div className="metric"><label>Shards</label><b>{snap?.done_shards ?? 0}/{snap?.total_shards ?? 0}</b></div>
      </div>

      <div className="card">
        <div className="card-title">总体进度</div>
        <div className="meter"><div className="meter-fill" style={{ width: pct + '%' }} /></div>
      </div>

      <div className="card">
        <div className="card-title">地址 × Dataset</div>
        <table className="table">
          <thead><tr><th>地址</th>{job.datasets.map((d) => <th key={d}>{d}</th>)}<th>进度</th></tr></thead>
          <tbody>
            {subjects.map((s) => (
              <tr key={s.id}>
                <td className="mono">{s.subject}</td>
                {job.datasets.map((d) => {
                  const item = datasets.find((x) => x.subject_id === s.id && x.dataset === d)
                  return (
                    <td key={d}>
                      {item
                        ? <span className={'badge b-' + item.status}>{statusLabel(item.status)} {(item.progress * 100).toFixed(0)}%</span>
                        : <span className="muted">—</span>}
                    </td>
                  )
                })}
                <td><div className="meter sm"><div className="meter-fill" style={{ width: (s.progress * 100).toFixed(1) + '%' }} /></div></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="card">
        <div className="card-title">Shards</div>
        <table className="table">
          <thead><tr><th>Dataset</th><th>区间</th><th>状态</th><th>行数</th><th>页</th><th>重试</th><th>Raw</th><th>错误</th></tr></thead>
          <tbody>
            {shards.map((sh) => (
              <tr key={sh.id}>
                <td>{sh.dataset}</td>
                <td className="mono">{sh.from_block.toLocaleString()} → {sh.to_block.toLocaleString()}</td>
                <td><span className={'badge b-' + sh.status}>{statusLabel(sh.status)}</span></td>
                <td>{sh.downloaded_rows.toLocaleString()}</td>
                <td>{sh.page_no}</td>
                <td>{sh.retries}</td>
                <td className="mono muted" title={sh.raw_object}>{sh.raw_object ? '✓' : '—'}</td>
                <td className="muted" title={sh.error}>{sh.error ? friendlyError(sh.error).slice(0, 60) : ''}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="card">
        <div className="card-title">导出</div>
        <div className="row gap wrap">
          {job.datasets.map((d) => (
            <span key={d} className="row gap">
              <button className="ghost" disabled={!!exporting} onClick={() => doExport(d, 'csv')}>CSV · {d}</button>
              <button className="ghost" disabled={!!exporting} onClick={() => doExport(d, 'xlsx')}>XLSX · {d}</button>
            </span>
          ))}
        </div>
        {exporting && <p className="muted">正在导出 {exporting} …（完成后自动下载）</p>}
      </div>
    </div>
  )
}
