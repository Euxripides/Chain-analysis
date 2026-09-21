# 部署回滚演练（20260916T180650Z）

- 演练目标：**回滚实测可用**（§16.11/§17），且回滚后能再前进回冻结版本
- 上一版 API/Worker：`/home/euripides/deploy-backups/rollback-20260916T1735Z/api.previous` / `/home/euripides/deploy-backups/rollback-20260916T1735Z/worker.previous`
- 上一版前端 dist：`/home/euripides/projects/blockchain-platform/frontend/apps/user-console/dist.prev-20260916T104300Z` / `/home/euripides/projects/blockchain-platform/frontend/apps/admin-console/dist.prev-20260916T105530Z`
- 日志目录：`/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/release/rollback-drill-20260916T180650Z.logs`

## 1. 基线（冻结版本）

| 项 | 值 |
|---|---|
| API 二进制 | `7831bff0bc6127963840aba8f505b0c337243e2c69a871f2b4010e4dbec19e32`（单元 active） |
| Worker 二进制 | `0d49fb75105eb5ba81a0b062fa557fc09893d2a43555989e6128451a2c1f2c78`（单元 active） |
| migrate 二进制 | `d48e58fec83bc2af83ed322cf3f47da2f84ed4fe7f3873c1887c5481d06bf062` |
| 用户台 / 后台台 dist 入口 | `71714cdcf4a6c420c2177f6b607c155df70962a731d330c8be5c5eaf5f9de180` / `2d4a129e0a37cd018995ab46e2fdcfcbe069e210ea35cd4f1a91283ebf77c383` |
| 基线服务状态 | /health/live 200（0.06s）、/health/ready 200、用户台 200、后台台 200、TLS 入口 200 |
| 上一版 API / Worker | `cbb8d01b8067b6b2a1a6acc6272b68b7ba8e5808427cab6911a8690d528a66d7` / `4379522a7eeaf92871e357227cb24378413baf71cd64958614fafc929b6fcd45` |
== 阶段 B：后端回滚（API + Worker）==

## 2. 回滚到上一版（实测）

- 动作：`systemctl --user stop blockchain-api blockchain-worker` → 覆盖 `backend/bin/{api,worker}` → `start`
- **回滚 RTO（停止到 /health/live 200）= 6.13 s**（上限 180s）
- 回滚后哈希：api `cbb8d01b8067b6b2a1a6acc6272b68b7ba8e5808427cab6911a8690d528a66d7`、worker `4379522a7eeaf92871e357227cb24378413baf71cd64958614fafc929b6fcd45`（与上一版逐字节一致）
- 回滚后 /health/ready：200
- 结论：**上一版在现网数据库/配置上可以真实拉起并对外服务**（不是「理论上可回滚」）
== 阶段 C：前端回滚 ==

## 3. 前端回滚（实测）

- 上一版 dist：`/home/euripides/projects/blockchain-platform/frontend/apps/user-console/dist.prev-20260916T104300Z` / `/home/euripides/projects/blockchain-platform/frontend/apps/admin-console/dist.prev-20260916T105530Z`
- **回滚 RTO**：用户台 3.25 s、后台台 0.04 s
- 服务端下发的入口哈希：用户台 `e417d66a64a6d38c2466575b38d3ac4f4519aa0f9755bf51125bf7cae60feff2`、后台台 `3684b55721d3a6591b6cac421d2f2ce1cd1b792efb6467a1b4e0bf74b08cb7ae`（等于上一版磁盘入口哈希）
- 结论：前端**换版可回滚且可观测**（服务端实际下发 = 上一版产物）
== 阶段 D：回滚后再前进（恢复冻结版本）==

## 4. 回滚后再前进（恢复冻结版本，实测）

- **前滚 RTO = 6.12 s**
- 前滚后哈希：api `7831bff0bc6127963840aba8f505b0c337243e2c69a871f2b4010e4dbec19e32`、worker `0d49fb75105eb5ba81a0b062fa557fc09893d2a43555989e6128451a2c1f2c78`、migrate `d48e58fec83bc2af83ed322cf3f47da2f84ed4fe7f3873c1887c5481d06bf062`（与基线逐条一致）
- 前滚后前端入口：用户台 `71714cdcf4a6c420c2177f6b607c155df70962a731d330c8be5c5eaf5f9de180`、后台台 `2d4a129e0a37cd018995ab46e2fdcfcbe069e210ea35cd4f1a91283ebf77c383`（与基线一致）
- 前滚后 /health/ready 200、TLS 入口 200

## 5. 判定

- 回滚 RTO：后端 6.13 s；前端 3.25 / 0.04 s；前滚 RTO 6.12 s
- 断言失败：**0** 条
- **本次演练判定：PASS**

- 说明：演练期间 API/Worker/控制台会短暂中断（重启窗口），这是回滚演练的固有代价；中断时长即上表 RTO。
