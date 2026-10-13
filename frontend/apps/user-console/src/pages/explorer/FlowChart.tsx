import { useEffect, useRef } from "react";
import { init, use } from "echarts/core";
import { LineChart } from "echarts/charts";
import { GridComponent, TooltipComponent, LegendComponent } from "echarts/components";
import { SVGRenderer } from "echarts/renderers";
import { formatRawAmount } from "../../sprint5/common";

use([LineChart, GridComponent, TooltipComponent, LegendComponent, SVGRenderer]);
export interface ChartPoint { bucket: string; in_raw: string; out_raw: string; decimals: number }
export default function FlowChart({ points, onSelect }: { points: ChartPoint[]; onSelect: (bucket: string) => void }) {
  const node = useRef<HTMLDivElement>(null); const callback = useRef(onSelect); callback.current = onSelect;
  useEffect(() => {
    if (!node.current) return;
    const chart = init(node.current, undefined, { renderer: "svg" });
    const display = (raw: string, decimals: number) => { const n = Number(formatRawAmount(raw, decimals).replace(/,/g, "")); return Number.isFinite(n) ? n : null; };
    chart.setOption({ animation: false, color: ["#1677ff", "#91caff"], legend: { top: 0, right: 16 }, grid: { left: 64, right: 24, top: 44, bottom: 36 },
      tooltip: { trigger: "axis", renderMode: "richText", formatter: (params: Array<{ dataIndex: number }>) => { const point = points[params[0]?.dataIndex]; return point ? `${point.bucket}\n收入  ${formatRawAmount(point.in_raw, point.decimals)}\n支出  ${formatRawAmount(point.out_raw, point.decimals)}` : ""; } },
      xAxis: { type: "category", data: points.map(p => p.bucket), axisLine: { lineStyle: { color: "#d9d9d9" } }, axisLabel: { color: "#737373", formatter: (label: string) => label.slice(5, 16).replace("T", " ") } },
      yAxis: { type: "value", axisLabel: { color: "#737373" }, splitLine: { lineStyle: { color: "#f0f0f0" } } },
      series: [{ name: "收入", type: "line", smooth: false, symbolSize: 5, data: points.map(p => display(p.in_raw, p.decimals)) }, { name: "支出", type: "line", smooth: false, symbolSize: 5, data: points.map(p => display(p.out_raw, p.decimals)) }],
    });
    chart.on("click", (event: { dataIndex?: number }) => { if (event.dataIndex !== undefined && points[event.dataIndex]) callback.current(points[event.dataIndex].bucket); });
    const resize = new ResizeObserver(() => chart.resize()); resize.observe(node.current);
    return () => { resize.disconnect(); chart.dispose(); };
  }, [points]);
  return <div ref={node} className="cs-flow-chart" role="img" aria-label="收入与支出趋势" />;
}
