import { useEffect, useRef, useState } from 'react'
import { AlertTriangle, Check, Copy } from 'lucide-react'
import type { VolumeGuard } from '../types'
import { fmtDuration } from '../types'
import { datasetLabel } from '../utils/field-labels'
import { copyText } from '../utils/clipboard'

type Props = { guard: VolumeGuard; busy: boolean; onContinue: () => void; onAbort: () => void; onClose: () => void }

export default function VolumeGuardDialog({ guard, busy, onContinue, onAbort, onClose }: Props) {
  const ref = useRef<HTMLElement>(null)
  const closeRef = useRef<HTMLButtonElement>(null)
  const [confirmExtreme, setConfirmExtreme] = useState(false)
  const [copied, setCopied] = useState(false)
  const extreme = guard.risk_level === 'EXTREME' || guard.risk_level === 'SUPER_EXTREME'

  useEffect(() => {
    closeRef.current?.focus()
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) onClose()
      if (event.key !== 'Tab') return
      const items = ref.current?.querySelectorAll<HTMLElement>('button:not(:disabled)')
      if (!items?.length) return
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [busy, onClose])

  const continueClick = () => {
    if (extreme && !confirmExtreme) { setConfirmExtreme(true); return }
    onContinue()
  }

  const copyAddress = async () => {
    await copyText(guard.subject)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1800)
  }

  return <div className="notice-backdrop volume-guard-backdrop" role="presentation">
    <section ref={ref} className="notice-dialog volume-guard-dialog" role="alertdialog" aria-modal="true" aria-labelledby="volume-guard-title" aria-describedby="volume-guard-summary">
      <header><span><AlertTriangle size={18} /></span><div><small>TRON VOLUME GUARD · {guard.risk_level}</small><h2 id="volume-guard-title">检测到异常高活跃地址</h2></div><button ref={closeRef} className="icon-button" aria-label="关闭" disabled={busy} onClick={onClose}>×</button></header>
      <p id="volume-guard-summary" className="volume-guard-summary">系统已安全暂停这个地址的 {datasetLabel(guard.dataset)} 下载，避免继续产生大量 API、时间和存储消耗。其他地址不受影响。</p>
      <div className="volume-guard-address"><span><small>地址</small><b title={guard.subject}>{guard.subject}</b></span><button type="button" aria-label={`复制地址 ${guard.subject}`} onClick={() => void copyAddress()}>{copied ? <Check size={15} /> : <Copy size={15} />}{copied ? '已复制' : '复制'}</button></div>
      <dl className="volume-guard-metrics">
        <div><dt>当前已下载</dt><dd>{guard.actual_rows.toLocaleString()} 条</dd></div>
        <div><dt>完成 Shards</dt><dd>{guard.completed_shards} / {guard.total_shards}</dd></div>
        <div><dt>预计总量</dt><dd>{guard.projected_rows_low.toLocaleString()} ~ {guard.projected_rows_high.toLocaleString()} 条</dd></div>
        <div><dt>预测置信度</dt><dd>{(guard.confidence * 100).toFixed(0)}%</dd></div>
        <div><dt>预计总耗时</dt><dd>{fmtDuration(guard.projected_duration_seconds)}</dd></div>
        <div><dt>预计 Provider 请求</dt><dd>{guard.projected_provider_calls.toLocaleString()}</dd></div>
        <div><dt>预计成本</dt><dd>${guard.projected_cost_usd.toFixed(4)}</dd></div>
        <div><dt>风险等级</dt><dd className="danger-text">{guard.risk_level}</dd></div>
      </dl>
      {confirmExtreme && <p className="volume-guard-extreme">预计数据超过 10M 条，继续可能产生较高 API 和存储消耗。请再次确认继续。</p>}
      <footer><button className="ghost danger-text" disabled={busy} onClick={onAbort}>{busy ? '处理中…' : '放弃任务'}</button><button className="primary" disabled={busy} onClick={continueClick}>{confirmExtreme ? '确认继续完整下载' : '继续完整下载'}</button></footer>
    </section>
  </div>
}
