import { useEffect, useRef } from 'react'

export default function CopyToast({ message, onClose }: { message: string; onClose: () => void }) {
  const onCloseRef = useRef(onClose)

  useEffect(() => { onCloseRef.current = onClose }, [onClose])
  useEffect(() => {
    const timer = window.setTimeout(() => onCloseRef.current(), 2000)
    return () => window.clearTimeout(timer)
  }, [])

  if (!message) return null
  const failed = message.includes('失败')
  return <div className={`copy-toast${failed ? ' copy-toast-error' : ''}`} role="status" aria-live="polite" aria-atomic="true">{message}</div>
}
