// Method 展示规则（方案 §26/§27/§43）。
//
// 硬约束：
//   - Method 列不能留空（§26）；
//   - Unknown 必须原样显示 Selector 且明确标注 Unknown，不得伪装成已解析（§62-19/§62-20）；
//   - 来源为签名库时必须提示 "Signature Match / Not Contract Verified"（§27）。

export interface MethodRow {
  method_id?: unknown
  method_name?: unknown
  method_signature?: unknown
  method_source?: unknown
  method_verified?: unknown
  method_confidence?: unknown
  method_type?: unknown
  activity_type?: unknown
}

export const METHOD_SOURCE_LABEL: Record<string, string> = {
  contract_abi: '合约 ABI',
  manual_override: '人工覆盖',
  ankr: 'Provider 深度解码',
  local_signature: '本地签名库',
  public_signature: '公共签名库',
  heuristic: '结构判定',
  unknown: '未解析',
}

export const METHOD_CONFIDENCE_LABEL: Record<string, string> = {
  verified: '已验证',
  high: '高',
  medium: '中',
  low: '低',
  unknown: '未知',
}

export const METHOD_TYPE_LABEL: Record<string, string> = {
  contract_call: '合约调用',
  native_transfer: '原生转账',
  contract_creation: '合约创建',
  empty_call: '空调用',
	invalid_input: '无效调用数据',
}

export function methodSourceLabel(source: string): string {
  return METHOD_SOURCE_LABEL[source] ?? source ?? '未解析'
}

export function methodConfidenceLabel(confidence: string): string {
  return METHOD_CONFIDENCE_LABEL[confidence] ?? confidence ?? '未知'
}

export function methodTypeLabel(methodType: string): string {
  return METHOD_TYPE_LABEL[methodType] ?? methodType
}

function text(value: unknown): string {
  return value === null || value === undefined ? '' : String(value)
}

function titleCase(name: string): string {
  if (!name) return name
  return name.charAt(0).toUpperCase() + name.slice(1)
}

export interface MethodDisplay {
  primary: string
  secondary: string
  selector: string
  name: string
  signature: string
  source: string
  confidence: string
  methodType: string
  verified: boolean
  unknown: boolean
  /** 签名匹配但并非合约验证：UI 必须提示，避免误导用户（§27） */
  signatureOnly: boolean
}

// describeMethod 由一行交易数据推导 Method 展示信息。
export function describeMethod(row: MethodRow): MethodDisplay {
  const selector = text(row.method_id)
  const name = text(row.method_name)
  const signature = text(row.method_signature)
  const source = text(row.method_source) || 'unknown'
  const methodType = text(row.method_type)
  const confidence = text(row.method_confidence) || 'unknown'
  const rawVerified = row.method_verified
  const verified = rawVerified === true || rawVerified === 1 || rawVerified === '1'

  if (name) {
    return {
      primary: titleCase(name),
      // 有 selector 显示 selector；结构化分类（原生转账等）显示类型
      secondary: selector || methodTypeLabel(methodType),
      selector, name, signature, source, confidence, methodType, verified,
      unknown: false,
      signatureOnly: source === 'local_signature' || source === 'public_signature',
    }
  }
  if (selector) {
    // 未解析：Selector 在上，Unknown 在下（§26）
    return {
      primary: selector, secondary: 'Unknown', selector, name: '', signature,
      source, confidence, methodType, verified: false, unknown: true, signatureOnly: false,
    }
  }
  // 没有 selector 也没有名称：用结构分类兜底，绝不留空
  const fallback = methodType ? methodTypeLabel(methodType) : text(row.activity_type) === 'transfer' ? 'Transfer' : '—'
  return {
    primary: fallback, secondary: '', selector: '', name: '', signature: '',
    source, confidence, methodType, verified: false, unknown: !methodType, signatureOnly: false,
  }
}
