# AD-009：提示信息只允许通过弹窗呈现

- 状态：Accepted
- 日期：2026-09-12
- 关联：`AGENTS.md`、`docs/code-standards.md`、§7.3

## 决策

1. `packages/ui/Dialog.tsx` 提供 `useDialog()`，暴露 `notify()` 与 `confirm()` 两个命令式接口；
   它是两套系统里**唯一**的提示通道。
2. 页面正文只允许出现业务数据、字段标签、导航和操作控件；禁止横幅、提示卡、常驻警告条、说明段落。
3. 弹窗必须具备：语义标题、正文、关闭操作、焦点陷阱、ESC 关闭、关闭后焦点回到触发元素、长内容在 body 内滚动。
4. 危险操作使用 `confirmWord`（例如 `REVOKE-ALL`）强制显式确认。

## 落地验证

Playwright `dual-system.spec.ts` 中的 `AGENTS 约束` 用例会扫描
`[role=alert]`、`.banner`、`.alert`、`.notice`、`.callout`、`.toast-inline`，
在任何页面出现即判定失败；`UX-001` 用例验证焦点陷阱、ESC 与焦点回归。
