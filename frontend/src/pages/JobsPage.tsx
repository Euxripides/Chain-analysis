import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { useScopedQuery as useQuery } from '../lib/sessionScope'
import { Archive, ChevronDown, Clock3, Copy, Download, Filter, MoreHorizontal, Pause, Play, Plus, Search, Trash2, X } from 'lucide-react'
import { api } from '../api/client'
import ConfirmDialog from '../components/ConfirmDialog'
import CopyToast from '../components/CopyToast'
import NoticeDialog from '../components/NoticeDialog'
import { PerformanceModeBadge } from '../components/PerformanceMode'
import { fmtDuration, statusLabel, type Job } from '../types'
import { copyText } from '../utils/clipboard'
import { datasetLabel } from '../utils/field-labels'

const terminal = new Set(['COMPLETED', 'CANCELLED', 'FAILED', 'PARTIAL_FAILED', 'ABORTED_BY_USER'])
// 可恢复（继续）的暂停态：PAUSE_REQUESTED 是暂停收敛中的状态，用户在这里
// 也必须能直接继续 —— 服务端已允许从该状态恢复，界面不能再隐藏入口。
const resumable = new Set(['PAUSED', 'PAUSE_REQUESTED', 'PAUSED_FALLBACK_TOO_EXPENSIVE'])
const statusViews = [
  ['all', '全部任务'], ['RUNNING', '运行中'], ['COMPLETED', '已完成'], ['FAILED', '失败'], ['PARTIAL_FAILED', '异常'],
] as const

function progressOf(job: Job) {
  const done = Number(job.stats.done_shards ?? 0)
  const total = Number(job.stats.total_shards ?? 0)
  return total > 0 ? Math.min(1, done / total) : job.status === 'COMPLETED' ? 1 : 0
}

function jobStage(job: Job) {
  if (job.status === 'COMPLETED') return '导出就绪'
  if (job.status === 'FAILED' || job.status === 'PARTIAL_FAILED') return String(job.stats.failed_stage || '请求拉取')
  if (job.status === 'RUNNING') return String(job.stats.stage || '请求拉取')
  if (job.status === 'QUEUED') return '等待资源'
  return statusLabel(job.status)
}

export default function JobsPage() {
  const [view, setView] = useState('all')
  const [search, setSearch] = useState('')
  const [chain, setChain] = useState('all')
  const [dataset, setDataset] = useState('all')
  const [newestFirst, setNewestFirst] = useState(true)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [failedOnly, setFailedOnly] = useState(false)
  const [exportableOnly, setExportableOnly] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [previewId, setPreviewId] = useState<string | null>(null)
  const [confirmIds, setConfirmIds] = useState<string[]>([])
  const [notice, setNotice] = useState('')
  const [copyToast, setCopyToast] = useState('')
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['jobs'], queryFn: () => api.listJobs(), refetchInterval: 3000, staleTime: 1500 })

  const jobs = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return (query.data?.jobs || []).filter((job) => {
      const viewMatch = view === 'all' || (view === 'FAILED' ? job.status === 'FAILED' : job.status === view)
      const chainMatch = chain === 'all' || job.chain === chain
      const datasetMatch = dataset === 'all' || job.datasets.includes(dataset)
      const searchMatch = !needle || [job.job_no, job.id, job.chain, ...job.datasets].join(' ').toLowerCase().includes(needle)
      const failureMatch = !failedOnly || ['FAILED', 'PARTIAL_FAILED'].includes(job.status)
      const exportMatch = !exportableOnly || job.status === 'COMPLETED'
      return viewMatch && chainMatch && datasetMatch && searchMatch && failureMatch && exportMatch
    }).sort((a, b) => newestFirst ? b.created_at.localeCompare(a.created_at) : a.created_at.localeCompare(b.created_at))
  }, [query.data?.jobs, view, chain, dataset, search, failedOnly, exportableOnly, newestFirst])

  const allJobs = query.data?.jobs || []
  const counts = useMemo(() => ({
    total: allJobs.length,
    running: allJobs.filter((job) => job.status === 'RUNNING').length,
    completed: allJobs.filter((job) => job.status === 'COMPLETED').length,
    failed: allJobs.filter((job) => ['FAILED', 'PARTIAL_FAILED'].includes(job.status)).length,
    cost: allJobs.reduce((sum, job) => sum + Number(job.stats.api_cost_usd || 0), 0),
    exports: allJobs.filter((job) => job.status === 'COMPLETED').length,
  }), [allJobs])
  const preview = allJobs.find((job) => job.id === previewId) || null
  const allVisibleSelected = jobs.length > 0 && jobs.every((job) => selected.has(job.id))

  function toggleOne(id: string) {
    setSelected((current) => { const next = new Set(current); next.has(id) ? next.delete(id) : next.add(id); return next })
  }

  function toggleVisible() {
    setSelected((current) => {
      const next = new Set(current)
      if (allVisibleSelected) jobs.forEach((job) => next.delete(job.id))
      else jobs.forEach((job) => next.add(job.id))
      return next
    })
  }

  async function act(job: Job, action: 'pause' | 'resume' | 'cancel') {
    try {
      await api[action](job.id)
      await queryClient.invalidateQueries({ queryKey: ['jobs'] })
    } catch (error) { setNotice(error instanceof Error ? error.message : String(error)) }
  }

  async function handleCopy(value: string) {
    try {
      await copyText(value)
      setCopyToast('已复制')
    } catch {
      setCopyToast('复制失败')
    }
  }

  async function cancelSelected() {
    const cancellable = allJobs.filter((job) => confirmIds.includes(job.id) && !terminal.has(job.status))
    try {
      await Promise.all(cancellable.map((job) => api.cancel(job.id)))
      setSelected(new Set())
      await queryClient.invalidateQueries({ queryKey: ['jobs'] })
    } catch (error) { setNotice(error instanceof Error ? error.message : String(error)) }
    finally { setConfirmIds([]) }
  }

  return <div className="task-workspace">
    <header className="route-header task-route-header">
      <div><h1>任务中心</h1><p>下载任务运行、筛选与处置</p></div>
      <Link to="/" className="primary-button"><Plus size={15} />新建任务</Link>
    </header>

    <section className="task-pulse" aria-label="任务概览">
      <div className="task-total"><small>全部任务</small><b>{counts.total.toLocaleString()}</b></div>
      <button onClick={() => setView('RUNNING')}><i className="blue" /><small>运行中</small><b>{counts.running}</b></button>
      <button onClick={() => setView('COMPLETED')}><i className="green" /><small>已完成</small><b>{counts.completed}</b></button>
      <button onClick={() => setView('FAILED')}><i className="red" /><small>失败 / 异常</small><b>{counts.failed}</b></button>
      <div><small>累计成本</small><b>${counts.cost.toFixed(4)}</b></div>
      <div><small>可导出任务</small><b>{counts.exports}</b></div>
    </section>

    <div className="task-command-deck">
      <nav className="task-views" aria-label="快捷视图">
        {statusViews.map(([key, label]) => <button key={key} className={view === key ? 'active' : ''} onClick={() => setView(key)}>{label}</button>)}
        <button className="saved-view" onClick={() => { localStorage.setItem('ankr-task-view', JSON.stringify({ view, chain, dataset, failedOnly, exportableOnly, newestFirst })); setNotice('当前任务筛选已保存到此浏览器。') }}>保存视图 <ChevronDown size={13} /></button>
      </nav>
      <div className="task-filters">
        <label className="task-search"><Search size={15} /><input name="task-search" autoComplete="off" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索任务号 / 地址 / Dataset…" /></label>
        <label><span className="sr-only">链</span><select name="task-chain" autoComplete="off" value={chain} onChange={(event) => setChain(event.target.value)}><option value="all">全部链</option>{Array.from(new Set(allJobs.map((job) => job.chain))).map((value) => <option key={value} value={value}>{value.toUpperCase()}</option>)}</select></label>
        <label><span className="sr-only">Dataset</span><select name="task-dataset" autoComplete="off" value={dataset} onChange={(event) => setDataset(event.target.value)}><option value="all">全部 Dataset</option>{Array.from(new Set(allJobs.flatMap((job) => job.datasets))).map((value) => <option key={value} value={value}>{datasetLabel(value)}</option>)}</select></label>
        <button className="secondary-button" aria-pressed={!newestFirst} onClick={() => setNewestFirst((value) => !value)}><Clock3 size={14} />{newestFirst ? '最新创建' : '最早创建'}</button>
        <button className="secondary-button" aria-expanded={advancedOpen} onClick={() => setAdvancedOpen((value) => !value)}><Filter size={14} />更多筛选</button>
        {(search || chain !== 'all' || dataset !== 'all' || view !== 'all' || failedOnly || exportableOnly) && <button className="text-button" onClick={() => { setSearch(''); setChain('all'); setDataset('all'); setView('all'); setFailedOnly(false); setExportableOnly(false) }}>清空筛选</button>}
      </div>
      {advancedOpen && <div className="task-advanced-filters"><label><input type="checkbox" checked={failedOnly} onChange={(event) => setFailedOnly(event.target.checked)} /><span>仅失败 / 异常</span></label><label><input type="checkbox" checked={exportableOnly} onChange={(event) => setExportableOnly(event.target.checked)} /><span>仅可导出</span></label></div>}
    </div>

    <section className="task-table-shell" aria-busy={query.isPending}>
      <div className="task-table-scroll">
        <table className="task-table">
          <thead><tr>
            <th className="checkbox-cell"><input type="checkbox" aria-label="选择当前页任务" checked={allVisibleSelected} onChange={toggleVisible} /></th>
            <th>任务号</th><th>状态</th><th>模式</th><th>链</th><th>Dataset</th><th>地址数</th><th>进度</th><th>当前阶段</th><th>行数</th><th>请求数</th><th>成本</th><th>运行时长</th><th>创建时间</th><th aria-label="操作" />
          </tr></thead>
          <tbody>
            {query.isPending ? Array.from({ length: 7 }, (_, index) => <tr className="skeleton-row" key={index}><td colSpan={15}><i /></td></tr>) : jobs.map((job) => {
              const progress = progressOf(job)
              const stats = job.stats
              return <tr key={job.id} className={`${selected.has(job.id) ? 'selected ' : ''}${job.status === 'FAILED' ? 'failed' : ''}`} tabIndex={0} onClick={() => setPreviewId(job.id)} onKeyDown={(event) => event.key === 'Enter' && setPreviewId(job.id)}>
                <td className="checkbox-cell" onClick={(event) => event.stopPropagation()}><input type="checkbox" aria-label={`选择任务 ${job.job_no}`} checked={selected.has(job.id)} onChange={() => toggleOne(job.id)} /></td>
                <td><Link to={`/jobs/${job.id}`} onClick={(event) => event.stopPropagation()} className="task-id">{job.job_no}</Link></td>
                <td><span className={`status-badge status-${job.status.toLowerCase()}`}><i />{statusLabel(job.status)}</span></td>
                <td><PerformanceModeBadge requested={job.requested_mode || 'fast'} effective={job.effective_mode || job.requested_mode || 'fast'} onExplain={() => setNotice(`请求模式：${(job.requested_mode || 'fast').toUpperCase()}\n当前模式：${(job.effective_mode || 'fast').toUpperCase()}\n原因：${job.mode_reason || 'system_default'}`)} /></td>
                <td><span className="chain-code">{job.chain.toUpperCase()}</span></td>
                <td><span className="dataset-cell" title={job.datasets.map(datasetLabel).join(', ')}>{job.datasets.map(datasetLabel).join(', ')}</span></td>
                <td>{Number(stats.subjects || stats.addresses || 0).toLocaleString()}</td>
                <td><div className="progress-cell"><span><i style={{ width: `${progress * 100}%` }} /></span><b>{(progress * 100).toFixed(0)}%</b></div></td>
                <td>{jobStage(job)}</td>
                <td>{Number(stats.downloaded_rows || 0).toLocaleString()}</td>
                <td>{Number(stats.api_requests || 0).toLocaleString()}</td>
                <td>${Number(stats.api_cost_usd || 0).toFixed(4)}</td>
                <td>{fmtDuration(Number(stats.elapsed_seconds || 0))}</td>
                <td className="table-time">{new Date(job.created_at).toLocaleString('zh-CN', { hour12: false })}</td>
                <td onClick={(event) => event.stopPropagation()}><details className="row-menu"><summary aria-label={`任务 ${job.job_no} 更多操作`}><MoreHorizontal size={16} /></summary><div><Link to={`/jobs/${job.id}`}>查看完整详情</Link><button onClick={() => void handleCopy(job.id)}><Copy size={13} />复制任务 ID</button>{job.status === 'RUNNING' && <button onClick={() => act(job, 'pause')}><Pause size={13} />暂停</button>}{resumable.has(job.status) && <button onClick={() => act(job, 'resume')}><Play size={13} />继续</button>}{!terminal.has(job.status) && <button className="danger-text" onClick={() => setConfirmIds([job.id])}><Trash2 size={13} />取消</button>}</div></details></td>
              </tr>
            })}
            {!query.isPending && jobs.length === 0 && <tr><td colSpan={15}><div className="table-empty"><Archive size={22} /><b>当前筛选条件下没有任务</b><button className="text-button" onClick={() => { setSearch(''); setChain('all'); setDataset('all'); setView('all') }}>清除筛选</button></div></td></tr>}
          </tbody>
        </table>
      </div>
      <footer className="table-footer"><span>共 {jobs.length.toLocaleString()} 条</span><div><button disabled>上一页</button><button className="active">1</button><button disabled>下一页</button></div></footer>
    </section>

    {selected.size > 0 && <div className="batch-bar"><b>已选择 {selected.size} 项</b><span /><button onClick={() => setNotice('所选任务的导出文件将在各任务详情中生成。')}><Download size={15} />批量导出</button><button className="danger" onClick={() => setConfirmIds(Array.from(selected))}><Trash2 size={15} />批量取消</button><button className="icon-button" aria-label="清除选择" onClick={() => setSelected(new Set())}><X size={16} /></button></div>}

    {preview && <><button className="drawer-scrim" aria-label="关闭任务预览" onClick={() => setPreviewId(null)} /><aside className="task-drawer" aria-label="任务快速预览">
      <header><div><small>任务快速预览</small><h2>{preview.job_no}</h2></div><button className="icon-button" aria-label="关闭" onClick={() => setPreviewId(null)}><X size={18} /></button></header>
      <div className="drawer-status"><span className={`status-badge status-${preview.status.toLowerCase()}`}><i />{statusLabel(preview.status)}</span><div className="progress-cell"><span><i style={{ width: `${progressOf(preview) * 100}%` }} /></span><b>{(progressOf(preview) * 100).toFixed(0)}%</b></div></div>
      <dl className="drawer-facts"><div><dt>链</dt><dd>{preview.chain.toUpperCase()}</dd></div><div><dt>运行模式</dt><dd><PerformanceModeBadge requested={preview.requested_mode || 'fast'} effective={preview.effective_mode || preview.requested_mode || 'fast'} onExplain={() => setNotice(`请求模式：${(preview.requested_mode || 'fast').toUpperCase()}\n当前模式：${(preview.effective_mode || 'fast').toUpperCase()}`)} /></dd></div><div><dt>Dataset</dt><dd>{preview.datasets.map(datasetLabel).join(', ')}</dd></div><div><dt>行数</dt><dd>{Number(preview.stats.downloaded_rows || 0).toLocaleString()}</dd></div><div><dt>请求数</dt><dd>{Number(preview.stats.api_requests || 0).toLocaleString()}</dd></div><div><dt>成本</dt><dd>${Number(preview.stats.api_cost_usd || 0).toFixed(5)}</dd></div><div><dt>创建时间</dt><dd>{new Date(preview.created_at).toLocaleString('zh-CN', { hour12: false })}</dd></div></dl>
      <section className="drawer-events"><h3>执行轨迹</h3><div className="signal-spine compact"><div className="ok"><i /><span>参数校验</span><b>完成</b></div><div className={preview.status === 'QUEUED' ? 'warn' : 'ok'}><i /><span>请求拉取</span><b>{jobStage(preview)}</b></div><div className={preview.status === 'COMPLETED' ? 'ok' : ''}><i /><span>导出文件</span><b>{preview.status === 'COMPLETED' ? '就绪' : '等待'}</b></div></div></section>
      <footer><Link className="primary-button" to={`/jobs/${preview.id}`}>查看完整详情</Link>{preview.status === 'RUNNING' && <button className="secondary-button" onClick={() => act(preview, 'pause')}>暂停任务</button>}{resumable.has(preview.status) && <button className="secondary-button" onClick={() => act(preview, 'resume')}>继续任务</button>}</footer>
    </aside></>}

    {confirmIds.length > 0 && <ConfirmDialog eyebrow="任务操作" title={`取消 ${confirmIds.length} 个任务`} confirmLabel="确认取消" onConfirm={cancelSelected} onCancel={() => setConfirmIds([])}><p>取消后当前执行会停止，已下载的数据不会自动删除。</p></ConfirmDialog>}
    {notice && <NoticeDialog title="任务操作" message={notice} onClose={() => setNotice('')} />}
    {copyToast && <CopyToast message={copyToast} onClose={() => setCopyToast('')} />}
  </div>
}
