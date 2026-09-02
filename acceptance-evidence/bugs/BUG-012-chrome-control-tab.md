# BUG-012：Chrome 上“管控页展示人工覆盖表单”用例 2/2 失败（“全局停机开关”5 秒内未渲染）

- 严重等级：P3
- 状态：可复现（Chrome 152.0.7977.64）
- 发现时间：2026-09-02 22:40 +07:00
- 环境：本地 dev server + VM API；Chrome channel
- 影响模块：API 控制中心 → 管控标签页
- 影响范围：同一用例在 Edge（2 轮）与 Firefox（1 轮）均通过，Chrome 两轮均在 getByText('全局停机开关') 5 秒超时；其前置断言“有效期（分钟）”已可见，说明仅停机开关区域渲染滞后
- 复现步骤：Chrome 打开 /api-control → 点击“管控”→ 等待“全局停机开关”出现（5s 超时）
- 实际结果：超时
- 预期结果：及时渲染
- 证据：acceptance-evidence/13-ui/UI-002-chrome-run.log 与 chrome-rerun.log
- 数据影响：无
