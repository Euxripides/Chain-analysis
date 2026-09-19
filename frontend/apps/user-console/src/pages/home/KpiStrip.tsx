import { MetricCard, type MetricTone } from "@chain/ui";
import type { DashboardCard } from "@chain/api-client";

/**
 * KPI 条（用户端重构规范 V4 §32.3）
 *
 * "不用 4 个超宽空卡，改为 5–6 个紧凑 KPI"。
 *
 * 实现方式是**只改布局不改数据**：用自适应网格让列数随视口变化
 * （窄屏 2 列 / 中屏 3–4 列 / 宽屏最多 6 列），因此不会再出现
 * "4 张卡铺满 1920px、每张只有一个数字"的稀疏观感。
 *
 * 复用 `@chain/ui` 的 `MetricCard` 而不是另造一个：
 *   - 它的"口径说明"按钮已经把说明内容收进 `useDialog()` 弹窗，
 *     符合仓库"提示只能走弹窗、不得内嵌页面"的硬约束；
 *   - 数值原样渲染，从不把金额转成 JS number（§63B）。
 *
 * **关于 sparkline**：规范 §32.3 提到 KPI "支持 sparkline"，但当前
 * `GET /api/v2/dashboard` 的 `cards[]` 只有 `{ key, label, value, unit }`，
 * 没有时间序列字段。规范 §0.4 / §64 #1·#25 要求以源码为准、禁止发明数据，
 * 因此这里不画趋势线。要加 sparkline 必须先由后端提供序列——那是接口变更，
 * 不属于 UI 重构；本条已记录为待产品确认项，而不是用假曲线糊过去。
 */

export interface KpiStripProps {
  cards: DashboardCard[];
  toneFor: (key: string) => MetricTone;
  /** 每个指标的统计范围说明（与重构前同一段文案，只是从页面正文移进弹窗）。 */
  noteFor: (card: DashboardCard) => React.ReactNode;
}

export function KpiStrip({ cards, toneFor, noteFor }: KpiStripProps) {
  if (cards.length === 0) return null;
  return (
    <section className="cs-kpistrip cs-stagger" aria-label="关键指标">
      {cards.map((card) => (
        <MetricCard
          key={card.key}
          label={card.label}
          value={card.value}
          unit={card.unit}
          tone={toneFor(card.key)}
          source={card.key}
          sourceNote={noteFor(card)}
        />
      ))}
    </section>
  );
}
