import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api/client'

export type TokenFilterOption = {
  value: string
  symbol: string
  name: string
  address: string
  logo?: string
  count: number
  status?: string
  confidence?: string
  verified?: boolean
  decimals?: number | null
}

type Props = {
  value: string
  chain: string
  options: TokenFilterOption[]
  onConfirm: (value: string) => void
}

const HISTORY_KEY = 'blockchain-platform-token-filter-history'

function compactAddress(address: string) {
  return address.length > 14 ? `${address.slice(0, 7)}…${address.slice(-4)}` : address
}

function TokenIcon({ token }: { token: TokenFilterOption }) {
  const [failed, setFailed] = useState(false)
  if (token.logo && !failed) return <img className="token-filter-icon" src={token.logo} alt="" onError={() => setFailed(true)} />
  const hue = [...(token.symbol || token.address)].reduce((sum, char) => sum + char.charCodeAt(0), 0) % 360
  return <span className="token-filter-icon token-filter-fallback" style={{ background: `hsl(${hue} 68% 47%)` }}>{(token.symbol || '?').slice(0, 1)}</span>
}

export default function TokenFilterDropdown({ chain, value, options, onConfirm }: Props) {
  const rootRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [draft, setDraft] = useState<string[]>(value ? value.split(',').filter(Boolean) : [])
  const [history, setHistory] = useState<string[]>([])
  const [remoteOptions, setRemoteOptions] = useState<TokenFilterOption[]>([])
  const [remoteLoading, setRemoteLoading] = useState(false)

  useEffect(() => setDraft(value ? value.split(',').filter(Boolean) : []), [value])
  useEffect(() => {
    try { setHistory(JSON.parse(localStorage.getItem(HISTORY_KEY) ?? '[]')) } catch { setHistory([]) }
  }, [])
  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => { if (!rootRef.current?.contains(event.target as Node)) setOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [open])

  useEffect(() => {
    if (!open) return
    let active = true
    const timer = window.setTimeout(async () => {
      setRemoteLoading(true)
      try {
        const result = await api.tokenSearch(chain, search.trim(), 40)
        if (!active) return
        setRemoteOptions(result.tokens.map((token) => ({
          value: token.contract_address, address: token.contract_address, symbol: token.symbol || '未知代币',
          name: token.name || '', count: 0, logo: token.verified ? token.logo_url : '', status: token.metadata_status,
          confidence: token.confidence, verified: token.verified, decimals: token.decimals,
        })))
      } catch { if (active) setRemoteOptions([]) } finally { if (active) setRemoteLoading(false) }
    }, search ? 220 : 0)
    return () => { active = false; window.clearTimeout(timer) }
  }, [chain, open, search])

  const mergedOptions = useMemo(() => {
    const merged = new Map<string, TokenFilterOption>()
    for (const option of [...options, ...remoteOptions]) merged.set(option.value, { ...merged.get(option.value), ...option })
    return [...merged.values()]
  }, [options, remoteOptions])
  const byValue = useMemo(() => new Map(mergedOptions.map((option) => [option.value, option])), [mergedOptions])
  const visible = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) {
      const symbols = new Set<string>()
      const diverse = mergedOptions.filter((option) => {
        const symbol = option.symbol.toLowerCase()
        if (symbols.has(symbol)) return false
        symbols.add(symbol)
        return true
      })
      return [...diverse, ...mergedOptions.filter((option) => !diverse.includes(option))].slice(0, 20)
    }
    return mergedOptions.filter((option) => [option.symbol, option.name, option.address].some((part) => part.toLowerCase().includes(query))).slice(0, 30)
  }, [mergedOptions, search])
  const selectedOptions = draft.map((item) => byValue.get(item) ?? { value: item, symbol: compactAddress(item), name: '', address: item, count: 0 })
  const applied = value ? value.split(',').filter(Boolean) : []
  const buttonLabel = applied.length === 0 ? '全部' : applied.length === 1 ? (byValue.get(applied[0])?.symbol || compactAddress(applied[0])) : `已选 ${applied.length} 项`

  function toggle(item: string) {
    setDraft((current) => current.includes(item) ? current.filter((value) => value !== item) : [...current, item])
  }

  function confirm() {
    const nextHistory = [...new Set([...draft, ...history])].slice(0, 6)
    setHistory(nextHistory)
    localStorage.setItem(HISTORY_KEY, JSON.stringify(nextHistory))
    onConfirm(draft.join(','))
    setOpen(false)
    setSearch('')
  }

  return (
    <div className="token-filter" ref={rootRef}>
      <button className={`compact-pill token-filter-trigger ${open ? 'open' : ''}`} aria-label="代币筛选" aria-expanded={open} onClick={() => setOpen((current) => !current)}>
        <span>代币: <b>{buttonLabel}</b></span><i>{open ? '⌃' : '⌄'}</i>
      </button>
      {open && <div className="token-filter-popover" role="dialog" aria-label="选择代币">
        <div className="token-filter-body">
          <label className="token-filter-search"><span>⌕</span><input autoFocus aria-label="搜索代币" placeholder="搜索币种全称 / 简称 / 合约地址" value={search} onChange={(event) => setSearch(event.target.value)} /></label>
          {selectedOptions.length > 0 && <div className="token-filter-selected">{selectedOptions.map((token) => <button key={token.value} onClick={() => toggle(token.value)}><TokenIcon token={token} />{token.symbol}<span>×</span></button>)}</div>}
          {!search && history.length > 0 && <div className="token-filter-section"><label>历史搜索</label><div className="token-history">{history.map((item) => <button key={item} onClick={() => { if (!draft.includes(item)) setDraft((current) => [...current, item]) }}>{byValue.get(item)?.symbol || compactAddress(item)}</button>)}</div></div>}
          <div className="token-filter-section token-list-section">
            <label>{search ? '搜索结果' : '热门代币'}</label>
            <div className="token-filter-list">
              {visible.map((token) => <label className="token-filter-option" key={token.value}>
                <TokenIcon token={token} />
                <span className="token-filter-names"><b>{token.symbol || '未知代币'}{token.status === 'conflict' && <em>冲突</em>}</b><small>{token.name || (token.status === 'partial' ? '部分收录' : '名称未收录')}</small></span>
                <span className="token-filter-address" title={token.address}>{compactAddress(token.address)}</span>
                <input aria-label={`${token.symbol || token.address} ${token.address}`} type="checkbox" checked={draft.includes(token.value)} onChange={() => toggle(token.value)} />
              </label>)}
              {visible.length === 0 && <div className="token-filter-empty">{remoteLoading ? '正在搜索代币…' : search ? '没有匹配的代币' : '暂无代币元数据'}</div>}
            </div>
          </div>
        </div>
        <div className="token-filter-actions"><button onClick={() => setDraft([])}>重置</button><button className="confirm" onClick={confirm}>确认</button></div>
      </div>}
    </div>
  )
}
