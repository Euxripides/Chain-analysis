import { useCallback, useRef, useState } from 'react'
import { cleanTokenSymbol } from '../../../utils/value-format'
import type { TokenView } from '../types'

export function TokenLogo({ token }: { token: TokenView }) {
  const [failed, setFailed] = useState(false)
  if (token.logo && !failed) return <img className="flow-token-icon" src={token.logo} alt="" width={32} height={32} onError={() => setFailed(true)} />
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
  const name = token.name || (token.contract ? '名称未收录' : '原生币')
  return <span ref={anchor} className="token-cell" data-status={status} data-contract={token.contract} tabIndex={0} onMouseEnter={show} onMouseLeave={() => setPosition(null)} onFocus={show} onBlur={() => setPosition(null)}>
    <TokenLogo token={{ ...token, symbol }} />
    <span className="token-cell-copy"><span className="token-cell-title"><b>{symbol}</b></span><small className="token-name">{name}</small></span>
    {position && <span className="token-metadata-tooltip" role="tooltip" style={position}><strong>{name}</strong><b>{symbol}</b><dl><dt>合约</dt><dd>{token.contract || '原生币'}</dd><dt>Decimals</dt><dd>{token.decimals ?? '未知'}</dd><dt>来源</dt><dd>{token.source || '暂无来源'}</dd><dt>置信度</dt><dd>{token.confidence || 'unknown'}</dd><dt>认证状态</dt><dd>{token.verified ? '可信注册表已认证' : '未认证'}</dd></dl></span>}
  </span>
}
