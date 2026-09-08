import { useEffect, useRef } from 'react'
import { AlertTriangle, X } from 'lucide-react'

export default function NoticeDialog({ title = '操作未完成', message, onClose }: { title?: string; message: string; onClose: () => void }) {
  const closeRef = useRef<HTMLButtonElement>(null)
  const dialogRef = useRef<HTMLElement>(null)

  useEffect(() => {
    closeRef.current?.focus()
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
      if (event.key === 'Tab') {
        const focusable = dialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),textarea:not(:disabled)')
        if (!focusable?.length) return
        const first = focusable[0]
        const last = focusable[focusable.length - 1]
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
      }
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [onClose])

  if (!message) return null
  return <div className="notice-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
    <section ref={dialogRef} className="notice-dialog" role="alertdialog" aria-modal="true" aria-labelledby="notice-title" aria-describedby="notice-message">
      <header><span><AlertTriangle size={18} aria-hidden="true" /></span><div><small>系统通知</small><h2 id="notice-title">{title}</h2></div><button ref={closeRef} className="icon-button" aria-label="关闭" onClick={onClose}><X size={18} /></button></header>
      <p id="notice-message">{message}</p>
      <footer><button className="primary" onClick={onClose}>我知道了</button></footer>
    </section>
  </div>
}
