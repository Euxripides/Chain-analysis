# 新建数据下载 · Dialog 动效时长（REM-009 规范修正）

## 为什么有这份记录

旧的「新建数据下载 Flowline 界面重构实施规范」把 Dialog 时长写成：

```text
dialogEnter: 220ms
dialogExit:  150ms
遮罩:        180ms
```

这组数字与**共享 Dialog renderer 的实际实现**不一致（退出与遮罩都是 160ms）。
共享 Dialog 被用户端多个页面共同使用，不能为了下载页单独修改它，
因此本轮的处理是**修正规范与验收**，不是修改共享实现。

## 权威时长（唯一事实来源）

来源：`frontend/packages/user-ui/styles/motion.css`（`UserDialogRenderer` 通过
`transitionName="cs-dialog"` / `maskTransitionName="cs-dialog-mask"` 使用）。

| 部位 | 进入 | 退出 | 来源 |
|---|---|---|---|
| Dialog 面板 | `var(--cs-dur-standard)` = **220ms**（opacity + `translateY(10px) scale(.96)` → none） | **160ms**（opacity） | motion.css `.cs-dialog-enter-active` / `.cs-dialog-leave-active` |
| 遮罩 | **160ms**（opacity） | **160ms**（opacity） | motion.css `.cs-dialog-mask-*-active` |
| `prefers-reduced-motion: reduce` | `transition: none !important` | 同左 | motion.css `@media (prefers-reduced-motion: reduce)` |

遮罩自 2026-09-20 起不再使用模糊（玻璃拟态整体退役），因此这里不存在 blur 时长的分歧。

## 下载页必须遵守

- 不创建第二套 Dialog CSS，不覆盖共享 `.cs-dialog-*` 的 animation / transition / duration。
- 唯一允许的页面级 `.cs-dialog` 规则是 `max-height` 类的**尺寸**适配，且必须限定在本页作用域：

  ```css
  .cs-dialog:has(.dl-dialog) .cs-dialog__body { /* 窄屏内部滚动 */ }
  ```

- 关闭后 mask 与面板不得残留，焦点归还触发元素，窄屏只在 Dialog 内部滚动。

## 验收如何证明

`frontend/scripts/download-flowline-remediation-acceptance.mjs` 的 `runDialogContract`：

1. 读 `download-new.css` 原始文本，任何 `.cs-dialog` 行只要含 `duration` / `animation` / `transition` / `{`
   就判失败（`:has(.dl-dialog)` 的尺寸规则除外）；
2. 真实浏览器里打开预览 Dialog，读取面板与遮罩的 computed `animation-duration` / `transition-duration`；
3. 关闭后断言 `.cs-dialog` 与 `.ant-modal-mask` 数量都为 0（无残留）。

原始测量写入 `frontend/acceptance-evidence/download-flowline-remediation/report.json` 的 `dialog` 字段。
