import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { LineChart, BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([LineChart, BarChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

export default function TrendChart({ data, onSelect }: { data: { time: string; requests: number; errors: number; p95_ms: number }[]; onSelect?: (time: string) => void }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!ref.current) return
    const chart = echarts.init(ref.current)
    chart.setOption({
      animationDuration: 380,
      color: ['#2563eb', '#dc2626', '#d97706'],
      tooltip: { trigger: 'axis', backgroundColor: '#0f172a', borderWidth: 0, textStyle: { color: '#fff' } },
      legend: { top: 0, right: 4, itemWidth: 12, textStyle: { color: '#64748b' } },
      grid: { top: 38, right: 24, bottom: 28, left: 48 },
      xAxis: { type: 'category', boundaryGap: false, data: data.map((d) => new Date(d.time).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit' })), axisLine: { lineStyle: { color: '#e6eaf0' } }, axisLabel: { color: '#64748b' } },
      yAxis: [{ type: 'value', axisLabel: { color: '#64748b' }, splitLine: { lineStyle: { color: '#eef1f5' } } }, { type: 'value', axisLabel: { color: '#64748b', formatter: '{value}ms' }, splitLine: { show: false } }],
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
  return <div ref={ref} className="ac-chart" role="img" aria-label="API 请求、错误和 P95 延迟趋势图" />
}
