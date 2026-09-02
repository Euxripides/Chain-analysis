import { useEffect, useId, useRef, useState } from 'react'
import { CHAINS } from '../types'

type Props = {
  value: string
  onChange: (value: string) => void
  ariaLabel?: string
  className?: string
}

const chainNames: Record<string, string> = {
  bsc: 'BSC', eth: 'ETH', polygon: 'POLYGON', arbitrum: 'ARBITRUM',
  base: 'BASE', avalanche: 'AVALANCHE', optimism: 'OPTIMISM',
}

export function ChainLogo({ chain }: { chain: string }) {
  if (chain === 'bsc') return <svg className="chain-logo" viewBox="0 0 32 32" aria-hidden="true"><circle cx="16" cy="16" r="16" fill="#f3ba2f"/><path fill="#fff" d="m16 6 3.1 3.1-3.1 3.1-3.1-3.1L16 6Zm-7 7 3.1 3.1L9 19.2 5.9 16.1 9 13Zm14 0 3.1 3.1-3.1 3.1-3.1-3.1L23 13Zm-7 7 3.1 3.1-3.1 3.1-3.1-3.1L16 20Zm0-6.2 2.3 2.3-2.3 2.3-2.3-2.3 2.3-2.3Z"/></svg>
  if (chain === 'eth') return <svg className="chain-logo" viewBox="0 0 32 32" aria-hidden="true"><circle cx="16" cy="16" r="16" fill="#627eea"/><path fill="#fff" fillOpacity=".75" d="m16 5-6.8 11.3L16 20l6.8-3.7L16 5Z"/><path fill="#fff" d="m16 21.3-6.8-4L16 27l6.8-9.7-6.8 4Z"/></svg>
  if (chain === 'polygon') return <svg className="chain-logo" viewBox="0 0 32 32" aria-hidden="true"><circle cx="16" cy="16" r="16" fill="#8247e5"/><path d="m20.8 11.5-3.3-1.9a2.4 2.4 0 0 0-2.4 0l-3.3 1.9a2.4 2.4 0 0 0-1.2 2.1v3.8l-2.3 1.3-2.3-1.3v-2.7l2.3-1.3 1.5.9v-2.6l-.3-.2a2.4 2.4 0 0 0-2.4 0l-3.3 1.9a2.4 2.4 0 0 0-1.2 2.1v3.8c0 .9.5 1.7 1.2 2.1l3.3 1.9a2.4 2.4 0 0 0 2.4 0l3.3-1.9a2.4 2.4 0 0 0 1.2-2.1v-3.8l2.3-1.3 2.3 1.3v2.7l-2.3 1.3-1.5-.9v2.6l.3.2a2.4 2.4 0 0 0 2.4 0l3.3-1.9a2.4 2.4 0 0 0 1.2-2.1v-3.8c0-.9-.5-1.7-1.2-2.1Z" fill="#fff" transform="translate(3 0) scale(.82)"/></svg>
  if (chain === 'arbitrum') return <svg className="chain-logo" viewBox="0 0 32 32" aria-hidden="true"><path d="m16 2 12 7v14l-12 7L4 23V9L16 2Z" fill="#213147"/><path d="m13.1 24.7 2.1 1.2 7.4-15-2.6-1.5-6.9 15.3Zm-4-2.3 2.3 1.3L18.2 8l-2.6-1.5-6.5 15.9Z" fill="#28a0f0"/><path d="m17.5 26.4 2.7-1.6-3-6.8-1.5 3.1 1.8 5.3Z" fill="#fff"/></svg>
  if (chain === 'base') return <svg className="chain-logo" viewBox="0 0 32 32" aria-hidden="true"><circle cx="16" cy="16" r="16" fill="#0052ff"/><path fill="#fff" d="M16.1 7.2a8.8 8.8 0 1 0 8.6 10.6H13.4v-3.6h11.3a8.8 8.8 0 0 0-8.6-7Z"/></svg>
  if (chain === 'avalanche') return <svg className="chain-logo" viewBox="0 0 32 32" aria-hidden="true"><circle cx="16" cy="16" r="16" fill="#e84142"/><path fill="#fff" d="M15.1 7.2c.6-1 1.5-1 2.1 0l7 12.5c.6 1 .1 1.8-1 1.8h-4.1c-1.1 0-1.7-.8-1.2-1.8l1.2-2.2-3-5.4-5.3 9.4H6.6c-1.1 0-1.5-.8-1-1.8l9.5-12.5Zm7.6 8.1c.5-.9 1.4-.9 1.9 0l1.7 3.1c.5.9.1 1.6-.9 1.6H22c-1 0-1.4-.7-.9-1.6l1.6-3.1Z"/></svg>
  return <svg className="chain-logo" viewBox="0 0 32 32" aria-hidden="true"><circle cx="16" cy="16" r="16" fill="#ff0420"/><path fill="#fff" d="M7 11.2c0-2.5 2-4.5 4.5-4.5h5c2.5 0 4.5 2 4.5 4.5v3.6c0 2.5-2 4.5-4.5 4.5h-5A4.5 4.5 0 0 1 7 14.8v-3.6Zm4.4-.7a1.8 1.8 0 0 0-1.8 1.8v1.4c0 1 .8 1.8 1.8 1.8h5.2c1 0 1.8-.8 1.8-1.8v-1.4c0-1-.8-1.8-1.8-1.8h-5.2ZM21.8 7h3.1v12.2h-3.1V7Z"/></svg>
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
