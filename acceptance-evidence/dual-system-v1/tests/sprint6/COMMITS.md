# Sprint 6 提交与部署对应关系

## 代码基线

- 分支：`feat/dual-system-iam-foundation`
- S5 验收基线：`0ae1f4d`（部署 SHA `e639707`）
- Sprint 6 首次交付提交（按顺序）：

```text
bab81a9 feat(s6): control plane, governance and observability backend
b5f0a15 feat(s6): admin console pages for control, governance and observability
2e7b03c docs(s6): report, ADR-017, runbook and OpenAPI contracts
c1ae2b9 docs(s6): acceptance evidence, manifest and regression results
f5588e8 test(s6): verify alert to request/provider/shard/checkpoint/object trace
dd1286f docs(s6): refresh audit coverage and surface inventory
9b883d0 docs(s6): map sprint 6 commits to the deployed artifact hash
```

- Sprint 6 审计修复阻断项提交（按顺序）：

```text
469fb61 fix(s6): seal unrecoverable audit gap behind an immutable integrity boundary
e0d0b9f test(s6): fix audit-004 harness races and boolean casts
7dba1eb docs(s6): acceptance evidence for the audit integrity boundary
```

## 部署产物（验收环境）

| 项 | 值 |
|---|---|
| 部署目录（VM `dataserver` / 192.168.31.152） | `/home/euripides/projects/blockchain-platform` |
| 部署方式 | `py -3.12 scripts/deploy_dual_system.py --yes`（Windows；Linux/macOS 为 `python3`） |
| 服务管理方式 | **systemd 用户单元**（`systemctl --user`），不是系统单元 |
| 服务单元名 | `blockchain-api.service`（:8080）、`blockchain-worker.service`（:8081） |
| **部署代码提交** | `7dba1eb35a03a4dba68f49f1a75f30f6c2a56e01` |
| 已部署 api 绝对路径 / SHA-256 | `/home/euripides/projects/blockchain-platform/backend/bin/api` = `59ac3cfd24d0932e0ff77259400b1ebcefa7fcc659e020fd1d306659e976c2e6` |
| 已部署 worker 绝对路径 / SHA-256 | `/home/euripides/projects/blockchain-platform/backend/bin/worker` = `ac98ce5de71bebb4fa75b9ba9860cda860fd8e1b30a28fe6f928e27e901bbd63` |
| 校验方式 | 在验收机用同步后的同一份源码重新 `go build`，产物哈希与已部署二进制**完全一致** |
| 构建可复现性 | `469fb61` 与 `7dba1eb` 两次独立部署产出**完全相同**的 api/worker 哈希（后端源码在两提交间未变） |
| 后台前端 | `frontend/apps/admin-console/dist` 34 个资源（17 个 S6 页面各自成 chunk，含新的 `AuditIntegrityPage`） |
| 迁移 | `0051`、`0052`、`0053`、`0054`、**`0055_s6_audit_integrity_boundary`** |
| E2E `build_sha` | `7dba1eb35a03a4dba68f49f1a75f30f6c2a56e01` |

> **路径与单元名消歧（2026-09-12 独立复核补充）**
>
> 上表的哈希只对 `backend/bin/` 下的绝对路径成立。VM 上另存在一个**顶层遗留目录**
> `/home/euripides/projects/blockchain-platform/bin/`，其中的 `api`/`worker` 是 2026-09-08~09
> 更早手工构建的产物（`bin/api` = `33440571…`、`bin/worker` = `7c2f26b7…`），
> **未被 systemd、部署脚本或任何配置引用**，也不是本次验收的部署产物。
> 它之所以存在，是因为 `deploy_dual_system.py` 的 `SKIP_DIRS` 含 `bin`（不同步任何 `bin` 目录）、
> 且编译输出固定写到 `backend/bin/`——所以顶层 `bin/` 只保留历史产物、从不被刷新。
> 核对二进制时请使用上表的绝对路径。
>
> 同理，服务不在 `chainsight-api` / `chainsight-worker` 单元下（这两个名字不存在）；
> 查状态请用 `systemctl --user status blockchain-api blockchain-worker`。
>
> 完整复核见 `../sprint6-reverify-20260912/REVERIFY.md`。

### commit → 部署 SHA 映射

| commit | 内容 | 是否改变部署产物 | 部署产物 SHA-256 |
|---|---|---|---|
| `469fb61` | 审计完整性事件边界（迁移 0055 + 后端 + 前端 + 验收脚本） | **是** | `59ac3cfd…`（api）、`ac98ce5d…`（worker） |
| `e0d0b9f` | 修复 AUDIT-004 验收脚本的布尔转换与链头竞态 | 否（仅测试脚本） | 同上 |
| `7dba1eb` | 验收证据、报告 §6bis、ADR-018、Playwright `AUDIT-004/UI` 用例、`.gitignore` 证据日志例外 | 否（无后端/前端运行时源码改动） | 同上（已重新部署验证，产物哈希不变） |

结论：**验收环境当前运行的 `bin/api` / `bin/worker` 由 `7dba1eb` 的源码构建**，
其源码与 `469fb61` 的后端源码逐字节相同，因此哈希不变。
`runtime-verification.txt` 中同时记录 `code_sha` 与两个二进制哈希，可直接核对。

## 验收结论（在 `7dba1eb` 的部署产物上重新执行）

| 套件 | 结果 |
|---|---|
| API E2E `run-sprint6-e2e.sh` | **PASS 159 / FAIL 0 / NOT_VERIFIED 0**，`status=PASS` |
| Playwright `dual-system-sprint6.spec.ts`（admin-desktop） | **9 PASS / 0 FAIL** |
| RBAC 回归 | PASS 29 / FAIL 0 / BLOCKED 2 |
| Sprint 1–2 回归 | PASS 21 / FAIL 0 / BLOCKED 0 |
| Sprint 4 回归 | **PASS 81 / FAIL 0 / NOT_VERIFIED 0** |
| Sprint 5 回归 | **PASS 106 / FAIL 0 / NOT_VERIFIED 0** |

- NOT_VERIFIED：**0**。Sprint 6 首次交付中的唯一 NOT_VERIFIED（每日锚点与其区间事件行的一致性）
  已由完整性事件边界消除（见 `docs/adr/ADR-018-audit-integrity-incident-boundary.md`）。
- S4 / S5 首次交付时各有 1 项审计链 `valid=true` 失败，本轮两套件均为 0 失败。
