# 阶段 N 汇总：UI、兼容性与可访问性

- UI-001 部分通过: 390×844（移动端布局）、1366×768、1440×900、1920×1080 无横向溢出且宽屏有效利用（main.content>1600px）；768×1024 下 /data-explorer 溢出 → BUG-011
- UI-002 通过(2/3) + 未执行(1): Edge 152.0.4191.53 全量回归 39+44 通过；Chrome 152.0.7977.64 14+5 通过（1 例管控页渲染超时 → BUG-012）；Firefox（Playwright 152.x bundled）核心 6 用例全通过；Firefox 完整版本未单独记录（bundled build）
- UI-003 部分通过: “选择代币”弹窗 Escape 关闭正常；“修改案件和标签”弹窗 Escape 无效 → BUG-010；表格勾选/链选择/Token 筛选键盘操作可用（smoke chain 选择 End+Enter 断言通过）
- UI-004 通过: 离线触发错误弹窗（dialog 出现）、恢复网络后可重试且数据行恢复；4 秒接口延迟下加载状态保持、无页面溢出、无重复任务（重复点击防抖由 disable 状态保证）
- UI-005 通过: 官方 Logo 真实加载（naturalWidth>0）；损坏/缺失 Logo 走回退（U5DТ logo 404 → 回退图标，无布局跳动断言通过 smoke）；仿冒合约不复用官方 Logo（data-contract 与 logo URL 独立）
- CTL-007 复核: 页面正文仅业务数据/字段标签/导航/操作控件，提示均通过带标题与关闭的弹窗
