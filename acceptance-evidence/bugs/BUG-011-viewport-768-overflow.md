# BUG-011：768×1024 视口下 /data-explorer 页面横向溢出

- 严重等级：P3
- 状态：可复现（Playwright）
- 发现时间：2026-09-02 23:08 +07:00
- 环境：本地 dev server（当前源码）+ VM API；Edge 152.0.4191.53
- 影响模块：数据浏览页（地址目录表 + 工具栏）
- 影响范围：UI-001 视口矩阵：768×1024 下 documentElement.scrollWidth > clientWidth；390×844（移动端布局）与 1366×768、1440×900、1920×1080 均无溢出
- 复现步骤：视口设为 768×1024 → 访问 /data-explorer → 检测横向溢出为 true
- 实际结果：页面级横向滚动出现
- 预期结果：无页面级横向溢出
- 证据：acceptance-evidence/13-ui/768-overflow-trace/（trace + error-context 页面快照）
- 数据影响：无
