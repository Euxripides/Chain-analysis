import type { CSSProperties } from 'react'
import type { MethodDisplay } from './method-display'
import { methodConfidenceLabel, methodSourceLabel, methodTypeLabel } from './method-display'

// MethodTooltip Hover 详情（方案 §27）。
//
// public_signature / local_signature 必须显示 "Signature Match / Not Contract Verified"，
// 避免用户把签名库匹配误当成合约已验证。
// 使用 fixed 定位：数据表格外层是 overflow 容器，absolute 定位会被裁切。
export default function MethodTooltip({ method, style }: { method: MethodDisplay; style?: CSSProperties }) {
  return (
    <div className="method-tooltip" role="tooltip" style={style}>
      <dl>
        <dt>Method</dt>
        <dd>{method.signature || method.name || (method.unknown ? '未解析' : methodTypeLabel(method.methodType))}</dd>
        <dt>Selector</dt>
        <dd className="mono">{method.selector || '—'}</dd>
        <dt>Source</dt>
        <dd>{methodSourceLabel(method.source)}</dd>
        <dt>Confidence</dt>
        <dd>{methodConfidenceLabel(method.confidence)}</dd>
        <dt>Verified</dt>
        <dd>{method.verified ? 'Yes' : 'No'}</dd>
      </dl>
      {method.signatureOnly && (
        <p className="method-tooltip-warning">
          Signature Match · Not Contract Verified
          <small>签名库匹配，未经合约 ABI 验证</small>
        </p>
      )}
      {method.unknown && (
        <p className="method-tooltip-warning">
          Unknown Selector
          <small>后台正在异步补全，可在详情中尝试深度解析</small>
        </p>
      )}
    </div>
  )
}
