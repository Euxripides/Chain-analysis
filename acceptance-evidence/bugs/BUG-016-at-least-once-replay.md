# BUG-016：暂停/恢复与租约回收存在 at-least-once 物理重复写入（FINAL 无业务重复）

- 严重等级：P3（语义记录，非缺陷定级）
- 状态：已确认行为
- 发现时间：2026-09-02 22:10 +07:00
- 环境：VM 192.168.31.152
- 影响模块：下载执行器 checkpoint/恢复
- 影响范围：REC-001/003 恢复后物理行 108,171 = FINAL 78,171 + 30,000（当前 checkpoint 页重写），FINAL 与业务 grain 完全唯一；属可接受的 at-least-once 语义，但会放大物理存储与 Merge 负载
- 复现步骤：见 11-recovery/rec003_final.sh 输出
- 预期结果：允许 at-least-once，但建议恢复时按已提交 page 跳过重放
- 数据影响：无业务重复
