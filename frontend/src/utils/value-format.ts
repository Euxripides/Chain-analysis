export function formatDecimal(value: unknown, maxFraction = 8): string {
  const source = String(value ?? '').trim()
  if (!source) return '—'
  if (!/^-?\d+(\.\d+)?$/.test(source)) return source

  const negative = source.startsWith('-')
  const unsigned = negative ? source.slice(1) : source
  let [integer, fraction = ''] = unsigned.split('.')
  integer = integer.replace(/^0+(?=\d)/, '') || '0'
  fraction = fraction.replace(/0+$/, '')

  if (integer === '0' && fraction) {
    const first = fraction.search(/[1-9]/)
    if (first >= 6) {
      const significant = fraction.slice(first, first + 6).replace(/0+$/, '')
      const mantissa = significant.length > 1 ? significant[0] + '.' + significant.slice(1) : significant
      return `${negative ? '-' : ''}${mantissa}e-${first + 1}`
    }
  }

  const grouped = integer.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  const visibleFraction = fraction.slice(0, maxFraction).replace(/0+$/, '')
  return `${negative ? '-' : ''}${grouped}${visibleFraction ? '.' + visibleFraction : ''}`
}

export function formatDateTime(value: unknown): string {
  const date = new Date(String(value ?? ''))
  if (Number.isNaN(date.getTime())) return String(value ?? '')
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

export function cleanTokenSymbol(value: unknown): string {
  return String(value ?? '').replace(/[\s\u2000-\u200f\u202f\u205f\u3000]+/gu, '') || '—'
}
