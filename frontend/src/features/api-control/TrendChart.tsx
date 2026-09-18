import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { LineChart, BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { SVGRenderer } from 'echarts/renderers'

// R-3 图表修复（1/2）：**改用 SVG 渲染器**。
// 原来用 CanvasRenderer，坐标轴刻度是画进位图的像素，既不能被无障碍技术读取，
// 也无法用 DOM bounds 判定"是否被容器裁掉"——只能靠肉眼。换成 SVG 后每个刻度都是
// 真实 <text> 元素，验收可以对 getBoundingClientRect() 做几何断言。
echarts.use([LineChart, BarChart, GridComponent, TooltipComponent, LegendComponent, SVGRenderer])

const compactNumber = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 })

export default function TrendChart({ data, onSelect }: { data: { time: string; requests: number; errors: number; p95_ms: number }[]; onSelect?: (time: string) => void }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!ref.current) return
    const chart = echarts.init(ref.current, undefined, { renderer: 'svg' })
    chart.setOption({
      animationDuration: 380,
      color: ['#2563eb', '#dc2626', '#d97706'],
      tooltip: { trigger: 'axis', backgroundColor: '#0f172a', borderWidth: 0, textStyle: { color: '#fff' } },
      legend: { top: 0, right: 4, itemWidth: 12, textStyle: { color: '#64748b' } },
      // R-3 图表修复（2/2）：原来 grid.left 固定 48px，而请求量刻度是 "15,000" 这类
      // 四位数以上文本，宽度超过 48px 就被容器裁掉（真实截图里只剩 "15," "12," "9,"）。
      // 这里改为 **containLabel: true**：由 ECharts 按实际刻度文本宽度预留空间，
      // 不是靠截断字符串凑宽度；left/right 退化为"标签之外的固定留白"。
      grid: { top: 40, left: 10, right: 16, bottom: 26, containLabel: true },
      xAxis: {
        type: 'category',
        boundaryGap: false,
        data: data.map((d) => new Date(d.time).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit' })),
        axisLine: { lineStyle: { color: '#e6eaf0' } },
        axisLabel: { color: '#64748b', hideOverlap: true },
      },
      yAxis: [
        {
          type: 'value',
          axisLabel: { color: '#64748b', formatter: (value: number) => compactNumber.format(value) },
          splitLine: { lineStyle: { color: '#eef1f5' } },
        },
        {
          type: 'value',
          axisLabel: { color: '#64748b', formatter: (value: number) => `${value}ms` },
          splitLine: { show: false },
        },
      ],
      series: [
        { name: '请求', type: 'line', data: data.map((d) => d.requests), smooth: .25, symbol: 'none', lineStyle: { width: 2 }, areaStyle: { color: 'rgba(37,99,235,.08)' } },
        { name: '错误', type: 'bar', data: data.map((d) => d.errors), barMaxWidth: 8 },
        { name: 'P95', type: 'line', yAxisIndex: 1, data: data.map((d) => d.p95_ms), symbol: 'none', lineStyle: { type: 'dashed', width: 1.5 } },
      ],
    })
    chart.on('click', (p) => onSelect?.(data[p.dataIndex]?.time))
    const resize = () => chart.resize()
    window.addEventListener('resize', resize)
    return () => { window.removeEventListener('resize', resize); chart.dispose() }
  }, [data, onSelect])
  return <div ref={ref} className="ac-chart" role="img" aria-label="API 请求、错误和 P95 延迟趋势图" data-chart-renderer="svg" />
}
