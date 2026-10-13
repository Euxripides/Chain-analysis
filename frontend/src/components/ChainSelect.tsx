import { useEffect, useId, useRef, useState } from 'react'
import { CHAINS } from '../types'
import { getChainLogo } from '../../packages/chain-assets'

type Props = {
  value: string
  onChange: (value: string) => void
  ariaLabel?: string
  className?: string
}

const chainNames: Record<string, string> = {
  bsc: 'BSC', eth: 'ETH', tron: 'TRON', polygon: 'POLYGON', arbitrum: 'ARBITRUM',
  base: 'BASE', avalanche: 'AVALANCHE', optimism: 'OPTIMISM',
  solana: 'SOLANA', fantom: 'FANTOM', flare: 'FLARE', gnosis: 'GNOSIS', linea: 'LINEA',
  scroll: 'SCROLL', somnia_mainnet: 'SOMNIA', story_mainnet: 'STORY', taiko: 'TAIKO',
  telos: 'TELOS', xlayer: 'X LAYER',
}

export function ChainLogo({ chain }: { chain: string }) {
  const src = getChainLogo(chain)
  if (src) return <img className="chain-logo" src={src} width={22} height={22} alt="" aria-hidden="true" decoding="async" />
  return <svg className="chain-logo" viewBox="0 0 32 32" aria-hidden="true"><circle cx="16" cy="16" r="15" fill="currentColor" fillOpacity=".12"/><text x="16" y="21" textAnchor="middle" fontSize="14" fill="currentColor">{(chainNames[chain] || chain).slice(0, 1)}</text></svg>
}

export default function ChainSelect({ value, onChange, ariaLabel = '链', className = '' }: Props) {
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const optionRefs = useRef<(HTMLButtonElement | null)[]>([])
  const listID = useId()
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => { if (!rootRef.current?.contains(event.target as Node)) setOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setOpen(false); triggerRef.current?.focus() } }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    optionRefs.current[Math.max(0, CHAINS.indexOf(value))]?.focus()
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [open, value])

  function choose(chain: string) {
    onChange(chain)
    setOpen(false)
    triggerRef.current?.focus()
  }

  function moveFocus(index: number, direction: number) {
    optionRefs.current[(index + direction + CHAINS.length) % CHAINS.length]?.focus()
  }

  return <div className={`chain-select ${className}`} ref={rootRef}>
    <button ref={triggerRef} type="button" className="chain-select-trigger" role="combobox" aria-label={ariaLabel} aria-expanded={open} aria-controls={listID} onClick={() => setOpen((current) => !current)} onKeyDown={(event) => { if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); setOpen(true) } }}>
      <ChainLogo chain={value} /><span>{chainNames[value] || value.toUpperCase()}</span><i aria-hidden="true">⌄</i>
    </button>
    {open ? <div id={listID} className="chain-select-menu" role="listbox" aria-label={`${ariaLabel}选项`}>
      {CHAINS.map((chain, index) => <button key={chain} ref={(node) => { optionRefs.current[index] = node }} type="button" role="option" aria-selected={chain === value} className={chain === value ? 'selected' : ''} onClick={() => choose(chain)} onKeyDown={(event) => { if (event.key === 'ArrowDown') { event.preventDefault(); moveFocus(index, 1) } else if (event.key === 'ArrowUp') { event.preventDefault(); moveFocus(index, -1) } else if (event.key === 'Home') { event.preventDefault(); optionRefs.current[0]?.focus() } else if (event.key === 'End') { event.preventDefault(); optionRefs.current[CHAINS.length - 1]?.focus() } }}>
        <ChainLogo chain={chain} /><span>{chainNames[chain]}</span>{chain === value ? <b aria-hidden="true">✓</b> : null}
      </button>)}
    </div> : null}
  </div>
}
