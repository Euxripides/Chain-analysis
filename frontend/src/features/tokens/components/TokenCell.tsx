import { useCallback, useRef, useState } from 'react'
import { cleanTokenSymbol } from '../../../utils/value-format'
import type { TokenView } from '../types'

function compact(value: string) { return value.length > 18 ? `${value.slice(0, 9)}…${value.slice(-6)}` : value }

export function TokenLogo({ token }: { token: TokenView }) {
  if (token.logo) return <img className="flow-token-icon" src={token.logo} alt="" />
  const seed = token.contract || token.symbol
  const hue = [...seed].reduce((sum, char) => sum + char.charCodeAt(0), 0) % 360
  return <span className="flow-token-icon flow-token-fallback" style={{ background: `hsl(${hue} 58% 46%)` }}>{cleanTokenSymbol(token.symbol).slice(0, 1)}</span>
}

export default function TokenCell({ token }: { token: TokenView }) {
  const anchor = useRef<HTMLSpanElement>(null)
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null)
  const show = useCallback(() => { const rect = anchor.current?.getBoundingClientRect(); if (!rect) return; setPosition({ top: Math.min(rect.bottom + 7, window.innerHeight - 235), left: Math.min(Math.max(8, rect.left), window.innerWidth - 322) }) }, [])
  const symbol = cleanTokenSymbol(token.symbol || '未知代币')
  const status = token.status || 'unknown'
  const statusLabel = token.verified ? '已验证' : status === 'conflict' ? '元数据冲突' : status === 'complete' ? '未验证' : status === 'partial' ? '部分收录' : '未收录'
  return <span ref={anchor} className="token-cell" data-status={status} tabIndex={0} onMouseEnter={show} onMouseLeave={() => setPosition(null)} onFocus={show} onBlur={() => setPosition(null)}>
    <TokenLogo token={{ ...token, symbol }} />
    <span className="token-cell-copy"><span className="token-cell-title"><b>{symbol}</b><em className={`metadata-status metadata-status-${status}`}>{statusLabel}</em></span><small className="token-contract" title={token.contract}>{token.contract ? compact(token.contract) : token.name || '原生币'}</small></span>
    {position && <span className="token-metadata-tooltip" role="tooltip" style={position}><strong>{token.name || '名称未收录'}</strong><b>{symbol}</b><dl><dt>合约</dt><dd>{token.contract || '原生币'}</dd><dt>Decimals</dt><dd>{token.decimals ?? '未知'}</dd><dt>来源</dt><dd>{token.source || '未解析'}</dd><dt>置信度</dt><dd>{token.confidence || 'unknown'}</dd></dl></span>}
  </span>
}
