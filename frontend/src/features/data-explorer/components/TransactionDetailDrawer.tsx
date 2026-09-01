import { useCallback, useEffect, useState } from 'react'
import { fetchTransactionDetail, type TransactionDetail } from '../api/transaction-detail'
import DecodedInputPanel from './DecodedInputPanel'
import { describeMethod, methodConfidenceLabel, methodSourceLabel, methodTypeLabel } from './method-display'

// TransactionDetailDrawer 交易详情（方案 §42）。
//
// 全部数据都走 TransactionDetailService：
// 前端与智能调查都不允许绕过它直接调用 Provider（§50）。
const OVERVIEW_FIELDS: { key: string; label: string; mono?: boolean }[] = [
  { key: 'block_number', label: '区块' },
  { key: 'timestamp', label: '时间' },
  { key: 'from_address', label: '发送方', mono: true },
  { key: 'to_address', label: '接收方', mono: true },
  { key: 'value_raw', label: '金额(原始)', mono: true },
  { key: 'gas_used', label: '已用 Gas' },
  { key: 'gas_price', label: 'Gas 价格', mono: true },
  { key: 'nonce', label: 'Nonce' },
  { key: 'status', label: '状态' },
  { key: 'source_provider', label: '数据来源' },
]

function fieldText(row: Record<string, unknown>, key: string): string {
  const value = row?.[key]
  if (value === null || value === undefined || value === '') return '—'
  if (key === 'status') return String(value) === '1' ? '成功' : '失败'
  if (key === 'timestamp') {
    const date = new Date(String(value))
    return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString()
  }
  return String(value)
}

export default function TransactionDetailDrawer({
  chain, txHash, onClose,
}: {
  chain: string
  txHash: string
  onClose: () => void
}) {
  const [detail, setDetail] = useState<TransactionDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [decoding, setDecoding] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async (mode: 'local_only' | 'auto' | 'force_refresh') => {
    if (mode === 'auto') setLoading(true)
    else if (mode === 'force_refresh') setDecoding(true)
    setError('')
    try {
      setDetail(await fetchTransactionDetail(chain, txHash, mode))
    } catch (reason) {
      setError(String(reason instanceof Error ? reason.message : reason))
    } finally {
      setLoading(false)
      setDecoding(false)
    }
  }, [chain, txHash])

  useEffect(() => { void load('auto') }, [load])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const method = detail ? describeMethod({
    method_id: detail.method.id, method_name: detail.method.name, method_signature: detail.method.signature,
    method_source: detail.method.source, method_verified: detail.method.verified,
    method_confidence: detail.method.confidence, method_type: detail.method.method_type,
  }) : null

  return (
    <div className="tx-drawer-backdrop" role="dialog" aria-modal="true" aria-label="交易详情" onClick={onClose}>
      <aside className="tx-drawer" onClick={(event) => event.stopPropagation()}>
        <header className="row between tx-drawer-head">
          <div>
            <h2>交易详情</h2>
            <p className="mono muted" title={txHash}>{txHash}</p>
          </div>
          <button className="ghost" onClick={onClose} aria-label="关闭">✕</button>
        </header>

        {loading && <p className="muted">加载中…</p>}
        {error && <div className="error">{error}</div>}

        {detail && method && (
          <div className="tx-drawer-body">
            {/* §42 Method Header：Method / Selector / Source 一目了然 */}
            <section className="method-header">
              <div>
                <span>Method</span>
                <b>{method.signature || method.primary}</b>
              </div>
              <div>
                <span>Selector</span>
                <b className="mono">{method.selector || '—'}</b>
              </div>
              <div>
                <span>Source</span>
                <b>{methodSourceLabel(method.source)}{method.verified ? ' · Verified' : ''}</b>
              </div>
              <div>
                <span>Confidence</span>
                <b>{methodConfidenceLabel(method.confidence)}</b>
              </div>
              <div>
                <span>类型</span>
                <b>{methodTypeLabel(method.methodType) || '—'}</b>
              </div>
              {detail.method.risk && <div><span>风险方法</span><b className="risk-flag">需关注</b></div>}
            </section>

            {method.signatureOnly && (
              <p className="method-note">Signature Match · Not Contract Verified —— 该方法名来自签名库匹配，未经过合约 ABI 验证。</p>
            )}

            <section className="tx-overview">
              <h3>Transaction Overview</h3>
              <dl>
                {OVERVIEW_FIELDS.map((field) => (
                  <div key={field.key}>
                    <dt>{field.label}</dt>
                    <dd className={field.mono ? 'mono' : ''}>{fieldText(detail.transaction, field.key)}</dd>
                  </div>
                ))}
              </dl>
            </section>

            <section className="tx-input">
              <h3>Input Data</h3>
              {/* §6：原始 input 完整保留，永远原样展示 */}
              <textarea readOnly value={detail.input || '0x'} rows={4} className="mono" />
            </section>

            <DecodedInputPanel detail={detail} decoding={decoding} onDeepDecode={() => void load('force_refresh')} />

            <section className="tx-logs">
              <h3>Event Logs（{detail.logs?.length ?? 0}）</h3>
              {(detail.logs?.length ?? 0) === 0 ? <p className="muted">该交易没有本地事件日志。</p> : (
                <table className="table">
                  <thead><tr><th>#</th><th>事件</th><th>合约</th><th>Topic0</th></tr></thead>
                  <tbody>
                    {detail.logs.map((log, index) => (
                      <tr key={index}>
                        <td>{String(log.log_index ?? index)}</td>
                        <td>{String(log.event_name ?? '') || <span className="muted">Unknown</span>}</td>
                        <td className="mono" title={String(log.address ?? '')}>{String(log.address ?? '').slice(0, 12)}…</td>
                        <td className="mono" title={String((log.topics as string[] | undefined)?.[0] ?? '')}>
                          {String((log.topics as string[] | undefined)?.[0] ?? '—').slice(0, 14)}…
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </section>
          </div>
        )}
      </aside>
    </div>
  )
}
