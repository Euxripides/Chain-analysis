import { lazy, Suspense, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { AgGridReact } from 'ag-grid-react'
import { AllCommunityModule, ModuleRegistry, themeQuartz, type ColDef, type RowClickedEvent } from 'ag-grid-community'
import { AnimatePresence, motion } from 'motion/react'
import { Activity, AlertTriangle, BadgeDollarSign, BookOpenCheck, Boxes, CircleGauge, DatabaseZap, ExternalLink, Gauge, RefreshCw, Search, ShieldAlert, SlidersHorizontal, X } from 'lucide-react'
import { apiControlHttp } from '../../api/client'
import type { AggregateItem, ControlState, Overview, RequestRow } from './types'
import './api-control.css'

ModuleRegistry.registerModules([AllCommunityModule])
const TrendChart = lazy(() => import('./TrendChart'))

const tabs = [
  ['overview', '总览', CircleGauge], ['providers', 'Provider', Boxes], ['operations', '操作', Activity],
  ['costs', '成本', BadgeDollarSign], ['errors', '错误', AlertTriangle], ['control', '管控', ShieldAlert],
  ['requests', '请求', Search], ['budgets', '预算', Gauge], ['alerts', '告警', BookOpenCheck],
] as const

const fmt = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })
const compact = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 2 })
const money = (v: number) => '$' + Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 6 })
const pct = (v: number) => (Number(v || 0) * 100).toFixed(2) + '%'
const dateTime = (v?: string | null) => v ? new Date(v).toLocaleString('zh-CN', { hour12: false }) : '—'

function QueryState({ pending, error, empty, children }: { pending: boolean; error?: Error | null; empty?: boolean; children: React.ReactNode }) {
  if (pending) return <div className="ac-skeleton"><i /><i /><i /></div>
  if (error) return <div className="ac-query-error"><AlertTriangle size={17} /><span>数据加载失败：{error.message}</span></div>
  if (empty) return <div className="ac-empty"><DatabaseZap size={24} /><b>当前范围没有记录</b><span>调整时间或筛选条件后重试。</span></div>
  return <>{children}</>
}

function SourceTag({ children, tone = 'internal' }: { children: React.ReactNode; tone?: 'official' | 'internal' | 'estimate' }) {
  return <span className={'ac-source ' + tone}>{children}</span>
}

function StatusBadge({ value }: { value: string }) { const key = value.toUpperCase(); return <span className={'ac-status s-' + key}>{value}</span> }

function StateRibbon({ state, stale }: { state?: ControlState; stale: boolean }) {
  const name = state?.state || 'UNKNOWN'
  return <section className={'ac-signal signal-' + name}>
    <div className="ac-signal-mark"><span /><ShieldAlert size={19} /></div>
    <div><small>自动管控状态</small><b>{name}</b></div>
    <div className="ac-signal-reason"><span>{state?.reason || '正在读取控制状态'}</span><small>5 分钟窗口 · {state?.window_requests ?? 0} 次请求 · 错误率 {pct(state?.error_rate ?? 0)}</small></div>
    <div className="ac-signal-flags"><span className={state?.automatic_protection ? 'on' : ''}>自动保护</span><span className={state?.background_paused ? 'risk' : ''}>后台任务{state?.background_paused ? '已暂停' : '运行中'}</span>{stale && <span className="risk">数据可能已过期</span>}</div>
  </section>
}

function OverviewTab({ filter, onDrill }: { filter: string; onDrill: (tab: string, operation?: string) => void }) {
  const query = useQuery({ queryKey: ['ac-overview', filter], queryFn: () => apiControlHttp<Overview>('/overview?' + filter), refetchInterval: 30_000 })
  const m = query.data?.metrics
  const cards = [
    ['逻辑调用', compact.format(m?.logical_calls || 0), '业务请求口径'], ['Provider 请求', compact.format(m?.provider_calls || 0), '真实外部调用'],
    ['成功率', pct(m?.success_rate || 0), `${fmt.format(m?.errors || 0)} 个错误`], ['P95 延迟', fmt.format(m?.p95_ms || 0) + ' ms', `P50 ${fmt.format(m?.p50_ms || 0)} ms`],
    ['消耗积分', compact.format(m?.credits || 0), '内部审计'], ['估算成本', money(m?.estimated_cost_usd || 0), '内部估算'],
  ]
  return <QueryState pending={query.isPending} error={query.error}>
    <div className="ac-kpis">{cards.map(([label, value, note], i) => <button key={label} className="ac-kpi" onClick={() => onDrill(i === 5 ? 'costs' : i === 2 ? 'errors' : i === 3 ? 'operations' : 'requests')}><span>{label}</span><b>{value}</b><small>{note}</small></button>)}</div>
    <div className="ac-grid-12">
      <section className="ac-panel ac-span-8"><div className="ac-panel-head"><div><small>流量与稳定性</small><h2>请求趋势</h2></div><SourceTag>内部审计</SourceTag></div><Suspense fallback={<div className="ac-chart-loading">正在加载图表…</div>}><TrendChart data={query.data?.trend || []} onSelect={() => onDrill('requests')} /></Suspense></section>
      <section className="ac-panel ac-span-4"><div className="ac-panel-head"><div><small>窗口诊断</small><h2>异常信号</h2></div></div><div className="ac-diagnostic"><div><span>重试</span><b>{fmt.format(m?.retries || 0)}</b></div><div><span>缓存命中</span><b>{fmt.format(m?.cache_hits || 0)}</b></div><div><span>P99 延迟</span><b>{fmt.format(m?.p99_ms || 0)} ms</b></div><div><span>数据新鲜度</span><b>{dateTime(query.data?.freshness)}</b></div></div><button className="ac-text-btn" onClick={() => onDrill('control')}>查看自动管控依据 →</button></section>
    </div>
  </QueryState>
}

type AggEndpoint = 'operations' | 'errors' | 'costs' | 'providers' | 'usage-types'
function AggregateTab({ endpoint, filter, onOpen }: { endpoint: AggEndpoint; filter: string; onOpen?: (label: string) => void }) {
  const q = useQuery({ queryKey: ['ac-aggregate', endpoint, filter], queryFn: () => apiControlHttp<{ items: AggregateItem[]; freshness: string }>('/' + endpoint + '?' + filter) })
  const labelKey = endpoint === 'operations' ? 'operation' : endpoint === 'errors' ? 'error_class' : endpoint === 'providers' ? 'provider' : 'usage_type'
  return <section className="ac-panel ac-table-panel"><div className="ac-panel-head"><div><small>按当前筛选实时聚合</small><h2>{endpoint === 'operations' ? '操作表现' : endpoint === 'errors' ? '错误分类' : endpoint === 'costs' ? '成本归因' : endpoint === 'providers' ? 'Provider 表现' : '用途分布'}</h2></div><div><SourceTag>{endpoint === 'costs' ? '内部审计' : '内部审计'}</SourceTag>{endpoint === 'costs' && <SourceTag tone="estimate">成本为估算</SourceTag>}</div></div><QueryState pending={q.isPending} error={q.error} empty={!q.data?.items.length}><div className="ac-responsive-table"><table><thead><tr><th>{labelKey === 'operation' ? '操作' : labelKey === 'error_class' ? '错误类型' : labelKey === 'provider' ? 'Provider' : '用途'}</th><th>请求</th><th>逻辑调用</th><th>错误</th><th>平均延迟</th><th>P95</th><th>积分</th><th>估算成本</th></tr></thead><tbody>{q.data?.items.map((item, i) => { const label = String(item[labelKey as keyof AggregateItem] || '未知'); return <tr key={label + i} onClick={() => onOpen?.(label)}><td><b className="ac-cell-title">{label}</b></td><td>{fmt.format(item.requests)}</td><td>{fmt.format(item.logical_calls)}</td><td className={item.errors ? 'ac-danger' : ''}>{fmt.format(item.errors)}</td><td>{fmt.format(item.avg_ms)} ms</td><td>{fmt.format(item.p95_ms)} ms</td><td>{fmt.format(item.credits)}</td><td>{money(item.estimated_cost_usd)}</td></tr>})}</tbody></table></div></QueryState></section>
}

function ProvidersTab({ filter }: { filter: string }) {
  const q = useQuery({ queryKey: ['ac-provider-accounts'], queryFn: () => apiControlHttp<{ accounts: { provider: string; status: string; official_available: boolean; stale: boolean; plan: string; remaining_credits: number; balance_usd: number; synced_at: string; source: string; console_url: string }[] }>('/provider-accounts') })
  return <><div className="ac-account-grid"><QueryState pending={q.isPending} error={q.error}>{q.data?.accounts.map((a) => <section className="ac-account" key={a.provider}><div className="ac-account-head"><div className="ac-provider-logo">A</div><div><b>{a.provider.toUpperCase()}</b><span>Provider 账户</span></div><StatusBadge value={a.status} /></div>{a.official_available ? <div className="ac-account-values"><div><span>套餐</span><b>{a.plan || '—'}</b></div><div><span>剩余积分</span><b>{fmt.format(a.remaining_credits)}</b></div><div><span>余额</span><b>{money(a.balance_usd)}</b></div></div> : <div className="ac-official-empty"><AlertTriangle size={18} /><div><b>官方账户数据未接入</b><span>不会使用内部成本估算冒充官方余额。</span></div></div>}<footer><SourceTag tone="official">官方数据</SourceTag><span>同步：{dateTime(a.synced_at)}</span>{a.console_url && <a href={a.console_url} target="_blank" rel="noreferrer">Provider 控制台 <ExternalLink size={13} /></a>}</footer></section>)}</QueryState></div><AggregateTab endpoint="providers" filter={filter} /></>
}

function RequestsTab({ filter, preset }: { filter: string; preset?: { key: string; value: string } }) {
  const [offset, setOffset] = useState(0); const [selected, setSelected] = useState<string | null>(null); const [search, setSearch] = useState('')
  const extra = preset?.value ? `&${preset.key}=${encodeURIComponent(preset.value)}` : ''
  const q = useQuery({ queryKey: ['ac-requests', filter, offset, extra], queryFn: () => apiControlHttp<{ requests: RequestRow[]; total: number; limit: number; offset: number }>(`/requests?${filter}&limit=50&offset=${offset}${extra}`) })
  const detail = useQuery({ queryKey: ['ac-request-detail', selected], queryFn: () => apiControlHttp<{ request: RequestRow & Record<string, unknown>; attempts: (RequestRow & Record<string, unknown>)[] }>('/requests/' + selected), enabled: !!selected })
  const rows = useMemo(() => (q.data?.requests || []).filter((r) => !search || [r.method, r.trace_id, r.request_id, r.error_class].join(' ').toLowerCase().includes(search.toLowerCase())), [q.data, search])
  const cols = useMemo<ColDef<RequestRow>[]>(() => [
    { field: 'request_time', headerName: '时间', minWidth: 170, valueFormatter: (p) => dateTime(p.value) }, { field: 'provider', headerName: 'Provider', width: 115 }, { field: 'chain', headerName: '链', width: 85 },
    { field: 'method', headerName: '操作', minWidth: 210, flex: 1 }, { field: 'usage_type', headerName: '用途', minWidth: 150 }, { field: 'status', headerName: '状态', width: 115 },
    { field: 'duration_ms', headerName: '延迟', width: 105, valueFormatter: (p) => `${fmt.format(p.value)} ms` }, { field: 'retry_attempt', headerName: '重试', width: 80 },
    { field: 'credits', headerName: '积分', width: 100, valueFormatter: (p) => fmt.format(p.value) }, { field: 'cost_usd', headerName: '估算成本', width: 120, valueFormatter: (p) => money(p.value) },
  ], [])
  return <section className="ac-panel ac-table-panel"><div className="ac-panel-head"><div><small>服务端分页 · 点击行查看调用链</small><h2>请求审计</h2></div><SourceTag>内部审计</SourceTag></div><div className="ac-local-search"><Search size={15} /><input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="搜索当前页操作、Trace ID、错误…" /></div><QueryState pending={q.isPending} error={q.error} empty={!rows.length}><div className="ac-grid-table"><AgGridReact theme={themeQuartz.withParams({ accentColor: '#2563EB', headerBackgroundColor: '#F8FAFC', borderColor: '#E6EAF0', rowHoverColor: '#F5F8FF', fontFamily: 'Inter, PingFang SC, Microsoft YaHei, sans-serif' })} rowData={rows} columnDefs={cols} rowHeight={48} headerHeight={42} onRowClicked={(e: RowClickedEvent<RequestRow>) => setSelected(e.data?.id || null)} /></div><div className="ac-pager"><span>共 {fmt.format(q.data?.total || 0)} 条 · 第 {Math.floor(offset / 50) + 1} 页</span><div><button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}>上一页</button><button disabled={offset + 50 >= (q.data?.total || 0)} onClick={() => setOffset(offset + 50)}>下一页</button></div></div></QueryState><AnimatePresence>{selected && <motion.div className="ac-drawer-backdrop" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} onClick={() => setSelected(null)}><motion.aside className="ac-drawer" initial={{ x: 520 }} animate={{ x: 0 }} exit={{ x: 520 }} transition={{ type: 'spring', damping: 28, stiffness: 300 }} onClick={(e) => e.stopPropagation()}><header><div><small>请求详情</small><h2>{detail.data?.request.method || '加载中…'}</h2></div><button aria-label="关闭" onClick={() => setSelected(null)}><X /></button></header><QueryState pending={detail.isPending} error={detail.error}><div className="ac-detail-grid">{detail.data && Object.entries(detail.data.request).map(([k, v]) => <div key={k}><span>{k}</span><b>{typeof v === 'boolean' ? (v ? '是' : '否') : String(v || '—')}</b></div>)}</div><h3>尝试时间线</h3><div className="ac-timeline">{detail.data?.attempts.map((a, i) => <div key={i}><i /><span>尝试 {i + 1}</span><b>{a.status} · {a.duration_ms} ms</b><small>{dateTime(a.request_time)}</small></div>)}</div></QueryState></motion.aside></motion.div>}</AnimatePresence></section>
}

function ControlTab() {
  const qc = useQueryClient(); const state = useQuery({ queryKey: ['ac-state'], queryFn: () => apiControlHttp<ControlState>('/control/states'), refetchInterval: 15_000, staleTime: 30_000 }); const actions = useQuery({ queryKey: ['ac-actions'], queryFn: () => apiControlHttp<{ actions: Record<string, unknown>[] }>('/control/actions') })
  const [mode, setMode] = useState('DEGRADED'); const [ttl, setTTL] = useState(30); const [reason, setReason] = useState(''); const [killReason, setKillReason] = useState('')
  const refresh = () => { qc.invalidateQueries({ queryKey: ['ac-state'] }); qc.invalidateQueries({ queryKey: ['ac-actions'] }); qc.invalidateQueries({ queryKey: ['ac-overview'] }) }
  const override = useMutation({ mutationFn: () => apiControlHttp('/control/overrides', { method: 'POST', body: JSON.stringify({ mode, ttl_minutes: ttl, reason }) }), onSuccess: () => { setReason(''); refresh() } })
  const kill = useMutation({ mutationFn: (enabled: boolean) => apiControlHttp('/control/kill-switch', { method: 'PATCH', body: JSON.stringify({ enabled, reason: killReason }) }), onSuccess: () => { setKillReason(''); refresh() } })
  const policy = useMutation({ mutationFn: (enabled: boolean) => apiControlHttp('/control/policies', { method: 'PATCH', body: JSON.stringify({ automatic_protection: enabled }) }), onSuccess: refresh })
  return <div className="ac-grid-12"><section className="ac-panel ac-span-7"><div className="ac-panel-head"><div><small>自动异常保护</small><h2>实时控制面</h2></div><StatusBadge value={state.data?.state || 'UNKNOWN'} /></div><QueryState pending={state.isPending} error={state.error}><div className="ac-control-state"><b>{state.data?.state}</b><span>{state.data?.reason}</span><small>错误率 {pct(state.data?.error_rate || 0)} · P95 {fmt.format(state.data?.p95_ms || 0)} ms · 自 {dateTime(state.data?.since)}</small></div><label className="ac-switch-row"><span><b>自动保护</b><small>异常时自动进入观察、降级或隔离状态</small></span><input type="checkbox" checked={!!state.data?.automatic_protection} onChange={(e) => policy.mutate(e.target.checked)} /></label><div className="ac-warning"><AlertTriangle size={17} /><span>人工覆盖会暂时绕过自动决策。必须填写原因，并在 TTL 到期后自动恢复。</span></div><div className="ac-form-row"><label><span>覆盖模式</span><select value={mode} onChange={(e) => setMode(e.target.value)}><option>OBSERVE</option><option>DEGRADED</option><option>ISOLATED</option><option>PROTECTED</option><option>RECOVERING</option><option>NORMAL</option></select></label><label><span>有效期（分钟）</span><input type="number" min={1} max={1440} value={ttl} onChange={(e) => setTTL(Number(e.target.value))} /></label></div><label className="ac-field"><span>接管原因</span><textarea rows={3} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="说明影响、依据和恢复条件" /></label>{override.error && <div className="ac-inline-error">{override.error.message}</div>}<button className="ac-primary" disabled={!reason || override.isPending} onClick={() => override.mutate()}>确认人工覆盖</button><div className="ac-kill-zone"><div><b>全局停机开关</b><span>只用于外部 API 异常或预算失控；启用后后台任务进入保护状态。</span></div><input value={killReason} onChange={(e) => setKillReason(e.target.value)} placeholder="填写启用/解除原因" /><button className={state.data?.kill_switch ? '' : 'ac-danger-btn'} disabled={!killReason || kill.isPending} onClick={() => kill.mutate(!state.data?.kill_switch)}>{state.data?.kill_switch ? '解除停机' : '启用停机'}</button></div></QueryState></section><section className="ac-panel ac-span-5"><div className="ac-panel-head"><div><small>不可篡改操作留痕</small><h2>最近动作</h2></div></div><QueryState pending={actions.isPending} error={actions.error} empty={!actions.data?.actions.length}><div className="ac-action-list">{actions.data?.actions.slice(0, 20).map((a, i) => <div key={String(a.id || i)}><i /><div><b>{String(a.action)}</b><span>{String(a.reason || a.from_state + ' → ' + a.to_state)}</span><small>{String(a.actor)} · {dateTime(String(a.created_at))}</small></div><SourceTag tone={a.source === 'automatic' ? 'internal' : 'official'}>{String(a.source)}</SourceTag></div>)}</div></QueryState></section></div>
}

function BudgetsTab() {
  const qc = useQueryClient(); const q = useQuery({ queryKey: ['ac-budgets'], queryFn: () => apiControlHttp<{ budgets: { id: string; name: string; provider: string; limit_usd: number; used_usd: number; utilization: number; warn_percent: number; source: string }[] }>('/budgets') }); const [name, setName] = useState(''); const [limit, setLimit] = useState(10)
  const add = useMutation({ mutationFn: () => apiControlHttp('/budgets', { method: 'POST', body: JSON.stringify({ name, limit_usd: limit, provider: 'all', period: 'monthly', warn_percent: 80 }) }), onSuccess: () => { setName(''); qc.invalidateQueries({ queryKey: ['ac-budgets'] }) } })
  return <div className="ac-grid-12"><section className="ac-panel ac-span-8"><div className="ac-panel-head"><div><small>月度预算与阈值</small><h2>预算使用</h2></div><SourceTag tone="estimate">内部估算</SourceTag></div><QueryState pending={q.isPending} error={q.error} empty={!q.data?.budgets.length}><div className="ac-budget-list">{q.data?.budgets.map((b) => <div key={b.id}><header><div><b>{b.name}</b><span>{b.provider}</span></div><strong>{money(b.used_usd)} / {money(b.limit_usd)}</strong></header><div className="ac-progress"><i style={{ width: Math.min(100, b.utilization * 100) + '%' }} /></div><footer><span>已使用 {pct(b.utilization)}</span><span>告警阈值 {b.warn_percent}%</span></footer></div>)}</div></QueryState></section><section className="ac-panel ac-span-4"><div className="ac-panel-head"><div><small>预算护栏</small><h2>新增预算</h2></div></div><label className="ac-field"><span>名称</span><input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：Ankr 月度预算" /></label><label className="ac-field"><span>限额（USD）</span><input type="number" min={0.01} value={limit} onChange={(e) => setLimit(Number(e.target.value))} /></label><button className="ac-primary" disabled={!name || limit <= 0 || add.isPending} onClick={() => add.mutate()}>创建预算</button></section></div>
}

function AlertsTab() {
  const qc = useQueryClient(); const q = useQuery({ queryKey: ['ac-alerts'], queryFn: () => apiControlHttp<{ alerts: { id: string; severity: string; title: string; message: string; status: string; last_seen_at: string }[] }>('/alerts') }); const action = useMutation({ mutationFn: ({ id, action }: { id: string; action: string }) => apiControlHttp(`/alerts/${id}/actions`, { method: 'POST', body: JSON.stringify({ action, ttl_minutes: 60 }) }), onSuccess: () => qc.invalidateQueries({ queryKey: ['ac-alerts'] }) })
  return <section className="ac-panel"><div className="ac-panel-head"><div><small>异常通知和处置</small><h2>告警中心</h2></div></div><QueryState pending={q.isPending} error={q.error} empty={!q.data?.alerts.length}><div className="ac-alert-list">{q.data?.alerts.map((a) => <div key={a.id}><AlertTriangle /><div><header><b>{a.title}</b><StatusBadge value={a.severity} /></header><p>{a.message}</p><small>{dateTime(a.last_seen_at)} · {a.status}</small></div><div><button onClick={() => action.mutate({ id: a.id, action: 'mute' })}>静默 1 小时</button><button onClick={() => action.mutate({ id: a.id, action: 'resolve' })}>标记解决</button></div></div>)}</div></QueryState></section>
}

export default function ApiControlPage() {
  const [params, setParams] = useSearchParams(); const tab = params.get('tab') || 'overview'; const [range, setRange] = useState(params.get('range') || '24h'); const [provider, setProvider] = useState(params.get('provider') || 'all'); const [chain, setChain] = useState(params.get('chain') || 'all'); const [preset, setPreset] = useState<{ key: string; value: string } | undefined>(() => params.get('operation') ? { key: 'operation', value: params.get('operation')! } : params.get('error_class') ? { key: 'error_class', value: params.get('error_class')! } : undefined)
  const filter = new URLSearchParams({ range, provider, chain }).toString(); const stateQ = useQuery({ queryKey: ['ac-state'], queryFn: () => apiControlHttp<ControlState>('/control/states'), refetchInterval: 15_000, staleTime: 30_000 })
  const setTab = (next: string) => setParams({ tab: next, range, provider, chain })
  const selectTab = (next: string) => { setPreset(undefined); setTab(next) }
  const drill = (next: string, value?: string) => { setPreset(value ? { key: next === 'requests' ? 'operation' : next, value } : undefined); setTab(next) }
  return <div className="ac-page"><header className="ac-title"><div><span>API CONTROL CENTER</span><h1>API 控制中心</h1><p>真实请求审计、成本归因与异常自动管控</p></div><button className="ac-refresh" onClick={() => stateQ.refetch()}><RefreshCw size={16} /> 刷新状态</button></header><StateRibbon state={stateQ.data} stale={stateQ.isStale} /><nav className="ac-tabs" aria-label="API 控制中心分区">{tabs.map(([key, label, Icon]) => <button key={key} className={tab === key ? 'active' : ''} onClick={() => selectTab(key)}><Icon size={16} /><span>{label}</span></button>)}</nav><section className="ac-filters"><div className="ac-filter-title"><SlidersHorizontal size={16} /><span>全局筛选</span></div><label><span>时间范围</span><select value={range} onChange={(e) => setRange(e.target.value)}><option value="1h">最近 1 小时</option><option value="6h">最近 6 小时</option><option value="24h">最近 24 小时</option><option value="7d">最近 7 天</option><option value="30d">最近 30 天</option></select></label><label><span>Provider</span><select value={provider} onChange={(e) => setProvider(e.target.value)}><option value="all">全部 Provider</option><option value="ankr">Ankr</option><option value="local-cache">本地缓存</option></select></label><label><span>链</span><select value={chain} onChange={(e) => setChain(e.target.value)}><option value="all">全部链</option><option value="bsc">BSC</option><option value="ethereum">Ethereum</option></select></label><div className="ac-freshness">自动刷新 <b>30s</b></div></section><main className="ac-tab-content">{tab === 'overview' && <OverviewTab filter={filter} onDrill={drill} />}{tab === 'providers' && <ProvidersTab filter={filter} />}{tab === 'operations' && <AggregateTab endpoint="operations" filter={filter} onOpen={(v) => { setPreset({ key: 'operation', value: v }); setTab('requests') }} />}{tab === 'costs' && <AggregateTab endpoint="costs" filter={filter} />}{tab === 'errors' && <AggregateTab endpoint="errors" filter={filter} onOpen={(v) => { setPreset({ key: 'error_class', value: v }); setTab('requests') }} />}{tab === 'control' && <ControlTab />}{tab === 'requests' && <RequestsTab filter={filter} preset={preset} />}{tab === 'budgets' && <BudgetsTab />}{tab === 'alerts' && <AlertsTab />}</main></div>
}
