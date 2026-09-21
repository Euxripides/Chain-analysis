import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Check, ChevronLeft, Copy, Download, FileJson, MoreHorizontal, Pause, Play, RefreshCw, Search, ShieldAlert, TrendingDown, X } from 'lucide-react'
import { api } from '../api/client'
import CopyToast from '../components/CopyToast'
import NoticeDialog from '../components/NoticeDialog'
import { PerformanceModeBadge, PerformanceModeStatus, useAnkrProductionProfile } from '../components/PerformanceMode'
import { fmtBytes, fmtDuration, friendlyError, statusLabel, type DatasetActual, type DatasetEstimate, type DatasetItem, type PerformanceMode, type Shard, type Snapshot } from '../types'
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

const BOTTLENECK_LABEL: Record<string, string> = {
  PROVIDER: 'Ankr Provider', LOCAL_SPOOL: '本地 Spool', PARSER: '本地解析',
  CLICKHOUSE: 'ClickHouse', BACKPRESSURE: '背压保护', UNKNOWN: '未知',
}

function pct(part?: number, total?: number) {
  if (!total || total <= 0) return 0
  return Math.min(100, Math.max(0, ((part ?? 0) / total) * 100))
}

// V1.1 三阶段进度（§29/§31/§66：COMPLETE 以 Validation 完成为准）
function StageProgress({ snapshot, jobStatus }: { snapshot: Snapshot | null | undefined; jobStatus: string }) {
  const total = snapshot?.total_shards || 0
  const fetched = snapshot?.fetched_shards || 0
  const ingested = snapshot?.ingested_shards || 0
  const validated = snapshot?.validated_shards || 0
  const fetchPct = pct(fetched, total)
  const ingestPct = pct(ingested, fetched)
  const validatePct = pct(validated, ingested)
  const complete = snapshot?.validation_status === 'COMPLETE' || jobStatus === 'COMPLETED'
  const stages = [
    { label: 'Fetch', pct: fetchPct, rows: snapshot?.fetched_rows, done: fetched, of: total },
    { label: 'Ingest', pct: ingestPct, rows: snapshot?.ingested_rows, done: ingested, of: fetched },
    { label: 'Validate', pct: validatePct, rows: snapshot?.validated_rows, done: validated, of: ingested },
  ]
  return <section className="stage-progress" aria-label="三阶段进度">
    <header><h2>Fetch / Ingest / Validate</h2>{complete
      ? <span className="success-text"><Check size={14} />Validation 100% · COMPLETE</span>
      : <span>{snapshot?.validation_status === 'VALIDATING' ? '校验中…' : '执行中…'}</span>}</header>
    <div className="stage-list">
      {stages.map((s) => <div className="stage-row" key={s.label}>
        <div className="stage-head"><b>{s.label}</b><span>{s.done.toLocaleString()} / {s.of.toLocaleString()} Shards · {Number(s.rows || 0).toLocaleString()} 行</span><em>{s.pct.toFixed(0)}%</em></div>
        <div className="stage-bar"><i style={{ width: s.pct + '%' }} /></div>
      </div>)}
    </div>
  </section>
}

// V1.1 控制状态横幅（§32-§35：PROTECTED / DEGRADED / RECOVERING 必须解释原因）
function ControlBanner({ snapshot }: { snapshot: Snapshot | null | undefined }) {
  const state = snapshot?.control_state
  if (!state || state === 'NORMAL') return null
  const isProtected = state === 'PROTECTED'
  const icon = isProtected ? <ShieldAlert size={16} /> : state === 'RECOVERING' ? <TrendingDown size={16} /> : <Activity size={16} />
  const title = isProtected ? '下载暂时暂停 · 数据安全'
    : state === 'DEGRADED' ? '自动降速中'
    : '正在恢复'
  const hint = isProtected ? '等待 Ingest Drain，恢复：自动'
    : state === 'DEGRADED' ? '已自动降低并发/PageSize，稳定后逐步恢复'
    : '逐步提升并发，下一次评估约 1 分钟内'
  return <section className={'control-banner control-' + state.toLowerCase()} role="status">
    {icon}<div><b>{state} · {title}</b><span>原因：{snapshot?.control_reason || '—'}。{hint}</span></div>
  </section>
}

const EST_SOURCE_LABEL: Record<string, string> = { chainbase: 'Chainbase', cache: 'Count Cache', profile: 'Sweet Spot 画像', fallback: 'V1.1 回退' }
const RAMP_LABEL: Record<string, string> = { warming: '爬升中', stable: '已稳定', rollback: '回退冷却', disabled: '关闭' }

// V1.2 Workload Estimate Card（§76/§77/§79：Estimate Source / Predicted / Initial Shards）
function WorkloadEstimate({ datasets }: { datasets: DatasetItem[] }) {
  const items = datasets.filter((d) => d.stats && (d.stats as Record<string, unknown>).estimate)
  if (!items.length) return null
  return <section className="v12-card workload-card" aria-label="Workload Estimate">
    <header><div><h2>Workload Estimate</h2><span>Chainbase Count Oracle · 只用于 Planning（§24/§25）</span></div></header>
    <div className="estimate-grid">
      {items.map((item) => {
        const stats = item.stats as Record<string, unknown>
        const est = (stats.estimate ?? {}) as DatasetEstimate
        const actual = (stats.actual ?? {}) as DatasetActual
        const src = EST_SOURCE_LABEL[est.source ?? ''] ?? est.source ?? '—'
        const done = item.status === 'COMPLETED' || item.status === 'SKIPPED'
        return <div className="estimate-block" key={item.id}>
          <div className="estimate-head"><b>{datasetLabel(item.dataset)}</b><span className={est.profile_hit ? 'est-profile-hit' : 'est-profile-miss'}>{est.profile_hit ? 'Sweet Spot HIT' : 'Profile MISS'}</span></div>
          <div className="estimate-row"><span>Estimate Source</span><em>{src}</em></div>
          <div className="estimate-row"><span>Predicted Rows</span><em>{est.predicted_rows != null && est.predicted_rows >= 0 ? Number(est.predicted_rows).toLocaleString() : '—'}</em></div>
          <div className="estimate-row"><span>Predicted Pages</span><em>{est.predicted_pages != null && est.predicted_pages >= 0 ? est.predicted_pages : '—'}</em></div>
          <div className="estimate-row"><span>Initial Shards</span><em>{est.initial_shards ?? '—'}</em>{est.preferred_shards ? <small>画像 preferred {est.preferred_shards}</small> : null}</div>
          <div className="estimate-row"><span>Predicted vs Actual</span><em>{actual.actual_rows != null
            ? <>{Number(actual.actual_rows).toLocaleString()}{actual.deviation_pct != null ? <b className={(actual.deviation_pct || 0) > 0.005 ? 'danger-text' : 'success-text'}> {(actual.deviation_pct || 0).toFixed(2)}%</b> : null}</>
            : <>{done ? '—' : '下载中…'}</>}</em></div>
          <div className="estimate-note" title={est.decision_reason}>原因：{est.decision_reason ?? '—'}</div>
        </div>
      })}
    </div>
  </section>
}

// V1.2 Global Concurrency Card（§80：Ankr Fetch Capacity / Global Ceiling 600）
function GlobalConcurrencyCard({ snapshot }: { snapshot: Snapshot | null | undefined }) {
  if (!snapshot || !snapshot.global_ceiling) return null
  const active = snapshot.global_active_slots ?? 0
  const target = snapshot.global_concurrency_target ?? 0
  const ceiling = snapshot.global_ceiling ?? 0
  const ramp = RAMP_LABEL[snapshot.global_ramp_phase ?? ''] ?? snapshot.global_ramp_phase ?? '—'
  const control = snapshot.control_state ?? 'NORMAL'
  return <section className="v12-card global-card" aria-label="Ankr Fetch Capacity">
    <header><div><h2>Ankr Fetch Capacity</h2><span>全局并发 · 所有链/地址共享 Ceiling（§33）</span></div><span className={control === 'NORMAL' ? 'est-profile-hit' : 'est-profile-miss'}>Control {control}</span></header>
    <div className="global-metrics">
      <div><small>Active</small><b>{active}</b></div>
      <div><small>Safe Target</small><b>{target}</b></div>
      <div><small>Hard Ceiling</small><b>{ceiling}</b></div>
      <div><small>Ramp</small><b>{ramp}</b></div>
      <div><small>Time→First Fetch</small><b>{snapshot.time_to_first_fetch_ms != null ? fmtDuration(snapshot.time_to_first_fetch_ms / 1000) : '—'}</b></div>
      <div><small>Time→Full Safe Concurrency</small><b>{snapshot.time_to_full_safe_concurrency_ms ? fmtDuration(snapshot.time_to_full_safe_concurrency_ms / 1000) : '—'}</b></div>
      <div><small>Planning API Cost</small><b>{'$' + Number(snapshot.planning_api_cost_usd || 0).toFixed(6)}</b></div>
      <div><small>Acquisition API Cost</small><b>{'$' + Number(snapshot.acquisition_api_cost_usd || 0).toFixed(5)}</b></div>
    </div>
  </section>
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
  const [drawer, setDrawer] = useState<Shard | null>(null)
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['job', id], queryFn: () => api.getJob(id!), refetchInterval: 3000, enabled: !!id })
  const profileQuery = useAnkrProductionProfile(!!query.data)

  useEffect(() => {
    if (!id) return
    const events = new EventSource('/api/v1/download-jobs/' + id + '/events')
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
    try { await api.setJobPerformanceMode(id, mode); await queryClient.invalidateQueries({ queryKey: ['job', id] }) }
    catch (error) { setNotice(error instanceof Error ? error.message : String(error)) }
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
    setExporting(dataset + ':' + format)
    try {
      const created = await api.createExport(id, dataset, format)
      const timer = window.setInterval(async () => {
        try {
          const current = await api.getExport(created.id)
          if (current.status === 'COMPLETED') { window.clearInterval(timer); setExporting(''); window.open('/api/v1/exports/' + created.id + '/download', '_blank') }
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
  const performance = query.data.performance || { requested_mode: job.requested_mode || 'fast', effective_mode: effectiveMode, target_concurrency: profileQuery.data?.modes[effectiveMode].target_concurrency || 32, mode_reason: job.mode_reason }

  return <div className="job-detail-workspace">
    <header className="job-detail-header">
      <div className="job-heading-row"><Link className="icon-button" to="/jobs" aria-label="返回任务中心"><ChevronLeft size={18} /></Link><div><div className="job-title-line"><h1>{job.job_no}</h1><span className="chain-code">{job.chain.toUpperCase()}</span><span className={'status-badge status-' + job.status.toLowerCase()}><i />{statusLabel(job.status)}</span><PerformanceModeBadge requested={job.requested_mode || 'fast'} effective={effectiveMode} onExplain={() => setNotice(`请求模式：${(job.requested_mode || 'fast').toUpperCase()}\n当前模式：${effectiveMode.toUpperCase()}\n原因：${job.mode_reason || 'system_default'}`)} /></div><p>{job.datasets.map(datasetLabel).join(', ')} · {subjects.length} 地址 <i /> 创建于 {new Date(job.created_at).toLocaleString('zh-CN', { hour12: false })}</p></div></div>
      <div className="route-actions">
        {job.status === 'RUNNING' && <button className="secondary-button" onClick={() => act('pause')}><Pause size={15} />暂停任务</button>}
        {(job.status === 'PAUSED' || job.status === 'PAUSED_FALLBACK_TOO_EXPENSIVE') && <button className="secondary-button" onClick={() => act('resume')}><Play size={15} />继续任务</button>}
        <button className="primary-button" disabled={!!exporting || job.datasets.length === 0} onClick={() => doExport(job.datasets[0], 'csv')}><Download size={15} />导出全部</button>
        <button className="icon-button" aria-label="更多任务操作"><MoreHorizontal size={18} /></button>
      </div>
    </header>

    <ControlBanner snapshot={snapshot} />

    <section className="core-metrics" aria-label="核心指标">
      <div><small>总体进度</small><b className="success-text">{(progress * 100).toFixed(1)}%</b></div>
      <div><small>已用时间</small><b>{fmtDuration(snapshot?.elapsed_seconds || 0)}</b></div>
      <div><small>预计剩余</small><b>{snapshot?.eta_seconds ? fmtDuration(snapshot.eta_seconds) : '—'}</b></div>
      <div><small>总成本</small><b>{'$' + Number(snapshot?.api_cost_usd || 0).toFixed(5)}</b></div>
      <div><small>Tail ETA</small><b>{snapshot?.tail_eta_seconds ? fmtDuration(snapshot.tail_eta_seconds) : '—'}</b></div>
      <div><small>活跃 Shards</small><b>{snapshot?.active_shards ?? 0}</b></div>
      <div><small>Retry Amp</small><b>{(snapshot?.retry_amp ?? 1).toFixed(2)}</b></div>
      <div><small>全局 Active / Target</small><b>{snapshot?.global_active_slots ?? 0} / {snapshot?.global_concurrency_target ?? '—'}</b></div>
      <div><small>Global Ceiling</small><b>{snapshot?.global_ceiling ?? '—'}</b></div>
    </section>

    <nav className="detail-tabs" aria-label="任务详情分区">{tabs.map(([key, label]) => <button key={key} className={tab === key ? 'active' : ''} onClick={() => setTab(key)}>{label}</button>)}</nav>

    {tab === 'overview' && <div className="detail-overview-grid">
      <main>
        <WorkloadEstimate datasets={datasets} />
        <GlobalConcurrencyCard snapshot={snapshot} />
        <StageProgress snapshot={snapshot} jobStatus={job.status} />

        <section className="engine-facts">
          <header><h2>引擎观测</h2><span>V1.1</span></header>
          <div className="engine-fact-grid">
            <div><small>当前瓶颈</small><b>{BOTTLENECK_LABEL[snapshot?.bottleneck ?? 'UNKNOWN'] ?? '未知'}</b>{snapshot?.bottleneck_detail ? <em>{snapshot.bottleneck_detail}</em> : null}</div>
            <div><small>Provider 平均响应</small><b>{snapshot?.provider_latency_avg_ms ? fmtDuration(snapshot.provider_latency_avg_ms / 1000) : '—'}</b></div>
            <div><small>Page Gap</small><b>{snapshot?.page_gap_avg_ms != null ? snapshot.page_gap_avg_ms + 'ms' : '—'}</b></div>
            <div><small>PageSize</small><b>{snapshot?.page_size_current || '—'}</b></div>
            <div><small>Fetch 速率</small><b>{Number(snapshot?.rows_per_sec || 0).toLocaleString()}/s</b></div>
            <div><small>Ingest 速率</small><b>{Number(snapshot?.write_rows_per_sec || 0).toLocaleString()}/s</b></div>
          </div>
        </section>

        <ProviderUsageCard snapshot={snapshot} />

        <section className="pipeline-stage">
          <header><h2>执行 Pipeline</h2><span>{snapshot?.done_shards || 0} / {snapshot?.total_shards || shards.length} Shards</span></header>
          <div className="pipeline-track">{phases.map((phase, index) => { const state = phaseState(index, progress, job.status); return <div className={'pipeline-node ' + state} key={phase}><span>{state === 'success' ? <Check size={15} /> : index + 1}</span><b>{phase}</b><small>{state === 'success' ? '已完成' : state === 'running' ? '执行中' : state === 'failed' ? '失败' : '等待'}</small>{index < phases.length - 1 && <i />}</div> })}</div>
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
      <header><div><h2>Shards</h2><span>{visibleShards.length} 条 · 点击行查看 Range 详情</span></div><select aria-label="筛选 Shard 状态" name="shard-status" autoComplete="off" value={shardFilter} onChange={(event) => setShardFilter(event.target.value)}><option value="all">全部状态</option><option value="COMPLETED">已完成</option><option value="RUNNING">运行中</option><option value="FETCHED">拉取完成</option><option value="INGESTED">入库中</option><option value="FAILED_FINAL">失败</option></select></header>
      <div className="task-table-scroll"><table className="task-table"><thead><tr><th>Dataset</th><th>Range（区块）</th><th>状态</th><th>行数</th><th>页</th><th>Wire / Decoded</th><th>Page Gap</th><th>重试</th><th>更新时间</th></tr></thead><tbody>{visibleShards.map((shard) => <ShardRow key={shard.id} shard={shard} onError={setNotice} onOpen={() => setDrawer(shard)} />)}</tbody></table></div>
    </section>}

    {tab === 'logs' && <section className="detail-table-panel log-panel">
      <header><div><h2>任务日志</h2><span>{events.length} 条</span></div><label className="task-search"><Search size={15} /><input name="job-log-search" autoComplete="off" value={logSearch} onChange={(event) => setLogSearch(event.target.value)} placeholder="搜索 Dataset / 状态 / 错误…" /></label></header>
      <div className="signal-spine event-list full">{events.map((shard) => <button className={shard.error ? 'warn' : 'ok'} key={shard.id} onClick={() => shard.error && setNotice(friendlyError(shard.error))}><i /><time>{new Date(shard.updated_at).toLocaleString('zh-CN', { hour12: false })}</time><span>{datasetLabel(shard.dataset)} · {statusLabel(shard.status)}</span><b>{shard.error ? friendlyError(shard.error) : shard.downloaded_rows.toLocaleString() + ' 行'}</b></button>)}</div>
    </section>}

    {tab === 'exports' && <section className="exports-workspace"><header><div><h2>生成导出文件</h2><span>{job.datasets.length} 个 Dataset</span></div></header>{job.datasets.map((dataset) => <div className="export-row" key={dataset}><FileJson size={18} /><div><b>{datasetLabel(dataset)}</b><span>{Number(snapshot?.downloaded_rows || 0).toLocaleString()} 行</span></div><button className="secondary-button" disabled={!!exporting} onClick={() => doExport(dataset, 'csv')}>CSV</button><button className="secondary-button" disabled={!!exporting} onClick={() => doExport(dataset, 'xlsx')}>XLSX</button></div>)}</section>}

    {notice && <NoticeDialog title="任务详情" message={notice} onClose={() => setNotice('')} />}
    {copyToast && <CopyToast message={copyToast} onClose={() => setCopyToast('')} />}
    {drawer && <RangeDrawer shard={drawer} onClose={() => setDrawer(null)} />}
  </div>
}

function ProviderUsageCard({ snapshot }: { snapshot: Snapshot | null | undefined }) {
	const render = (title: string, rows: NonNullable<Snapshot['provider_usage_60s']> = []) => <div className="usage-window"><h3>{title}</h3><div className="task-table-scroll"><table className="task-table"><thead><tr><th>来源</th><th>用途</th><th>请求</th><th>QPS</th><th>P50 / P95 / P99</th><th>429 / 错误 / 重试</th></tr></thead><tbody>{rows.map((row) => <tr key={row.provider + ':' + row.usage_type}><td>{row.provider}</td><td>{row.usage_type}</td><td>{row.requests.toLocaleString()}</td><td>{row.qps.toFixed(2)}</td><td>{Math.round(row.p50_ms)} / {Math.round(row.p95_ms)} / {Math.round(row.p99_ms)} ms</td><td>{row.rate_limits} / {row.errors} / {row.retries}</td></tr>)}</tbody></table></div></div>
	return <section className="engine-facts provider-usage"><header><h2>Provider 请求构成</h2><span>实时滚动窗口</span></header>{render('最近 60 秒', snapshot?.provider_usage_60s)}{render('最近 5 分钟', snapshot?.provider_usage_5m)}</section>
}

function ShardRow({ shard, onError, onOpen }: { shard: Shard; onError: (message: string) => void; onOpen: () => void }) {
  return <tr className={shard.error ? 'failed' : ''} onClick={() => (shard.error ? onError(friendlyError(shard.error)) : onOpen())}>
    <td>{datasetLabel(shard.dataset)}</td><td className="mono">{shard.from_block.toLocaleString()} → {shard.to_block.toLocaleString()}</td><td><span className={'status-badge status-' + shard.status.toLowerCase()}><i />{statusLabel(shard.status)}</span></td><td>{shard.downloaded_rows.toLocaleString()}</td><td>{shard.page_count ?? shard.page_no}</td><td>{fmtBytes(shard.wire_bytes || 0)} / {fmtBytes(shard.decoded_bytes || 0)}</td><td>{shard.page_gap_avg_ms != null ? shard.page_gap_avg_ms + 'ms' : '—'}</td><td>{shard.retries} / {shard.max_retries}</td><td className="table-time">{new Date(shard.updated_at).toLocaleString('zh-CN', { hour12: false })}</td>
  </tr>
}

// V1.1 Range Detail Drawer（§40：不显示真实 pageToken）
function RangeDrawer({ shard, onClose }: { shard: Shard; onClose: () => void }) {
  const rows: Array<[string, string]> = [
    ['状态', statusLabel(shard.status)],
    ['Range Start', shard.from_block.toLocaleString()],
    ['Range End', shard.to_block.toLocaleString()],
    ['Pages', String(shard.page_count ?? shard.page_no)],
    ['Rows', shard.downloaded_rows.toLocaleString()],
    ['Wire Bytes', fmtBytes(shard.wire_bytes || 0)],
    ['Decoded Bytes', fmtBytes(shard.decoded_bytes || 0)],
    ['PageSize', shard.page_size ? String(shard.page_size) : '—'],
    ['Page Gap Avg', shard.page_gap_avg_ms != null ? shard.page_gap_avg_ms + 'ms' : '—'],
    ['Fetch 耗时', shard.fetch_duration_ms ? fmtDuration(shard.fetch_duration_ms / 1000) : '—'],
    ['Ingest 耗时', shard.ingest_duration_ms ? fmtDuration(shard.ingest_duration_ms / 1000) : '—'],
    ['Checkpoint Page', String(shard.page_no)],
    ['Retries', shard.retries + ' / ' + shard.max_retries],
  ]
  return <div className="drawer-scrim" onClick={onClose}>
    <aside className="range-drawer" onClick={(e) => e.stopPropagation()} role="dialog" aria-label="Range 详情">
      <header><div><h2>Range 详情</h2><span>{datasetLabel(shard.dataset)} · {shard.id.slice(0, 8)}</span></div><button className="icon-button" aria-label="关闭" onClick={onClose}><X size={16} /></button></header>
      <dl className="range-drawer-dl">{rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>
      <p className="range-drawer-note">出于安全考虑不展示 pageToken（仅存储哈希）。</p>
    </aside>
  </div>
}
