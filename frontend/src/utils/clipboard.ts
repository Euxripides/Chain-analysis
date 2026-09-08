/**
 * Copy text in both secure contexts and the HTTP deployment used by the
 * internal workbench. Clipboard API is unavailable on non-secure origins, so
 * keep a standards-compatible execCommand fallback for that environment.
 */
export async function copyText(value: string): Promise<void> {
  const text = value.trim()
  if (!text) throw new Error('没有可复制的内容')

  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch {
      // Fall through to the legacy path when permissions are denied.
    }
  }

  if (typeof document === 'undefined' || typeof document.execCommand !== 'function') {
    throw new Error('当前浏览器不支持复制，请手动选择文本')
  }

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.setAttribute('aria-hidden', 'true')
  textarea.style.position = 'fixed'
  textarea.style.top = '0'
  textarea.style.left = '-9999px'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.focus({ preventScroll: true })
  textarea.select()
  textarea.setSelectionRange(0, text.length)

  let copied = false
  try {
    copied = document.execCommand('copy')
  } finally {
    textarea.remove()
  }
  if (!copied) throw new Error('浏览器拒绝了复制操作，请手动选择文本')
}
