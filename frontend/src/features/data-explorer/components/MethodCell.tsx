import { useCallback, useRef, useState } from 'react'
import MethodTooltip from './MethodTooltip'
import { describeMethod, type MethodRow } from './method-display'

// MethodCell Data Explorer 的 Method 列（方案 §26/§27）。
//
//   已解析：Transfer / 0xa9059cbb
//   未解析：0x12ab34cd / Unknown
//
// 永远不留空，且 Unknown 不会被伪装成已解析方法（§62-19/§62-20）。
const TOOLTIP_WIDTH = 280

export default function MethodCell({ row, onOpenDetail }: { row: MethodRow; onOpenDetail?: () => void }) {
  const anchor = useRef<HTMLButtonElement>(null)
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null)
  const method = describeMethod(row)

  const show = useCallback(() => {
    const rect = anchor.current?.getBoundingClientRect()
    if (!rect) return
    const left = Math.min(rect.left, Math.max(8, window.innerWidth - TOOLTIP_WIDTH - 12))
    setPosition({ top: rect.bottom + 6, left })
  }, [])

  const className = [
    'method-cell',
    method.unknown ? 'method-cell-unknown' : '',
    method.verified ? 'method-cell-verified' : '',
    method.signatureOnly ? 'method-cell-signature' : '',
  ].filter(Boolean).join(' ')

  return (
    <span className="method-cell-wrap" onMouseEnter={show} onMouseLeave={() => setPosition(null)}>
      <button
        ref={anchor}
        type="button"
        className={className}
        title={method.signature || method.selector || method.primary}
        onFocus={show}
        onBlur={() => setPosition(null)}
        onClick={onOpenDetail}
        disabled={!onOpenDetail}
      >
        <b>{method.primary}</b>
        {method.secondary && <small className={method.unknown ? 'method-unknown-tag' : 'mono'}>{method.secondary}</small>}
      </button>
      {position && <MethodTooltip method={method} style={{ top: position.top, left: position.left }} />}
    </span>
  )
}
