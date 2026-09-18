# S7-SEC-01 完成输出（§28.10 格式）

```text
Task ID: S7-SEC-01
Goal:    完成威胁矩阵、依赖扫描、SBOM、镜像/IaC 检查、渗透负向用例（计划 §28.9 第 2376 行）
```

## Files changed

**新增（全部为证据/测试资产，未改任何产品代码、配置或迁移）**

| 路径 | 性质 |
|---|---|
| `acceptance-evidence/dual-system-v1/tests/run-sprint7-sec-negative.sh` | 新增可重跑负向套件（456 行，md5 `3defa1374e7eb4388827a2ab1fe75ee8`） |
| `acceptance-evidence/dual-system-v1/sprint7/security/threat-matrix.md` | 交付物 1 |
| `.../security/dependency-scan/**`（44 文件） | 交付物 2 |
| `.../security/sbom/**` | 交付物 3 |
| `.../security/image-iac/**` | 交付物 4 |
| `.../security/negative-tests/**`（51 文件） | 交付物 5 |
| `.../security/secret-scan/**`（12 文件）+ `tests/run-sprint7-secret-scan.sh` | 交付物 6 |
| `.../security/application-account-separation.md` + `application-account-separation/raw-privilege-measurement.txt` | 交付物 7 |
| `.../security/README.md`、本文件 | 索引与完成记录 |

**未改动**：`backend/**`、`frontend/**`、`deploy/.env`、`deploy/*.conf`、`deploy/*.service`、任何迁移文件、`go.mod`/`go.sum`/`pnpm-lock.yaml`（lockfile 审计前后 md5 一致，见 `dependency-scan/frontend/lockfile-fingerprint.txt`）。
**未新增**任何依赖到项目（govulncheck 装于 `/tmp/gtools`；syft/trivy 只以容器运行）。

## API contracts changed

无。仅**发现**一处契约缺陷 DEF-01（`POST /api/v1/admin/action-api-keys` 省略 `allowed_ips` 时 500），未修改。

## Migrations added/applied

无新增迁移。本轮**只读**验证了既有迁移 0054（`blockchain_app` 角色与 REVOKE）、0055（审计完整性边界与锚点触发器）的落地效果。

## Permissions added

无。仅验证了既有权限表：后台 105 条路由 100% 声明权限；`api_client` 角色 0 条 `admin:*`；
`blockchain_app` 对审计表 `SELECT/INSERT` 通过、`UPDATE/DELETE/TRUNCATE` 全部拒绝。

## Audit events added

本轮**未新增审计事件**，但**读取并以此作为证据**：
- `authorization.denied`（伪造头/未认证/无权限/CSRF 失败）——AUTH-009e、UNAUTH-b、RBAC-008g
- `auth.login.*`（success/failure/blocked + `reason_code`）——SEC-004e/f
- `admin.provider.update`（SEC-003 可逆写入的 before/after）——SEC-003g
- `admin.session.revoke`——AUTHZ-e/f
（另：套件运行本身产生的正常业务审计事件按系统设计写入，不受本任务控制。）

## Tests run and exact results

| 套件 | 命令 | 结果 |
|---|---|---|
| 渗透负向（本任务） | `bash acceptance-evidence/dual-system-v1/tests/run-sprint7-sec-negative.sh` | **PASS 87 / FAIL 3 / NOT_VERIFIED 0**，`status=FAIL`（`negative-tests/result.json`） |
| 依赖扫描 Go | `govulncheck ./...`（backend） | exit 3（= 有发现）；12 条 |
| 依赖扫描前端 | `pnpm audit --json`（frontend） | exit 1；3 条 |
| SSRF 守卫单测 | `go test ./internal/tokenmeta/ -run TestLogoSSRFGuardRejectsPrivateAndMetadataAddresses -v` | PASS（套件内 SEC-003a） |
| SBOM | `syft bin/api` / `syft dir:frontend` | exit 0 ×4；CycloneDX 1.7 + SPDX 2.3 |
| 镜像扫描 | `trivy image` × 4 | exit 0；CRITICAL 20 / HIGH 203 |
| 秘密扫描 | `bash acceptance-evidence/dual-system-v1/tests/run-sprint7-secret-scan.sh` | EXIT=0（0 条明文），连续 3 次重跑一致 |

**3 条 FAIL 的精确清单**：`AUTH-013c`（Cookie 无 `__Host-`）、`AUTH-013d`（Cookie 无 `Secure`）、`RBAC-008h`（省略 `allowed_ips` 建 Key 返回 500）。
两轮构建（`753ba724…`、`6106d8e4…`）结果一致。

## Manual UI evidence

**无**。S7-SEC-01 不含 UI 交付；CSP/点击劫持以真实响应头验证（`negative-tests/files/SEC-002-*-headers.txt`），
未执行浏览器级 XSS 渲染验证（见「Known limitations」）。

## Data/file/runtime evidence

- DB：`SET ROLE blockchain_app` 的 12 条越权实测（`application-account-separation/raw-privilege-measurement.txt`、`negative-tests/files/AUDIT-role.txt`）
- 文件：49 个原始请求/响应/DB 查询存档（`negative-tests/files/`）；SBOM 4 份、trivy 报告 8 份、govulncheck 原始输出
- 运行时：API 健康 200、`build_api_sha` 前后一致、`pg_stat_activity` 17 条应用连接、监听端口实测

## Security considerations

- 共 13 条残余风险（R-01…R-11 + DEF-01/DEF-02），其中 **P1 = 4**、P2 = 4、P3 = 5；**P0 = 0**
- §23.8 安全发布门**当前不通过**（依赖 P0/P1、基础镜像 CRITICAL/HIGH、连接账号仍为超级用户、Cookie 缺 Secure/`__Host-`）
- 重要负面结论：trivy 的 misconfig 扫描器**不认识**本仓库的 nginx/systemd/compose，`num=0` 不等于「0 问题」
- 正面结口：RBAC-008（API Key audience/scope）由本轮首次实测覆盖，收口了 `docs/implementation/dual-system-progress.md:103` 记录的 PARTIAL

## Rollback procedure

本任务**无产品变更**，故无需回滚。若需清除证据：删除 `acceptance-evidence/dual-system-v1/sprint7/security/` 与
`acceptance-evidence/dual-system-v1/tests/run-sprint7-sec-negative.sh`、`run-sprint7-secret-scan.sh` 即可；
数据库侧无残留（案件 0、API Key 0、Provider 端点已还原、无账号被遗留锁定，仅 1 个已停用临时账号）。

## Known limitations / follow-ups

1. **XSS 未做浏览器级验证**：只验证了 API 响应无标签、DB 无标签、CSP 头正确、前端无 `dangerouslySetInnerHTML`；
   未用真实浏览器执行 payload 观察 `[role=alert]`（§27.6 SEC-002 期望「浏览器/CSP report」证据）。
2. **OIDC / 真实 IdP 未接**：AUTH-012c 只证明「未启用时不产生站外跳转」。
3. **跨租户资源类型未穷尽**：覆盖案件/评论/工作区/后台端点/API Key；findings/evidence/report/canvas/download 依赖 S5 套件。
4. **依赖漏洞的运行时可利用性**未证（需污点/动态分析）。
5. **ClickHouse / MinIO / NATS 的应用账号分离**未验证（只覆盖 PostgreSQL）。
6. **§23.8「角色停用后旧请求不能写入」**未在本轮覆盖（属 RBAC-006）。
7. **未接入 CI**：`docs/dual-system-development-plan.md` §29.1 的管道尚未包含依赖/SBOM/镜像/IaC 步骤（GATE-05）。
8. 建议把 `run-sprint7-sec-negative.sh` 注册进 `tests/run-sprint7-qa.sh` 的 `run_suite` 列表（本轮未改该文件，避免与并行任务冲突）。
