# Sprint 6 证据清单（S6-QA-01 + 审计修复阻断项）

- 生成时间：2026-09-12
- **部署代码提交：`7dba1eb35a03a4dba68f49f1a75f30f6c2a56e01`（`7dba1eb`）**
- 部署口令：`20260912-235409`
- `backend/bin/api` SHA-256 = `59ac3cfd24d0932e0ff77259400b1ebcefa7fcc659e020fd1d306659e976c2e6`（前缀 `59ac3cfd`）
- `backend/bin/worker` SHA-256 = `ac98ce5de71bebb4fa75b9ba9860cda860fd8e1b30a28fe6f928e27e901bbd63`（前缀 `ac98ce5d`）
- 迁移：0051 / 0052 / 0053 / 0054 / **0055** 已应用（见 `runtime-verification.txt`）
- API E2E：**PASS 159 / FAIL 0 / NOT_VERIFIED 0**（`result.json`，`status=PASS`）
- Playwright（admin-desktop，含 390×844 移动宽度用例）：**9 PASS / 0 FAIL**（18 skipped 为设计内跳过）
- 构建可复现：`469fb61` 与 `7dba1eb` 两次部署得到**完全相同**的 `bin/api` / `bin/worker` 哈希

## 结果状态判定

`result.json` 由套件直接写入，判定条件为 **`fail==0 and not_verified==0 and pass>0`**，
并同时记录 `blocking_failures` / `blocking_not_verified` / `criteria` 三个字段。
套件退出码同样要求两项均为 0：

```bash
[ "$FAIL" -eq 0 ] && [ "$NV" -eq 0 ] || exit 1
```

因此**不存在"存在未验证项却报告 PASS"的路径**。

| 文件 | 字节 | 说明 | SHA-256（前 16 位） |
|---|---:|---|---|
| `files/audit-export.jsonl` | 519624 | ADMIN-008 导出的审计 JSONL（行数/manifest/SHA-256 已逐项核对） | `1cba6c36eaaf9bc1…` |
| `playwright-full.log` | 4841 | Playwright（admin-desktop）9 PASS / 0 FAIL，含新的 `AUDIT-004/UI` 完整性边界页面用例 | `60b181ef24536d17…` |
| `regression-rbac.log` | 3075 | S1–S3 RBAC 回归：PASS 29 / FAIL 0 / BLOCKED 2（与基线一致） | `8d5b8ebb15f68f02…` |
| `regression-sprint12.log` | 2617 | S1–S2 回归：PASS 21 / FAIL 0 / BLOCKED 0 | `78cda7f87e67197f…` |
| `regression-sprint4.log` | 5555 | S4 回归：**PASS 81 / FAIL 0 / NOT_VERIFIED 0**（S6 时的 1 项审计链失败已消除） | `93ec5eb27252f4db…` |
| `regression-sprint5.log` | 7872 | S5 回归：**PASS 106 / FAIL 0 / NOT_VERIFIED 0**（S6 时的 1 项审计链失败已消除） | `a4d650c0671681df…` |
| `result.json` | 328 | API E2E 结果：PASS 159 / FAIL 0 / NOT_VERIFIED 0，`status=PASS` | `3cdee12a76245ed8…` |
| `run.log` | 27064 | API E2E 完整逐条日志（每条 PASS 均对应 API/DB/文件/运行时断言） | `bcdb1fc86ee5abde…` |
| `runtime-verification.txt` | 2010 | 部署后运行时核对：服务/健康/迁移/运行期降权/边界状态/秘密扫描 | `e352d2bff19e09d8…` |
| `screenshots/audit-integrity.png` | 230524 | 新的完整性边界页面（桌面 1440×900，真实数据，fullPage） | `1b42a94fda577543…` |
| `screenshots/audit-integrity-mobile.png` | 182819 | 同一页面（移动 390×844，无横向溢出） | `316d4e3108c9661f…` |
| `screenshots/*.png`（其余 15 个） | — | S6 既有页面截图，本次未改动 | 见 git 历史 |

## 审计修复阻断项的关键证据（在 `runtime-verification.txt` 中可直接核对）

```text
integrity_incident=INC-AUDIT-GAP-1-2343 chain=audit gap=1-2343 events=2343 trusted_from=2344
integrity_sealed_tail_hash=3db79aeb77ce12ae8ccab3540891a7ff241bfc34af8f4a9dcdfd283578686ad1
integrity_evidence_hash=f0ad300fa191cf42fd290343763d012d73291f737bde34a10d085f562ab27967
integrity_approval=f72ca54a-554b-438c-9edf-2a403b76254d
integrity_events=1:requested@admin@local -> 2:approved@fixture-platform_admin@fixture.local -> 3:detected@admin@local -> 4:sealed@admin@local
integrity_retained_anchors=audit/2026-09-11 [1,92] sealed_unrecoverable archive_intact=true
gap_rows_present=0
anchors=audit/2026-09-11 [1,92]; audit/2026-09-12 [2344,2921]; login/2026-09-11 [1,109]; login/2026-09-12 [110,2035]
runtime_role_audit_truncate_priv=false
runtime_role_integrity_priv=
  audit_chain_anchors=true/true/false/false/false
  audit_integrity_incident_anchors=true/true/false/false/false
  audit_integrity_incident_events=true/true/false/false/false
  audit_integrity_incidents=true/true/false/false/false
```

权限矩阵读法：`INSERT/SELECT/UPDATE/DELETE/TRUNCATE`——运行角色对边界证据与既有锚点
**只能新增与读取，不能修改、删除或清空**；`DROP` 还需要 owner 身份（运行角色不是 owner）。
验收脚本 `AUDIT-003i..003s`、`AUDIT-004z2` 以运行角色真实执行这些破坏性语句并断言被拒绝。

## 未纳入仓库的文件

- `ids.env`（UUID 与邮箱，不含口令）、`/tmp/s6-e2e-env.sh`（运维临时口令文件，按约定不入库）。
- Playwright 的 `test-results/` 与 `playwright-report/`（失败时才产生 trace，本次无失败）。

## 证据日志的入库说明

仓库根 `.gitignore` 有一条通用规则 `*.log`。首次交付时它**同时**排除了本目录的验收证据日志，
导致 `manifest.md` 引用的 `run.log` / `playwright-full.log` / `regression-*.log`
实际只存在于验收机，无法随仓库复核。

本轮加入例外规则 `!acceptance-evidence/**/*.log`，上述日志现已入库，
可用表格中的 SHA-256 直接核对；`git check-ignore` 对该目录的 `.log` 已不再命中。
