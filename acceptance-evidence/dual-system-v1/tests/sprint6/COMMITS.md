# Sprint 6 提交与部署对应关系

## 代码基线

- 分支：`feat/dual-system-iam-foundation`
- S5 验收基线：`0ae1f4d`（部署 SHA `e639707`）
- Sprint 6 提交（按顺序）：

```text
dd1286f docs(s6): refresh audit coverage and surface inventory
f5588e8 test(s6): verify alert to request/provider/shard/checkpoint/object trace
c1ae2b9 docs(s6): acceptance evidence, manifest and regression results
2e7b03c docs(s6): report, ADR-017, runbook and OpenAPI contracts
b5f0a15 feat(s6): admin console pages for control, governance and observability
bab81a9 feat(s6): control plane, governance and observability backend
```

## 部署产物（验收环境）

| 项 | 值 |
|---|---|
| 部署方式 | `python3 scripts/deploy_dual_system.py --yes` |
| `backend/bin/api` SHA-256 | `879258213e78d2383aef78525ec170eb8a67d2a5c86308eb74f42e4d541762fd` |
| `backend/bin/worker` SHA-256 | `5510ab144f731c08ab1c50005407474c3c995b66cb20c37a2b5fdb4864606b45` |
| 校验方式 | 在验收机用同步后的同一份源码重新 `go build`，产物哈希与已部署二进制**完全一致** |
| 后台前端 | `frontend/apps/admin-console/dist` 33 个资源（16 个 S6 页面各自成 chunk） |
| 迁移 | `0051_s6_control_governance`、`0052_s6_data_governance`、`0053_s6_permission_grants`、`0054_s6_append_only_hardening` |
| E2E `build_sha` | `post-deploy-87925821`（即上面 api 二进制的 SHA-256 前 8 位） |

## 验收结论

- API E2E：**PASS 115 / FAIL 0 / NOT_VERIFIED 1**（`result.json`）
- Playwright（admin-desktop，含移动宽度用例）：**8 PASS / 0 FAIL**
- S1–S5 回归：RBAC 29/0/2、Sprint1–2 21/0/0、S4 78/1、S5 103/1
  （S4/S5 的唯一失败为审计链 `valid=true` 断言，根因见 `docs/implementation/sprint6-report.md` §6）
- 唯一 NOT_VERIFIED：每日锚点与其覆盖区间事件行的一致性（同一根因）
