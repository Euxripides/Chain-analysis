import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, ChevronLeft, Copy, Download, FileJson, MoreHorizontal, Pause, Play, RefreshCw, Search } from 'lucide-react'
import { api } from '../api/client'
import CopyToast from '../components/CopyToast'
import NoticeDialog from '../components/NoticeDialog'
import { PerformanceModeBadge, PerformanceModeStatus, useAnkrProductionProfile } from '../components/PerformanceMode'
import { fmtBytes, fmtDuration, friendlyError, statusLabel, type PerformanceMode, type Shard, type Snapshot } from '../types'
import { copyText } from '../utils/clipboard'
import { datasetLabel } from '../utils/field-labels'

type DetailTab = 'overview' | 'shards' | 'logs' | 'exports'
const tabs: Array<[DetailTab, string]> = [['overview', '概览'], ['shards', 'Shards'], ['logs', '日志'], ['exports', '导出']]

function phaseState(index: number, progress: number, status: string) {
  if (status === 'FAILED' && index >= 2) return index === 2 ? 'failed' : 'waiting'
  if (status === 'COMPLETED') return 'success'
  const active = Math.min(5, Math.floor(progress * 6))
  return index < active ? 'success' : index === active ? 'running' : 'waiting'
}

export default function JobDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [live, setLive] = useState<Snapshot | null>(null)
  const [tab, setTab] = useState<DetailTab>('overview')
  const [shardFilter, setShardFilter] = useState('all')
  const [logSearch, setLogSearch] = useState('')
  const [exporting, setExporting] = useState('')
  const [notice, setNotice] = useState('')
  const [copyToast, setCopyToast] = useState('')
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['job', id], queryFn: () => api.getJob(id!), refetchInterval: 3000, enabled: !!id })
  const profileQuery = useAnkrProductionProfile(!!query.data?.performance)

  useEffect(() => {
    if (!id) return
    const events = new EventSource(`/api/v1/download-jobs/${id}/events`)
    events.onmessage = (event) => { try { setLive(JSON.parse(event.data)) } catch { /* malformed event ignored */ } }
    return () => events.close()
  }, [id])

  useEffect(() => { if (query.error) setNotice(query.error.message) }, [query.error])

  async function act(action: 'pause' | 'resume') {
    if (!id) return
    try { await api[action](id); await queryClient.invalidateQueries({ queryKey: ['job', id] }) }
    catch (error) { setNotice(error instanceof Error ? error.message : String(error)) }
  }

  async function changePerformanceMode(mode: PerformanceMode) {
    if (!id) return
    try {
      await api.setJobPerformanceMode(id, mode)
      await queryClient.invalidateQueries({ queryKey: ['job', id] })
    } catch (error) {
      setNotice(error instanceof Error ? error.message : String(error))
    }
  }

  async function handleCopy(value: string) {
    try {
      await copyText(value)
      setCopyToast('已复制')
    } catch {
      setCopyToast('复制失败')
    }
  }

  async function doExport(dataset: string, format: 'csv' | 'xlsx') {
    if (!id) return
    setExporting(`${dataset}:${format}`)
    try {
      const created = await api.createExport(id, dataset, format)
      const timer = window.setInterval(async () => {
        try {
          const current = await api.getExport(created.id)
          if (current.status === 'COMPLETED') { window.clearInterval(timer); setExporting(''); window.open(`/api/v1/exports/${created.id}/download`, '_blank') }
          else if (current.status === 'FAILED') { window.clearInterval(timer); setExporting(''); setNotice(current.error || '导出失败') }
        } catch { /* polling continues */ }
      }, 1000)
    } catch (error) { setExporting(''); setNotice(error instanceof Error ? error.message : String(error)) }
  }

  if (!id) return null
  if (query.isPending && !query.data) return <div className="workspace-page"><div className="workspace-skeleton"><i /><i /><i /></div></div>
  if (!query.data) return <div className="workspace-page"><Link to="/jobs">返回任务中心</Link></div>

  const { job, subjects, datasets, shards } = query.data
  const snapshot = live ?? query.data.progress
  const progress = snapshot?.progress ?? (job.status === 'COMPLETED' ? 1 : 0)
  const phases = ['参数校验', '分片生成', '请求拉取', '解析入库', '导出文件', '完成']
  const visibleShards = shards.filter((shard) => shardFilter === 'all' || shard.status === shardFilter)
  const events = [...shards]
    .filter((shard) => !logSearch || [shard.dataset, shard.status, shard.error].join(' ').toLowerCase().includes(logSearch.toLowerCase()))
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at)).slice(0, 30)
  const subject = subjects[0]?.subject || '—'
  const effectiveMode = job.effective_mode || job.requested_mode || 'fast'
  const performance = query.data.performance || {
    requested_mode: job.requested_mode || 'fast', effective_mode: effectiveMode,
    target_concurrency: profileQuery.data?.modes[effectiveMode].target_concurrency || 32,
    mode_reason: job.mode_reason,
  }

  return <div className="job-detail-workspace">
    <header className="job-detail-header">
      <div className="job-heading-row"><Link className="icon-button" to="/jobs" aria-label="返回任务中心"><ChevronLeft size={18} /></Link><div><div className="job-title-line"><h1>{job.job_no}</h1><span className="chain-code">{job.chain.toUpperCase()}</span><span className={`status-badge status-${job.status.toLowerCase()}`}><i />{statusLabel(job.status)}</span><PerformanceModeBadge requested={job.requested_mode || 'fast'} effective={effectiveMode} onExplain={() => setNotice(`请求模式：${(job.requested_mode || 'fast').toUpperCase()}\n当前模式：${effectiveMode.toUpperCase()}\n原因：${job.mode_reason || 'system_default'}`)} /></div><p>{job.datasets.map(datasetLabel).join(', ')} · {subjects.length} 地址 <i /> 创建于 {new Date(job.created_at).toLocaleString('zh-CN', { hour12: false })}</p></div></div>
      <div className="route-actions">
        {job.status === 'RUNNING' && <button className="secondary-button" onClick={() => act('pause')}><Pause size={15} />暂停任务</button>}
        {job.status === 'PAUSED' && <button className="secondary-button" onClick={() => act('resume')}><Play size={15} />继续任务</button>}
        <button className="primary-button" disabled={!!exporting || job.datasets.length === 0} onClick={() => doExport(job.datasets[0], 'csv')}><Download size={15} />导出全部</button>
        <button className="icon-button" aria-label="更多任务操作"><MoreHorizontal size={18} /></button>
      </div>
    </header>

    <section className="core-metrics" aria-label="核心指标">
      <div><small>总体进度</small><b className="success-text">{(progress * 100).toFixed(1)}%</b></div>
      <div><small>已用时间</small><b>{fmtDuration(snapshot?.elapsed_seconds || 0)}</b></div>
      <div><small>预计剩余</small><b>{snapshot?.eta_seconds ? fmtDuration(snapshot.eta_seconds) : '—'}</b></div>
      <div><small>总成本</small><b>${Number(snapshot?.api_cost_usd || 0).toFixed(5)}</b></div>
    </section>

    <nav className="detail-tabs" aria-label="任务详情分区">{tabs.map(([key, label]) => <button key={key} className={tab === key ? 'active' : ''} onClick={() => setTab(key)}>{label}</button>)}</nav>

    {tab === 'overview' && <div className="detail-overview-grid">
      <main>
        <section className="pipeline-stage">
          <header><h2>执行 Pipeline</h2><span>{snapshot?.done_shards || 0} / {snapshot?.total_shards || shards.length} Shards</span></header>
          <div className="pipeline-track">{phases.map((phase, index) => { const state = phaseState(index, progress, job.status); return <div className={`pipeline-node ${state}`} key={phase}><span>{state === 'success' ? <Check size={15} /> : index + 1}</span><b>{phase}</b><small>{state === 'success' ? '已完成' : state === 'running' ? '执行中' : state === 'failed' ? '失败' : '等待'}</small>{index < phases.length - 1 && <i />}</div> })}</div>
        </section>

        <section className="event-stream">
          <header><div><h2>最近事件</h2><span>{events.length} 条</span></div><button className="secondary-button" onClick={() => setTab('logs')}>查看全部日志</button></header>
          <div className="signal-spine event-list">{events.slice(0, 8).map((shard) => <div className={shard.error ? 'warn' : 'ok'} key={shard.id}><i /><time>{new Date(shard.updated_at).toLocaleTimeString('zh-CN', { hour12: false })}</time><span>{datasetLabel(shard.dataset)} · {statusLabel(shard.status)}</span><b>{shard.downloaded_rows.toLocaleString()} 行</b></div>)}</div>
          {!events.length && <div className="table-empty"><Check size={20} /><b>暂无执行事件</b></div>}
        </section>

        <section className="exception-summary"><header><h2>异常摘要</h2><button className="text-button" onClick={() => setTab('logs')}>查看错误日志</button></header>{shards.some((shard) => shard.error) ? <div>{shards.filter((shard) => shard.error).slice(0, 3).map((shard) => <button key={shard.id} onClick={() => setNotice(friendlyError(shard.error))}><b>{datasetLabel(shard.dataset)}</b><span>{friendlyError(shard.error)}</span></button>)}</div> : <p className="success-text"><Check size={16} />无异常</p>}</section>
      </main>

      <aside className="context-rail">
        <header><h2>基础信息</h2><button className="icon-button" aria-label="刷新任务"><RefreshCw size={15} /></button></header>
        <PerformanceModeStatus state={performance} profile={profileQuery.data} editable={!['COMPLETED', 'CANCELLED', 'FAILED', 'PARTIAL_FAILED'].includes(job.status)} onChange={changePerformanceMode} />
        <dl>
          <div><dt>链</dt><dd>{job.chain.toUpperCase()}</dd></div>
          <div><dt>地址</dt><dd className="copy-value"><span title={subject}>{subject}</span><button className="icon-button" aria-label="复制地址" title="复制完整地址" onClick={() => void handleCopy(subject)}><Copy size={13} /></button></dd></div>
          <div><dt>Dataset</dt><dd>{job.datasets.map(datasetLabel).join(', ')}</dd></div>
          <div><dt>区间</dt><dd>{job.from_block?.toLocaleString() || '—'} → {job.to_block?.toLocaleString() || '—'}</dd></div>
          <div><dt>Shards</dt><dd>{snapshot?.done_shards || 0} / {snapshot?.total_shards || shards.length}</dd></div>
          <div><dt>API 请求</dt><dd>{Number(snapshot?.api_requests || 0).toLocaleString()}</dd></div>
          <div><dt>网络速度</dt><dd>{fmtBytes(snapshot?.bytes_per_sec || 0)}/s</dd></div>
          <div><dt>缓存命中率</dt><dd>{((snapshot?.coverage_hit_ratio || 0) * 100).toFixed(1)}%</dd></div>
          <div><dt>创建时间</dt><dd>{new Date(job.created_at).toLocaleString('zh-CN', { hour12: false })}</dd></div>
        </dl>
      </aside>
    </div>}

    {tab === 'shards' && <section className="detail-table-panel">
      <header><div><h2>Shards</h2><span>{visibleShards.length} 条</span></div><select aria-label="筛选 Shard 状态" name="shard-status" autoComplete="off" value={shardFilter} onChange={(event) => setShardFilter(event.target.value)}><option value="all">全部状态</option><option value="COMPLETED">已完成</option><option value="RUNNING">运行中</option><option value="FAILED_FINAL">失败</option></select></header>
      <div className="task-table-scroll"><table className="task-table"><thead><tr><th>Dataset</th><th>区间</th><th>状态</th><th>行数</th><th>页</th><th>请求 / 重试</th><th>Raw</th><th>更新时间</th></tr></thead><tbody>{visibleShards.map((shard) => <ShardRow key={shard.id} shard={shard} onError={setNotice} />)}</tbody></table></div>
    </section>}

    {tab === 'logs' && <section className="detail-table-panel log-panel">
      <header><div><h2>任务日志</h2><span>{events.length} 条</span></div><label className="task-search"><Search size={15} /><input name="job-log-search" autoComplete="off" value={logSearch} onChange={(event) => setLogSearch(event.target.value)} placeholder="搜索 Dataset / 状态 / 错误…" /></label></header>
      <div className="signal-spine event-list full">{events.map((shard) => <button className={shard.error ? 'warn' : 'ok'} key={shard.id} onClick={() => shard.error && setNotice(friendlyError(shard.error))}><i /><time>{new Date(shard.updated_at).toLocaleString('zh-CN', { hour12: false })}</time><span>{datasetLabel(shard.dataset)} · {statusLabel(shard.status)}</span><b>{shard.error ? friendlyError(shard.error) : `${shard.downloaded_rows.toLocaleString()} 行`}</b></button>)}</div>
    </section>}

    {tab === 'exports' && <section className="exports-workspace"><header><div><h2>生成导出文件</h2><span>{job.datasets.length} 个 Dataset</span></div></header>{job.datasets.map((dataset) => <div className="export-row" key={dataset}><FileJson size={18} /><div><b>{datasetLabel(dataset)}</b><span>{Number(snapshot?.downloaded_rows || 0).toLocaleString()} 行</span></div><button className="secondary-button" disabled={!!exporting} onClick={() => doExport(dataset, 'csv')}>CSV</button><button className="secondary-button" disabled={!!exporting} onClick={() => doExport(dataset, 'xlsx')}>XLSX</button></div>)}</section>}

    {notice && <NoticeDialog title="任务详情" message={notice} onClose={() => setNotice('')} />}
    {copyToast && <CopyToast message={copyToast} onClose={() => setCopyToast('')} />}
  </div>
}

function ShardRow({ shard, onError }: { shard: Shard; onError: (message: string) => void }) {
  return <tr className={shard.error ? 'failed' : ''} onClick={() => shard.error && onError(friendlyError(shard.error))}>
    <td>{datasetLabel(shard.dataset)}</td><td className="mono">{shard.from_block.toLocaleString()} → {shard.to_block.toLocaleString()}</td><td><span className={`status-badge status-${shard.status.toLowerCase()}`}><i />{statusLabel(shard.status)}</span></td><td>{shard.downloaded_rows.toLocaleString()}</td><td>{shard.page_no}</td><td>{shard.retries} / {shard.max_retries}</td><td>{shard.raw_object ? '已保存' : '—'}</td><td className="table-time">{new Date(shard.updated_at).toLocaleString('zh-CN', { hour12: false })}</td>
  </tr>
}
