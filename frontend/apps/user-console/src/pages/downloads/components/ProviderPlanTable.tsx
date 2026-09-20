import type { ProviderPlanItem } from "../download.types";

/**
 * Provider 计划表（开发文档 §13.1 / §15.2）
 *
 * 纯展示组件：只渲染服务端 `provider_plan` 里真实返回的字段，不做任何前端估算。
 *
 * 两个口径必须分列，不能合并成一个"请求数"：
 *   - **Data API 页数**：1 页 = 1 次 HTTP 请求，单页最多 100 条交易；
 *   - **RPC 详情调用数**：JSON-RPC 方法调用数（首期 Batch=1，1 次请求 = 1 个方法调用）。
 * 把一页 100 条交易显示成 100 次请求，是这张表首先要避免的错误。
 */

export interface ProviderPlanTableProps {
  items: ProviderPlanItem[];
  /** 表格说明（读屏用；视觉上不占位）。 */
  caption: string;
}

/** 数值列：服务端可能给 number、字符串或 null；null/空一律显示 —，不补 0。 */
function cell(value: number | string | null | undefined): string {
  if (value === null || value === undefined || value === "") return "—";
  return typeof value === "number" ? String(value) : value;
}

export function ProviderPlanTable({ items, caption }: ProviderPlanTableProps) {
  if (items.length === 0) return null;
  return (
    <div className="dl-coverage__table">
      <table>
        <caption className="visually-hidden">{caption}</caption>
        <thead>
          <tr>
            <th scope="col">Provider</th>
            <th scope="col">通道</th>
            <th scope="col">预计页数（Data API）</th>
            <th scope="col">预计 RPC 调用（详情）</th>
            <th scope="col">预计 CU</th>
            <th scope="col">预计成本（USD）</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item, index) => (
            <tr key={item.provider + "|" + item.api_channel + "|" + index}>
              <td className="mono">{item.provider || "—"}</td>
              <td className="mono">{item.api_channel || "—"}</td>
              <td className="dl-num">{cell(item.estimated_pages)}</td>
              <td className="dl-num">{cell(item.estimated_rpc_calls)}</td>
              <td className="dl-num">{cell(item.estimated_cu)}</td>
              <td className="dl-num">{cell(item.estimated_cost_usd)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export default ProviderPlanTable;
