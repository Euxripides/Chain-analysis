import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import { fmtDuration, statusLabel, type Job } from '../types'

function jobProgress(job: Job): number {
  const s = job.stats as Record<string, unknown>
  const done = Number(s['done_shards'] ?? 0)
  const total = Number(s['total_shards'] ?? 0)
  if (total > 0) return done / total
  return 0
}

export default function JobsPage() {
  const [filter, setFilter] = useState('')
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({
    queryKey: ['jobs', filter],
    queryFn: () => api.listJobs(filter),
    refetchInterval: 2000,
  })

  async function act(job: Job, action: 'pause' | 'resume' | 'cancel') {
    try {
      await api[action](job.id)
      qc.invalidateQueries({ queryKey: ['jobs'] })
    } catch (e) {
      alert(String(e))
    }
  }

  return (
    <div className="page">
      <div className="row between">
        <h1>任务中心</h1>
        <select value={filter} onChange={(e) => setFilter(e.target.value)}>
          <option value="">全部状态</option>
          <option value="RUNNING">运行中</option>
          <option value="QUEUED">排队中</option>
          <option value="PAUSED">已暂停</option>
          <option value="COMPLETED">已完成</option>
          <option value="PARTIAL_FAILED">部分失败</option>
          <option value="FAILED">失败</option>
          <option value="CANCELLED">已取消</option>
        </select>
      </div>

      {isLoading ? <p>加载中…</p> : (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th>任务号</th><th>链</th><th>Dataset</th><th>状态</th><th>进度</th>
                <th>行数</th><th>请求</th><th>成本</th><th>创建时间</th><th></th>
              </tr>
            </thead>
            <tbody>
              {(data?.jobs ?? []).map((j) => {
                const p = jobProgress(j)
                const stats = j.stats as Record<string, unknown>
                const active = ['RUNNING', 'QUEUED', 'PAUSE_REQUESTED', 'RESUMING'].includes(j.status)
                return (
                  <tr key={j.id}>
                    <td><Link className="mono link" to={'/jobs/' + j.id}>{j.job_no}</Link></td>
                    <td>{j.chain.toUpperCase()}</td>
                    <td>{j.datasets.length} 类</td>
                    <td><span className={'badge b-' + j.status}>{statusLabel(j.status)}</span></td>
                    <td style={{ minWidth: 140 }}>
                      <div className="meter sm">
                        <div className="meter-fill" style={{ width: (p * 100).toFixed(1) + '%' }} />
                      </div>
                      <span className="muted">{(p * 100).toFixed(1)}%</span>
                    </td>
                    <td>{Number(stats['downloaded_rows'] ?? 0).toLocaleString()}</td>
                    <td>{Number(stats['api_requests'] ?? 0)}</td>
                    <td>{'$' + Number(stats['api_cost_usd'] ?? 0).toFixed(4)}</td>
                    <td className="muted">{new Date(j.created_at).toLocaleString()}</td>
                    <td>
                      <div className="row gap">
                        {active && <button className="ghost sm" onClick={() => act(j, 'pause')}>暂停</button>}
                        {j.status === 'PAUSED' && <button className="ghost sm" onClick={() => act(j, 'resume')}>继续</button>}
                        {!['COMPLETED', 'CANCELLED', 'FAILED', 'PARTIAL_FAILED'].includes(j.status) && (
                          <button className="ghost sm danger" onClick={() => act(j, 'cancel')}>取消</button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}
              {(data?.jobs ?? []).length === 0 && (
                <tr><td colSpan={10} className="muted center">暂无任务，去 <Link to="/">地址下载</Link> 创建一个</td></tr>
              )}
            </tbody>
          </table>
        </div>
      )}
      <p className="muted" style={{ marginTop: 8 }}>{fmtDuration(0)}</p>
    </div>
  )
}
