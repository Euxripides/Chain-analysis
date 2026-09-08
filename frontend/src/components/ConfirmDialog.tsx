import { useEffect, useRef, type ReactNode } from 'react'

export default function ConfirmDialog({ eyebrow, title, children, confirmLabel, onConfirm, onCancel }: { eyebrow: string; title: string; children: ReactNode; confirmLabel: string; onConfirm: () => void; onCancel: () => void }) {
  const dialogRef = useRef<HTMLElement>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    cancelRef.current?.focus()
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onCancel()
      if (event.key !== 'Tab') return
      const buttons = dialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled)')
      if (!buttons?.length) return
      const first = buttons[0]
      const last = buttons[buttons.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', handleKey)
    return () => window.removeEventListener('keydown', handleKey)
  }, [onCancel])

  return <div className="notice-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onCancel()}>
    <section ref={dialogRef} className="notice-dialog update-preview-dialog" role="dialog" aria-modal="true" aria-labelledby="confirm-dialog-title">
      <header><div><small>{eyebrow}</small><h2 id="confirm-dialog-title">{title}</h2></div></header>
      <div className="confirm-dialog-content">{children}</div>
      <footer><button ref={cancelRef} className="ghost" onClick={onCancel}>取消</button><button className="primary" onClick={onConfirm}>{confirmLabel}</button></footer>
    </section>
  </div>
}
