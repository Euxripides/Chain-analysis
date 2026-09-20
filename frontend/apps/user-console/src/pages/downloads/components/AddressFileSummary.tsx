import React from "react";

/**
 * 文件解析数据（《新建数据下载页面重构规范》§8.4）
 *
 * 导入文件后把解析器**真实产出**的文件级计数留在这里：扫描行数、空白行、
 * 超出单次上限被截断的行数、识别到的可选列、本地链分布。
 *
 * 为什么不是弹窗：规范 §8.3/§8.4 明确要求本地解析不再用全局 Dialog 反复打断；
 * 而这些是文件的**数据事实**（不是提示文案），因此以数据行的形式贴着文件面板展示。
 * 服务端裁决（接受/拒绝/案件标签）仍然只走「服务端地址校验结果」弹窗。
 */

export interface FileParseFacts {
  name: string;
  kind: string;
  rows: number;
  blank: number;
  truncated: number;
  usedHeaderRow: boolean;
  columns: string[];
  perChain: Record<string, number>;
  metaRows: number;
}

export function AddressFileSummary({ facts }: { facts: FileParseFacts }) {
  /* 待服务端校验的地址数 = 各链计数之和；没有链列时退化为"扫描行数 - 空白行"。 */
  const chainTotal = Object.values(facts.perChain).reduce((sum, count) => sum + count, 0);
  const addressCount = chainTotal > 0 ? chainTotal : Math.max(0, facts.rows - facts.blank);

  const entries: Array<[string, React.ReactNode, string | undefined, string | undefined]> = [
    ["文件", facts.name, undefined, undefined],
    ["类型", facts.kind, undefined, undefined],
    /* 稳定锚点（§15.2）：值本身就是纯数字，测试断言 toHaveText("1")。 */
    ["扫描行数", facts.rows, undefined, "local-parse-row-count"],
    ["待校验地址", addressCount, undefined, "local-parse-address-count"],
    ["空白行", facts.blank, undefined, undefined],
    ["表头", facts.usedHeaderRow ? "address 表头（列映射）" : "无表头（整表当地址清单）", facts.usedHeaderRow ? "true" : "false", "local-parse-address-header"],
    ["识别列", facts.columns.length > 0 ? facts.columns.join("、") : "无", undefined, undefined],
    ["带案件/标签的行", facts.metaRows, undefined, undefined],
    ["链分布", Object.entries(facts.perChain).map(([key, count]) => `${key} ${count}`).join(" · ") || "—", undefined, undefined],
  ];
  if (facts.truncated > 0) entries.push(["超出单次上限", `${facts.truncated} 行未导入`, undefined, undefined]);

  return (
    <div data-testid="local-parse-summary">
      <dl className="cs-filesummary" data-testid="file-parse-summary">
        {entries.map(([label, value, detected, testId]) => (
          <div className="cs-filesummary__row" key={label}>
            <dt>{label}</dt>
            <dd
              className={label === "超出单次上限" ? "cs-filesummary__warn" : undefined}
              data-testid={testId}
              data-detected={detected}
            >
              {value}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

export default AddressFileSummary;