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

- Sprint 6 审计修复阻断项提交：

```text
469fb61 fix(s6): seal unrecoverable audit gap behind an immutable integrity boundary
e0d0b9f test(s6): fix audit-004 harness races and boolean casts
1b54b04 docs(s6): acceptance evidence for the audit integrity boundary
```

## 部署产物（验收环境）

| 项 | 值 |
|---|---|
| 部署方式 | `py -3.12 scripts/deploy_dual_system.py --yes` |
| **部署代码提交** | `469fb61c6a12acec77dfe6631a2276c368645019` |
| `backend/bin/api` SHA-256 | `59ac3cfd24d0932e0ff77259400b1ebcefa7fcc659e020fd1d306659e976c2e6` |
| `backend/bin/worker` SHA-256 | `ac98ce5de71bebb4fa75b9ba9860cda860fd8e1b30a28fe6f928e27e901bbd63` |
| 校验方式 | 在验收机用同步后的同一份源码重新 `go build`，产物哈希与已部署二进制**完全一致** |
| 后台前端 | `frontend/apps/admin-console/dist` 34 个资源（17 个 S6 页面各自成 chunk，含新的 `AuditIntegrityPage`） |
| 迁移 | `0051`、`0052`、`0053`、`0054`、**`0055_s6_audit_integrity_boundary`** |
| E2E `build_sha` | `469fb61c6a12acec77dfe6631a2276c368645019` |

### commit → 部署 SHA 映射

| commit | 内容 | 是否改变部署产物 | 部署产物 SHA-256 |
|---|---|---|---|
| `469fb61` | 审计完整性事件边界（迁移 0055 + 后端 + 前端 + 验收脚本） | **是**（`bin/api` / `bin/worker` / admin-console dist） | `59ac3cfd…`（api）、`ac98ce5d…`（worker） |
| `e0d0b9f` | 修复 AUDIT-004 验收脚本的布尔转换与链头竞态 | 否（仅测试脚本） | 同上，未重新编译 |
| 证据/文档提交 | `manifest.md` / `COMMITS.md` / 报告 / 证据文件 / Playwright 用例 | 否（不含后端与前端运行时源码改动） | 同上，未重新编译 |

结论：**验收环境上运行的 `bin/api`/`bin/worker` 由 `469fb61` 的源码构建**；
后续提交只包含测试与证据，未改变部署产物（`runtime-verification.txt` 中的 SHA-256 可直接核对）。

## 验收结论

- API E2E：**PASS 159 / FAIL 0 / NOT_VERIFIED 0**（`result.json`，`status=PASS`）
- Playwright（admin-desktop，含移动宽度用例）：**9 PASS / 0 FAIL**
- S1–S5 回归：RBAC 29/0/2、Sprint1–2 21/0/0、**S4 81/0/0**、**S5 106/0/0**
- NOT_VERIFIED：**0**（Sprint 6 首次交付中的唯一 NOT_VERIFIED 已由完整性事件边界消除）
