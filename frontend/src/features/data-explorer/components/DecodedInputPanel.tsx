import { useState } from 'react'
import type { DecodedParam, TransactionDetail } from '../api/transaction-detail'
import { methodSourceLabel } from './method-display'

// DecodedInputPanel Decoded Input 展示（方案 §21/§41/§42/§43）。
//
// 数值以字符串原样展示：uint256 精度绝不能被 JS Number 破坏（§41/§62-13）。
function renderValue(value: unknown): string {
  if (value === null || value === undefined) return '—'
  if (typeof value === 'string') return value
  if (typeof value === 'boolean') return value ? 'true' : 'false'
  return JSON.stringify(value)
}

function boundedValue(value: unknown, remaining: { value: number }, depth = 0): { value: unknown; truncated: boolean } {
  if (remaining.value <= 0 || depth > 16) return { value: '…', truncated: true }
  remaining.value--
  if (Array.isArray(value)) {
    const out: unknown[] = []
    let truncated = false
    for (const item of value) {
      if (remaining.value <= 0) { truncated = true; break }
      const child = boundedValue(item, remaining, depth + 1)
      out.push(child.value)
      truncated ||= child.truncated
    }
    if (out.length < value.length) out.push(`… 其余 ${value.length - out.length} 项`)
    return { value: out, truncated: truncated || out.length < value.length }
  }
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    let truncated = false
    for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
      if (remaining.value <= 0) { truncated = true; break }
      const child = boundedValue(item, remaining, depth + 1)
      out[key] = child.value
      truncated ||= child.truncated
    }
    return { value: out, truncated }
  }
  return { value, truncated: false }
}

function ValueCell({ value }: { value: unknown }) {
  const [limit, setLimit] = useState(40)
  const structured = Array.isArray(value) || (!!value && typeof value === 'object')
  if (!structured) return <>{renderValue(value)}</>
  const preview = boundedValue(value, { value: limit })
  return (
    <>
      <span>{JSON.stringify(preview.value)}</span>
      {preview.truncated && limit < 2000 && (
        <button className="ghost sm decoded-load-more" onClick={() => setLimit((current) => Math.min(2000, current + 100))}>
          加载更多
        </button>
      )}
    </>
  )
}

function ParamRows({ params }: { params: DecodedParam[] }) {
  return (
    <table className="table decoded-table">
      <thead>
        <tr><th>参数</th><th>类型</th><th>值</th></tr>
      </thead>
      <tbody>
        {params.map((param, index) => (
          <tr key={`${param.name}-${index}`}>
            <td>{param.name || `arg${index}`}</td>
            <td className="mono">{param.type}</td>
            <td className="mono decoded-value"><ValueCell value={param.value} /></td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

export default function DecodedInputPanel({
  detail, onDeepDecode, decoding,
}: {
  detail: TransactionDetail
  onDeepDecode: () => void
  decoding: boolean
}) {
  const params = detail.decoded_params ?? []
  const canDeepDecode = params.length === 0 || detail.method.source === 'unknown'

  return (
    <section className="decoded-panel">
      <div className="row between decoded-head">
        <h3>Decoded Input</h3>
        <span className="muted">
          {params.length > 0
            ? `来源：${methodSourceLabel(detail.decode_source)}${detail.decode_cache_hit ? ' · 本地缓存' : ''}`
            : '本地暂无解码结果'}
        </span>
      </div>

      {params.length > 0 ? <ParamRows params={params} /> : (
        <p className="muted">
          该交易尚未解码。本地 ABI 与签名库无法还原参数时，可按需向 Provider 请求一次深度解析。
        </p>
      )}

      {/* §43：method_source = unknown 或无解码结果时提供深度解析入口 */}
      {canDeepDecode && (
        <div className="row gap decoded-actions">
          <button className="primary" disabled={decoding} onClick={onDeepDecode}>
            {decoding ? '解析中…' : '尝试深度解析'}
          </button>
          <span className="muted">深度解析会消耗一次 Provider 额度，结果会被缓存，同一交易不会重复请求。</span>
        </div>
      )}

      {detail.decode_error && <div className="error">深度解析失败：{detail.decode_error}</div>}

      {(detail.decoded_logs?.length ?? 0) > 0 && (
        <div className="decoded-logs">
          <h4>Decoded Event Logs</h4>
          {detail.decoded_logs?.map((log) => (
            <div key={`${log.log_index}-${log.topic0}`} className="decoded-log">
              <div className="row between">
                <b>{log.event_name || log.topic0.slice(0, 12) + '…'}</b>
                <span className="muted mono">#{log.log_index} · {log.address.slice(0, 10)}…</span>
              </div>
              {log.event_signature && <div className="muted mono">{log.event_signature}</div>}
              {(log.params?.length ?? 0) > 0 && <ParamRows params={log.params ?? []} />}
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
