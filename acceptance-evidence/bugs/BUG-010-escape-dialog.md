# BUG-010：键盘 Escape 无法关闭“修改案件和标签”弹窗（焦点未归还）

- 严重等级：P3
- 状态：可复现（Playwright）
- 发现时间：2026-09-02 23:10 +07:00
- 环境：本地 dev server（当前源码）+ VM API；Playwright 1.62.1 / Edge 152.0.4191.53
- 影响模块：地址详情头部元数据弹窗
- 影响范围：UI-003 键盘可达性：Escape 后弹窗仍存在；对比“选择代币”弹窗 Escape 正常关闭
- 复现步骤：进入地址详情 → 点击“修改案件和标签”→ 按 Escape → 弹窗未关闭（toHaveCount(0) 失败，收到 1）
- 实际结果：弹窗保持打开
- 预期结果：Escape 关闭并把焦点还给触发按钮
- 证据：acceptance-evidence/13-ui/escape-dialog-trace/（trace + error-context）
- 数据影响：无
