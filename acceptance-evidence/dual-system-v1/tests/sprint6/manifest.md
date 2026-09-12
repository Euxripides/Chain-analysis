# Sprint 6 证据清单（S6-QA-01）

- 生成时间：2026-09-12
- 部署：`backend/bin/api` sha256 = `879258213e78d2383aef78525ec170eb8a67d2a5c86308eb74f42e4d541762fd`（前缀 `87925821`）
- 迁移：0051 / 0052 / 0053 / 0054 已应用（见 `runtime-verification.txt`）
- API E2E：**PASS 115 / FAIL 0 / NOT_VERIFIED 1**（`result.json`）
- Playwright（admin-desktop）：**8 PASS / 0 FAIL**

| 文件 | 字节 | 说明 | SHA-256（前 16 位） |
|---|---:|---|---|
| `files/audit-export.jsonl` | 519624 | ADMIN-008 导出的审计 JSONL（行数/manifest/SHA-256 已逐项核对） | `1cba6c36eaaf9bc1…` |
| `playwright-full.log` | 1496 | Playwright（admin-desktop，含 390×844 移动宽度用例）8 PASS / 0 FAIL | `40a4d1bd30ac4968…` |
| `regression-rbac.log` | 3087 | S1–S3 RBAC 回归：PASS 29 / FAIL 0 / BLOCKED 2（与 S5 基线一致） | `d549aa78cc47a173…` |
| `regression-sprint12.log` | 2628 | S1–S2 回归：PASS 21 / FAIL 0 | `dcceeffe9c8eec10…` |
| `regression-sprint4.log` | 5428 | S4 回归：PASS 78 / FAIL 1（唯一失败为审计链 valid，原因见报告 §6） | `c5f2531af8bf9cd2…` |
| `regression-sprint5.log` | 7769 | S5 回归：PASS 103 / FAIL 1（唯一失败为审计链 valid，原因见报告 §6） | `e221b382c89584a9…` |
| `result.json` | 196 | API E2E 结果：PASS 115 / FAIL 0 / NOT_VERIFIED 1 | `77a953037157961a…` |
| `run.log` | 19162 | API E2E 完整逐条日志（每条 PASS 均对应 API/DB/文件/运行时断言） | `e023fe613ec748f2…` |
| `runtime-verification.txt` | 842 | 部署后运行时核对：服务/健康/迁移/运行期降权/秘密扫描 | `85345b4fcb2a65c4…` |
| `screenshots/alerts.png` | 114245 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `b14e17b28deba401…` |
| `screenshots/api-keys.png` | 49043 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `5b31346725e8c835…` |
| `screenshots/backups.png` | 75111 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `f8887d4f1ab34d68…` |
| `screenshots/budgets.png` | 127490 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `79838cf0fd36125f…` |
| `screenshots/control.png` | 117104 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `68957fcca4e9be10…` |
| `screenshots/costs.png` | 91409 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `8042214d16630b3c…` |
| `screenshots/feature-flags.png` | 118568 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `4eebdbf17c08d44a…` |
| `screenshots/kill-switch.png` | 86561 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `b3b51fb67d9c4b33…` |
| `screenshots/kill-switch-mobile.png` | 40628 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `b9c75f8e265903a9…` |
| `screenshots/observability.png` | 127779 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `606d668fa0f988f4…` |
| `screenshots/overview.png` | 99444 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `99ab1689f1a0eff3…` |
| `screenshots/performance.png` | 105369 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `e6410d899aa556ac…` |
| `screenshots/provider-detail.png` | 57792 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `0083fb5c6fd2ff43…` |
| `screenshots/providers.png` | 105551 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `cfbd5c6539dc7f5b…` |
| `screenshots/requests.png` | 115599 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `72acce8e8f0abd04…` |
| `screenshots/retention.png` | 102088 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `eb0c44ad2b378565…` |
| `screenshots/routing.png` | 103343 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `3de163b507a3a06e…` |
| `screenshots/settings.png` | 91104 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `ca98b9acfed8c048…` |
| `screenshots/system-version.png` | 99259 | S6 页面截图（桌面 1440×900；kill-switch-mobile 为 390×844） | `8d843bb97032c6be…` |

## 未纳入仓库的文件

- `ids.env`：包含夹具账号邮箱与资源 UUID（可能含凭据相关字段），只在验收机上保留。
- 截图与导出文件均可由 `acceptance-evidence/dual-system-v1/tests/run-sprint6-e2e.sh` 与
  `docs/implementation/sprint6-report.md` §7 的命令重新生成。

## 与 S5 基线的差异

| 套件 | S5 基线 | S6 本轮 | 差异说明 |
|---|---|---|---|
| S5 API E2E | PASS 104 / FAIL 0 | PASS 103 / FAIL 1 | 唯一失败为 `审计链 valid=true`，由报告 §6 记录的 AUDIT-003 事件（历史前缀被清空）导致，非代码回归 |
| S4 API E2E | PASS 79 / FAIL 0 | PASS 78 / FAIL 1 | 同一原因（S4 的 AUDIT-001 也断言链有效） |
| RBAC | PASS 29 / FAIL 0 / BLOCKED 2 | PASS 29 / FAIL 0 / BLOCKED 2 | 一致 |
| Sprint1–2 | PASS 19 / FAIL 0 / BLOCKED 1 | PASS 21 / FAIL 0 / BLOCKED 0 | 本轮转正 |
| S6 API E2E | — | PASS 115 / FAIL 0 / NOT_VERIFIED 1 | NOT_VERIFIED 为每日锚点一致性（同 §6 原因） |
