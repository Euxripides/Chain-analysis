# 重复消息 → 重复数据：四道防线实测（20260916T173500Z）

- 判定：**FAIL**

| 防线 | 结论 | 证据 |
|---|---|---|
| ② JetStream msg-id 去重 | FAIL | last_seq 轨迹不符合预期：{'last_seq': 2529403, 'messages': 441833} → {'last_seq': 2529403, 'messages': 441828} → {'last_seq': 2529403, 'messages': 441824} → {'last_seq': 2529403, 'messages': 441820} |
| ① outbox dedupe_key 唯一 | PASS | 重复写入被唯一约束拒绝（23505；验证在事务内完成并已回滚） |
| ① outbox (aggregate,sequence) 唯一 | PASS | 重复写入被唯一约束拒绝（23505；验证在事务内完成并已回滚） |
| ③ 消费者幂等（重投） | PASS | 已终态 job 1f104b01-8c13-40db-8625-fb928f5a84f9 的 COMPLETED 事件重投后：步骤状态/revision/审计/通知四项全部不变 |
| ④ ReplacingMergeTree 合并 | PASS | 同一排序键写入两次：count()=2、count(FINAL)=1（重复在查询语义上被合并；临时表建在演练库 blockchain_dr_2f45fa9eee7c，测试后已 drop） |

- EVENTS 流状态轨迹（last_seq/messages）：{'last_seq': 2529403, 'messages': 441833} → {'last_seq': 2529403, 'messages': 441828} → {'last_seq': 2529403, 'messages': 441824} → {'last_seq': 2529403, 'messages': 441820}
