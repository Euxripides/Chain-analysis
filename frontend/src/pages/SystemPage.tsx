import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Activity, AlertTriangle, Boxes, CheckCircle2, CircleGauge, Coins, Database, ExternalLink, ShieldCheck } from 'lucide-react'
import { api, apiControlHttp } from '../api/client'
import type { AggregateItem, Overview } from '../features/api-control/types'

const number = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 2 })
const exact = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })
const pct = (n: number) => (Number(n || 0) * 100).toFixed(2) + '%'
const money = (n: number) => '$' + Number(n || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 6 })

function HealthCard({ icon: Icon, label, value, note, tone = 'ok', to }: { icon: typeof Activity; label: string; value: string; note: string; tone?: 'ok' | 'warn' | 'danger'; to: string }) {
  return <Link className={'status-health-card ' + tone} to={to}><div className="status-health-icon"><Icon size={18} /></div><span>{label}</span><b>{value}</b><small>{note}</small><i>查看详情 →</i></Link>
}

export default function SystemPage() {
  const ready = useQuery({ queryKey: ['ready'], queryFn: async () => { const r = await fetch('/health/ready'); if (!r.ok) throw new Error('系统未就绪'); return r.json() as Promise<{ status: string; checks: Record<string, string> }> }, refetchInterval: 15_000, staleTime: 20_000 })
  const provider = useQuery({ queryKey: ['provider-status'], queryFn: api.providerStatus, refetchInterval: 15_000, staleTime: 20_000 })
  const overview = useQuery({ queryKey: ['ac-overview', 'system'], queryFn: () => apiControlHttp<Overview>('/overview?range=24h'), refetchInterval: 30_000, staleTime: 30_000 })
  const account = useQuery({ queryKey: ['ac-provider-accounts'], queryFn: () => apiControlHttp<{ accounts: { provider: string; status: string; official_available: boolean; plan: string; remaining_credits: number | null; balance_usd: number | null; synced_at: string }[] }>('/provider-accounts') })
  const errors = useQuery({ queryKey: ['ac-errors', 'system'], queryFn: () => apiControlHttp<{ items: AggregateItem[] }>('/errors?range=24h') })
  const coverage = useQuery({ queryKey: ['coverage-stats'], queryFn: async () => { const r = await fetch('/api/v1/coverage/stats'); if (!r.ok) throw new Error('Coverage 加载失败'); return r.json() as Promise<{ stats: Record<string, number> }> }, refetchInterval: 30_000 })
  const tokens = useQuery({ queryKey: ['token-enrichment'], queryFn: api.tokenEnrichment, refetchInterval: 30_000 })

  const systemOK = ready.data?.status === 'ready' || ready.data?.status === 'ok'
  const providerState = String(provider.data?.state || 'UNKNOWN')
  const control = overview.data?.control
  const metrics = overview.data?.metrics
  const acc = account.data?.accounts[0]
  const tokenTotal = tokens.data?.token_contracts || 0
  const tokenComplete = tokens.data?.complete || 0
  const tokenRate = tokenTotal ? tokenComplete / tokenTotal : 0
  const stats = coverage.data?.stats || {}
  const coveredBlocks = Object.entries(stats).filter(([k]) => k.endsWith(':blocks')).reduce((sum, [, v]) => sum + Number(v || 0), 0)
  const coveredRows = Object.entries(stats).filter(([k]) => k.endsWith(':rows')).reduce((sum, [, v]) => sum + Number(v || 0), 0)
  const recentErrors = (errors.data?.items || []).filter((item) => item.error_class && item.error_class !== 'none' && item.errors > 0)

  return <div className="page status-overview">
    <header className="status-title"><div><span>STATUS OVERVIEW</span><h1>状态总览</h1><p>系统健康、Provider、数据覆盖与 API 状态</p></div><span className={ready.isError || overview.isError ? 'status-live warn' : 'status-live'}><i />{ready.isError || overview.isError ? '部分数据异常' : '实时监测中'}</span></header>
    <section className="status-health-grid">
      <HealthCard icon={CheckCircle2} label="System" value={systemOK ? 'HEALTHY' : ready.isPending ? 'CHECKING' : 'UNHEALTHY'} note={`${Object.keys(ready.data?.checks || {}).length} 个基础设施检查`} tone={systemOK ? 'ok' : 'danger'} to="/system" />
      <HealthCard icon={Boxes} label="Provider" value={providerState} note={String(provider.data?.provider || 'Ankr')} tone={providerState === 'NORMAL' || providerState === 'CLOSED' || providerState === 'HEALTHY' ? 'ok' : providerState === 'UNKNOWN' ? 'warn' : 'danger'} to="/api-control?tab=providers" />
      <HealthCard icon={ShieldCheck} label="Auto Control" value={control?.state || 'UNKNOWN'} note={control?.reason || '正在读取控制状态'} tone={control?.state === 'NORMAL' ? 'ok' : control?.state === 'OBSERVE' || control?.state === 'DEGRADED' ? 'warn' : 'danger'} to="/api-control?tab=control" />
      <HealthCard icon={CircleGauge} label="Audit" value={pct(metrics?.success_rate || 0)} note={`${exact.format(metrics?.errors || 0)} 个错误 · P95 ${exact.format(metrics?.p95_ms || 0)} ms`} tone={(metrics?.success_rate || 0) >= .99 ? 'ok' : 'warn'} to="/api-control?tab=requests" />
    </section>
    <section className="status-summary-grid">
      <Link className="status-summary-card" to="/api-control?tab=providers"><header><div><Coins size={18} /><span>Ankr 官网账户余额</span></div><span className={acc?.official_available ? 'status-origin official' : 'status-origin unavailable'}>{acc?.official_available ? '官网快照' : '未接入'}</span></header>{acc?.official_available ? <><b>{acc.remaining_credits == null ? '—' : number.format(acc.remaining_credits)} Credits</b><p>{acc.plan || '—'} · 剩余余额 {acc.balance_usd == null ? '—' : money(acc.balance_usd)}</p></> : <><b>官网账户余额未配置</b><p>不会用本地估算消耗冒充剩余额度</p></>}<footer>查看 Provider 详情 <ExternalLink size={12} /></footer></Link>
      <Link className="status-summary-card" to="/api-control"><header><div><Activity size={18} /><span>本地审计 24h</span></div><span className="status-origin internal">非官网总账</span></header><b>{number.format(metrics?.logical_calls || 0)} Calls</b><p>本地捕获 {number.format(metrics?.provider_calls || 0)} 次外部请求 · 估算成本 {money(metrics?.estimated_cost_usd || 0)}</p><footer>打开 API 控制中心 <ExternalLink size={12} /></footer></Link>
      <Link className="status-summary-card" to="/system"><header><div><Database size={18} /><span>Token Metadata</span></div><span className="status-origin internal">本地数据</span></header><b>{pct(tokenRate)} Complete</b><p>{number.format(tokenComplete)} / {number.format(tokenTotal)} · 队列 {number.format(tokens.data?.queue_depth || 0)}</p><footer>查看元数据状态 <ExternalLink size={12} /></footer></Link>
      <Link className="status-summary-card" to="/data-explorer"><header><div><Database size={18} /><span>Coverage</span></div><span className="status-origin internal">本地数据</span></header><b>{number.format(coveredBlocks)} Blocks</b><p>{number.format(coveredRows)} 行已纳入本地覆盖</p><footer>进入数据浏览 <ExternalLink size={12} /></footer></Link>
    </section>
    <section className="status-recent"><header><div><AlertTriangle size={17} /><div><span>Recent Errors / Warnings</span><small>最近 24 小时 · 内部审计</small></div></div><Link to="/api-control?tab=errors">查看全部 →</Link></header>{errors.isPending ? <div className="status-loading">正在读取近期异常…</div> : recentErrors.length ? <div>{recentErrors.slice(0, 5).map((item) => <Link key={item.error_class} to={`/api-control?tab=requests&error_class=${encodeURIComponent(item.error_class || '')}`}><span>{item.error_class}</span><b>{item.errors} 次</b><small>P95 {exact.format(item.p95_ms)} ms</small></Link>)}</div> : <div className="status-no-errors"><CheckCircle2 size={18} /><span>当前窗口没有记录到 API 错误</span></div>}</section>
  </div>
}
