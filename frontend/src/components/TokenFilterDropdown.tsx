import { useEffect, useMemo, useRef, useState } from 'react'

export type TokenFilterOption = {
  value: string
  symbol: string
  name: string
  address: string
  logo?: string
  count: number
}

type Props = {
  value: string
  options: TokenFilterOption[]
  onConfirm: (value: string) => void
}

const HISTORY_KEY = 'blockchain-platform-token-filter-history'

function compactAddress(address: string) {
  return address.length > 14 ? `${address.slice(0, 7)}…${address.slice(-4)}` : address
}

function TokenIcon({ token }: { token: TokenFilterOption }) {
  if (token.logo) return <img className="token-filter-icon" src={token.logo} alt="" />
  const hue = [...(token.symbol || token.address)].reduce((sum, char) => sum + char.charCodeAt(0), 0) % 360
  return <span className="token-filter-icon token-filter-fallback" style={{ background: `hsl(${hue} 68% 47%)` }}>{(token.symbol || '?').slice(0, 1)}</span>
}

export default function TokenFilterDropdown({ value, options, onConfirm }: Props) {
  const rootRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [draft, setDraft] = useState<string[]>(value ? value.split(',').filter(Boolean) : [])
  const [history, setHistory] = useState<string[]>([])

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

  const byValue = useMemo(() => new Map(options.map((option) => [option.value, option])), [options])
  const visible = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) {
      const symbols = new Set<string>()
      const diverse = options.filter((option) => {
        const symbol = option.symbol.toLowerCase()
        if (symbols.has(symbol)) return false
        symbols.add(symbol)
        return true
      })
      return [...diverse, ...options.filter((option) => !diverse.includes(option))].slice(0, 20)
    }
    return options.filter((option) => [option.symbol, option.name, option.address].some((part) => part.toLowerCase().includes(query))).slice(0, 30)
  }, [options, search])
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
                <span className="token-filter-names"><b>{token.symbol || '未知代币'}</b><small>{token.name || '—'}</small></span>
                <span className="token-filter-address" title={token.address}>{compactAddress(token.address)}</span>
                <input aria-label={`${token.symbol || token.address} ${token.address}`} type="checkbox" checked={draft.includes(token.value)} onChange={() => toggle(token.value)} />
              </label>)}
              {visible.length === 0 && <div className="token-filter-empty">{search ? '没有匹配的代币' : '正在加载代币…'}</div>}
            </div>
          </div>
        </div>
        <div className="token-filter-actions"><button onClick={() => setDraft([])}>重置</button><button className="confirm" onClick={confirm}>确认</button></div>
      </div>}
    </div>
  )
}
