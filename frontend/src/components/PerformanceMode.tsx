import { useState } from 'react'
import { useScopedQuery as useQuery } from '../lib/sessionScope'
import { ShieldCheck } from 'lucide-react'
import { api } from '../api/client'
import NoticeDialog from './NoticeDialog'
import type { AnkrProductionProfile, PerformanceMode, PerformanceModeState } from '../types'

const modeMeta: Record<PerformanceMode, { label: string; caption: string }> = {
  safe: { label: 'SAFE', caption: '稳定优先' },
  fast: { label: 'FAST · 推荐', caption: '性能与稳定性平衡' },
  turbo: { label: 'TURBO', caption: '速度优先' },
}
const modeLevel: Record<PerformanceMode, number> = { safe: 1, fast: 2, turbo: 3 }

export function useAnkrProductionProfile(enabled = true) {
  return useQuery({ queryKey: ['ankr-production-profile'], queryFn: api.getAnkrProductionProfile, staleTime: 60_000, enabled })
}

export function PerformanceModeSelector({ value, onChange, profile, disabled = false }: {
  value: PerformanceMode
  onChange: (mode: PerformanceMode) => void
  profile?: AnkrProductionProfile
  disabled?: boolean
}) {
  return <div className="performance-mode-selector" role="radiogroup" aria-label="运行模式">
    {(Object.keys(modeMeta) as PerformanceMode[]).map((mode) => {
      const target = profile?.modes[mode].target_concurrency
      return <label key={mode} className={`performance-mode-option mode-${mode} ${value === mode ? 'selected' : ''}`}>
        <input type="radio" name="performance-mode" value={mode} checked={value === mode} disabled={disabled} onChange={() => onChange(mode)} />
        <span>{modeMeta[mode].label}</span><small>{modeMeta[mode].caption}</small>
        <b>{target ? `全局目标 ${target}` : '读取配置中'}</b>
      </label>
    })}
  </div>
}

export function PerformanceModeBadge({ requested, effective, onExplain }: {
  requested?: PerformanceMode
  effective?: PerformanceMode
  onExplain?: () => void
}) {
  const shown = effective || requested || 'fast'
  const degraded = !!requested && !!effective && modeLevel[effective] < modeLevel[requested]
  if (!degraded) return <span className={`performance-mode-badge mode-${shown}`}>{shown.toUpperCase()}</span>
  return <button type="button" className={`performance-mode-badge mode-${shown} degraded`} onClick={(event) => { event.stopPropagation(); onExplain?.() }}>
    {shown.toUpperCase()}<ShieldCheck size={12} />自动保护
  </button>
}

export function PerformanceModeStatus({ state, profile, editable, onChange }: {
  state: PerformanceModeState
  profile?: AnkrProductionProfile
  editable?: boolean
  onChange?: (mode: PerformanceMode) => Promise<void> | void
}) {
  const [explain, setExplain] = useState(false)
  const [busy, setBusy] = useState(false)
  const degraded = modeLevel[state.effective_mode] < modeLevel[state.requested_mode]
  async function change(mode: PerformanceMode) {
    if (!onChange || mode === state.requested_mode) return
    setBusy(true)
    try { await onChange(mode) } finally { setBusy(false) }
  }
  return <>
    <div className="performance-mode-status">
      <div><small>请求模式</small><b>{state.requested_mode.toUpperCase()}</b></div>
      <span>→</span>
      <div><small>当前模式</small><PerformanceModeBadge requested={state.requested_mode} effective={state.effective_mode} onExplain={() => setExplain(true)} /></div>
      <div><small>全局目标并发</small><b>{state.target_concurrency}</b></div>
      <div><small>生产上限</small><b>{profile?.hard_ceiling ?? state.hard_ceiling ?? '—'}</b></div>
      {editable && <div className="performance-mode-actions">{(['safe', 'fast', 'turbo'] as PerformanceMode[]).map((mode) => <button key={mode} disabled={busy} className={state.requested_mode === mode ? 'active' : ''} onClick={() => void change(mode)}>{mode.toUpperCase()}</button>)}</div>}
    </div>
    {explain && <NoticeDialog title="Ankr 自动保护" message={degraded ? `系统检测到 API 或本地资源压力，已从 ${state.requested_mode.toUpperCase()} 切换至 ${state.effective_mode.toUpperCase()}。任务会继续执行，无需人工处理。` : `当前运行在 ${state.effective_mode.toUpperCase()} 模式。`} onClose={() => setExplain(false)} />}
  </>
}
