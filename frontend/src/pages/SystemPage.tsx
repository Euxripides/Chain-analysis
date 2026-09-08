import { lazy, Suspense, useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Activity, AlertTriangle, ArrowUpRight, Check, Database, RefreshCw, Server, ShieldCheck } from 'lucide-react'
import { api, apiControlHttp } from '../api/client'
import NoticeDialog from '../components/NoticeDialog'
import type { AggregateItem, Overview } from '../features/api-control/types'

const TrendChart = lazy(() => import('../features/api-control/TrendChart'))
const compact = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 2 })
const exact = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })
const pct = (value: number) => (Number(value || 0) * 100).toFixed(2) + '%'
const money = (value: number) => '$' + Number(value || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 6 })

export default function SystemPage() {
  const [notice, setNotice] = useState('')
  const ready = useQuery({ queryKey: ['ready'], queryFn: async () => { const response = await fetch('/health/ready'); if (!response.ok) throw new Error('系统未就绪'); return response.json() as Promise<{ status: string; checks: Record<string, string> }> }, refetchInterval: 15_000, staleTime: 20_000 })
  const provider = useQuery({ queryKey: ['provider-status'], queryFn: api.providerStatus, refetchInterval: 15_000, staleTime: 20_000 })
  const overview = useQuery({ queryKey: ['ac-overview', 'system'], queryFn: () => apiControlHttp<Overview>('/overview?range=24h'), refetchInterval: 30_000, staleTime: 30_000 })
  const account = useQuery({ queryKey: ['ac-provider-accounts'], queryFn: () => apiControlHttp<{ accounts: { provider: string; official_available: boolean; remaining_credits: number | null; balance_usd: number | null }[] }>('/provider-accounts') })
  const errors = useQuery({ queryKey: ['ac-errors', 'system'], queryFn: () => apiControlHttp<{ items: AggregateItem[] }>('/errors?range=24h') })
  const coverage = useQuery({ queryKey: ['coverage-stats'], queryFn: async () => { const response = await fetch('/api/v1/coverage/stats'); if (!response.ok) throw new Error('Coverage 加载失败'); return response.json() as Promise<{ stats: Record<string, number> }> }, refetchInterval: 30_000 })
  const tokens = useQuery({ queryKey: ['token-enrichment'], queryFn: api.tokenEnrichment, refetchInterval: 30_000 })

  useEffect(() => {
    const error = ready.error || provider.error || overview.error || coverage.error || tokens.error
    if (error) setNotice(error.message)
  }, [ready.error, provider.error, overview.error, coverage.error, tokens.error])

  const metrics = overview.data?.metrics
  const systemOK = ready.data?.status === 'ready' || ready.data?.status === 'ok'
  const providerState = String(provider.data?.state || 'UNKNOWN')
  const providerOK = ['NORMAL', 'CLOSED', 'HEALTHY'].includes(providerState)
  const stats = coverage.data?.stats || {}
  const coveredBlocks = Object.entries(stats).filter(([key]) => key.endsWith(':blocks')).reduce((sum, [, value]) => sum + Number(value || 0), 0)
  const coveredRows = Object.entries(stats).filter(([key]) => key.endsWith(':rows')).reduce((sum, [, value]) => sum + Number(value || 0), 0)
  const tokenTotal = tokens.data?.token_contracts || 0
  const tokenRate = tokenTotal ? (tokens.data?.complete || 0) / tokenTotal : 0
  const recentErrors = (errors.data?.items || []).filter((item) => item.error_class && item.error_class !== 'none' && item.errors > 0)
  const accountState = account.data?.accounts[0]

  const health = [
    { label: 'API 服务', value: systemOK ? '正常' : ready.isPending ? '检查中' : '异常', ok: systemOK, to: '/system' },
    { label: 'Provider 连接', value: providerOK ? '正常' : providerState, ok: providerOK, to: '/api-control?tab=providers' },
    { label: 'Token Metadata', value: pct(tokenRate), ok: tokenRate >= .98, to: '/system?view=assets' },
    { label: '数据覆盖', value: compact.format(coveredBlocks) + ' Blocks', ok: coveredBlocks > 0, to: '/data-explorer' },
    { label: '自动管控', value: overview.data?.control.state || '检查中', ok: overview.data?.control.state === 'NORMAL', to: '/api-control?tab=control' },
  ]

  return <div className="workspace-page status-workspace">
    <header className="route-header">
      <div><h1>状态总览</h1><p>系统、Provider、数据覆盖与 API 运行状态</p></div>
      <div className="route-actions"><span className={`live-status${systemOK && providerOK ? '' : ' warn'}`}><i />{systemOK && providerOK ? '系统运行正常' : '需要检查'}</span><button className="secondary-button" onClick={() => Promise.all([ready.refetch(), provider.refetch(), overview.refetch()])}><RefreshCw size={15} />刷新</button></div>
    </header>

    <section className="health-ribbon" aria-label="核心运行状态">
      <div><span className={systemOK ? 'health-orb ok' : 'health-orb danger'}><Check size={15} /></span><small>系统状态</small><b>{systemOK ? '正常运行' : '检查中'}</b></div>
      <div><small>Provider</small><b>{String(provider.data?.provider || 'Ankr')}</b></div>
      <div><small>自动管控</small><b>{overview.data?.control.state || '—'}</b></div>
      <div><small>审计成功率</small><b>{pct(metrics?.success_rate || 0)}</b></div>
      <div><small>最后更新</small><b>{overview.data?.freshness ? new Date(overview.data.freshness).toLocaleTimeString('zh-CN', { hour12: false }) : '—'}</b></div>
    </section>

    <div className="status-canvas">
      <section className="usage-stage">
        <header className="stage-header">
          <div><span>API 请求量</span><h2>{compact.format(metrics?.logical_calls || 0)} <small>Calls</small></h2><Link className="text-button" to="/api-control">查看请求趋势 <ArrowUpRight size={14} /></Link></div>
          <div className="segmented-control" aria-label="趋势时间范围"><button className="active">24 小时</button><button>7 天</button><button>30 天</button></div>
        </header>
        <Suspense fallback={<div className="workspace-skeleton chart"><i /><i /><i /></div>}><TrendChart data={overview.data?.trend || []} /></Suspense>
        <div className="metric-strip">
          <div><small>当前速率</small><b>{exact.format(metrics?.provider_calls || 0)}</b></div>
          <div><small>错误</small><b className="danger-text">{exact.format(metrics?.errors || 0)}</b></div>
          <div><small>P95</small><b>{exact.format(metrics?.p95_ms || 0)} ms</b></div>
          <div><small>估算成本</small><b>{money(metrics?.estimated_cost_usd || 0)}</b></div>
        </div>
      </section>

      <aside className="signal-rail">
        <header><div><Activity size={17} /><h2>系统健康</h2></div><Link to="/api-control?tab=alerts">全部检查项</Link></header>
        <div className="signal-spine">
          {health.map((item) => <Link to={item.to} key={item.label} className={item.ok ? 'ok' : 'warn'}><i /><span>{item.label}</span><b>{item.value}</b><ArrowUpRight size={13} /></Link>)}
        </div>
        <div className="rail-section-title"><span>今日异常</span><Link to="/api-control?tab=errors">{recentErrors.reduce((sum, item) => sum + item.errors, 0)} 次</Link></div>
        <div className="diagnostic-list">
          {recentErrors.slice(0, 3).map((item) => <Link to={`/api-control?tab=requests&error_class=${encodeURIComponent(item.error_class || '')}`} key={item.error_class}><AlertTriangle size={15} /><span>{item.error_class}</span><b>{item.errors}</b></Link>)}
          {!recentErrors.length && <div className="empty-row"><ShieldCheck size={16} /><span>当前窗口无异常</span></div>}
        </div>
      </aside>
    </div>

    <section className="asset-ledger" aria-label="数据资产摘要">
      <header><Database size={17} /><h2>数据资产</h2></header>
      <div className="asset-ledger-row">
        <Link to="/api-control?tab=providers"><small>API Credits</small><b>{accountState?.official_available && accountState.remaining_credits != null ? compact.format(accountState.remaining_credits) : '未接入'}</b><span>{accountState?.balance_usd == null ? '官网账户余额' : money(accountState.balance_usd)}</span></Link>
        <Link to="/data-explorer"><small>Data Coverage</small><b>{compact.format(coveredBlocks)} Blocks</b><span>{compact.format(coveredRows)} 行</span></Link>
        <Link to="/system?view=assets"><small>Token Metadata</small><b>{pct(tokenRate)}</b><span>{compact.format(tokens.data?.complete || 0)} / {compact.format(tokenTotal)}</span></Link>
      </div>
    </section>
    {notice && <NoticeDialog title="状态数据未完整加载" message={notice} onClose={() => setNotice('')} />}
  </div>
}
