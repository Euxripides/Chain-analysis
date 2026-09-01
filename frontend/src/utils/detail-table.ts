import { fieldLabel } from './field-labels'
import { orderedFields } from './field-order'

export type DetailFilters = {
  fromTime: string
  toTime: string
  minAmount: string
  maxAmount: string
  token: string
  fromAddress: string
  toAddress: string
  /** Method 统一搜索：名称 / 签名 / Selector / unknown（§28） */
  method: string
}

export const EMPTY_DETAIL_FILTERS: DetailFilters = {
  fromTime: '', toTime: '', minAmount: '', maxAmount: '', token: '', fromAddress: '', toAddress: '', method: '',
}

export function filterPayload(filters: DetailFilters) {
  const fromTime = filters.fromTime
    ? new Date(filters.fromTime.length === 10 ? `${filters.fromTime}T00:00:00` : filters.fromTime).toISOString()
    : ''
  const toTime = filters.toTime
    ? new Date(filters.toTime.length === 10 ? `${filters.toTime}T23:59:59.999` : filters.toTime).toISOString()
    : ''
  return {
    ...(fromTime && { from_time: fromTime }),
    ...(toTime && { to_time: toTime }),
    ...(filters.minAmount && { min_amount: filters.minAmount }),
    ...(filters.maxAmount && { max_amount: filters.maxAmount }),
    ...(filters.token.trim() && { token: filters.token.trim() }),
    ...(filters.fromAddress.trim() && { from_address: filters.fromAddress.trim().toLowerCase() }),
    ...(filters.toAddress.trim() && { to_address: filters.toAddress.trim().toLowerCase() }),
    ...(filters.method.trim() && { method: filters.method.trim() }),
  }
}

export function hasActiveFilters(filters: DetailFilters) {
  return Object.values(filters).some((value) => value.trim() !== '')
}

export function rowKey(row: Record<string, unknown>) {
  return [row.tx_hash, row.log_index, row.token_address, row.contract_address, row.token_id, row.activity_type, row.counterparty]
    .map((value) => String(value ?? '')).join('|')
}

type TokenStat = { key: string; symbol: string; address: string; count: number; total: string }

function sumDecimals(values: string[]): string {
  if (values.length === 0) return '0'
  const parsed = values.map((value) => {
    const clean = value.replaceAll(',', '').trim()
    const negative = clean.startsWith('-')
    const [whole = '0', fraction = ''] = clean.replace(/^[-+]/, '').split('.')
    const digits = BigInt((whole + fraction).replace(/^0+(?=\d)/, '') || '0') * (negative ? -1n : 1n)
    return { digits, scale: fraction.length }
  })
  const scale = Math.max(...parsed.map((part) => part.scale))
  const total = parsed.reduce((sum, part) => sum + part.digits * (10n ** BigInt(scale - part.scale)), 0n)
  const negative = total < 0n
  const digits = (negative ? -total : total).toString().padStart(scale + 1, '0')
  if (scale === 0) return `${negative ? '-' : ''}${digits}`
  const whole = digits.slice(0, -scale) || '0'
  const fraction = digits.slice(-scale).replace(/0+$/, '')
  return `${negative ? '-' : ''}${whole}${fraction ? `.${fraction}` : ''}`
}

export function calculateDetailStats(rows: Record<string, unknown>[]) {
  const groups = new Map<string, { symbol: string; address: string; amounts: string[]; count: number }>()
  let incoming = 0
  let outgoing = 0
  for (const row of rows) {
    if (row.direction === 'in') incoming += 1
    if (row.direction === 'out') outgoing += 1
    const address = String(row.token_address ?? row.contract_address ?? '')
    const symbol = String(row.token_symbol ?? row.token_name ?? '')
    if (!address && !symbol && row.amount === undefined) continue
    const key = address || symbol || 'native'
    const group = groups.get(key) ?? { symbol: symbol || '原生币', address, amounts: [], count: 0 }
    group.count += 1
    if (row.amount !== null && row.amount !== undefined && String(row.amount) !== '') group.amounts.push(String(row.amount))
    groups.set(key, group)
  }
  const tokens: TokenStat[] = [...groups.entries()].map(([key, group]) => ({
    key, symbol: group.symbol, address: group.address, count: group.count, total: sumDecimals(group.amounts),
  })).sort((a, b) => b.count - a.count)
  return { count: rows.length, incoming, outgoing, tokenCount: tokens.length, tokens }
}

function exportColumns(rows: Record<string, unknown>[], dataset: string) {
  return orderedFields(rows[0], dataset, {
    appendRemaining: true,
    exclude: ['matched_addresses', 'case_names', 'tag_names', 'token_logo'],
  })
}

function downloadBlob(content: BlobPart, mime: string, filename: string) {
  const url = URL.createObjectURL(new Blob([content], { type: mime }))
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}

export async function exportDetailRows(rows: Record<string, unknown>[], dataset: string, format: 'csv' | 'xlsx' | 'json', basename: string) {
  const columns = exportColumns(rows, dataset)
  const records = rows.map((row) => Object.fromEntries(columns.map((column) => [fieldLabel(column), row[column] ?? ''])))
  if (format === 'json') {
    downloadBlob(JSON.stringify(records, null, 2), 'application/json;charset=utf-8', `${basename}.json`)
    return
  }
  if (format === 'xlsx') {
    const XLSX = await import('xlsx')
    const sheet = XLSX.utils.json_to_sheet(records)
    const workbook = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(workbook, sheet, '数据')
    XLSX.writeFile(workbook, `${basename}.xlsx`)
    return
  }
  const escape = (value: unknown) => `"${String(value ?? '').replaceAll('"', '""')}"`
  const csv = [columns.map((column) => escape(fieldLabel(column))).join(','),
    ...rows.map((row) => columns.map((column) => escape(row[column])).join(','))].join('\r\n')
  downloadBlob(`\uFEFF${csv}`, 'text/csv;charset=utf-8', `${basename}.csv`)
}
