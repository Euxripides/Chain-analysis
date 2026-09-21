# 重复消息 → 重复数据 实测（20260916T173134Z）

- 目标：已完成 job `1f104b01-8c13-40db-8625-fb928f5a84f9` 的事件 subject `events.jobs.1f104b01-8c13-40db-8625-fb928f5a84f9`
- 判定：**FAIL**

| 检查 | 结论 | 证据 |
|---|---|---|
| ① outbox dedupe_key 唯一 | FAIL | 重复 dedupe_key 的插入**未被拒绝** |
| ② JetStream msg-id 去重 | FAIL | 计数不符合预期：442696 → 442691 → 442685（同 msg-id 第二次应被丢弃） |
| ③ 消费者幂等（重投） | PASS | 对已终态 job 的 COMPLETED 事件做不同 msg-id 的重投：steps/revision/audit/notifications 全部无变化 |

## 计数对照

| 指标 | 重投前 | 重投后 |
|---|---|---|
| steps | completed:532,failed:7,running:3 | completed:532,failed:7,running:3 |
| revision_sum | 612 | 612 |
| audit_rows | 93080 | 93080 |
| notifications | 104 | 104 |

- EVENTS 流 messages：442696 → 442691（同 msg-id 重复发布）→ 442685（不同 msg-id 重投）
