# S7-SEC-01 证据索引与结论摘要

> 任务：`S7-SEC-01`（计划 §28.9 第 2376 行）：完成威胁矩阵、依赖扫描、SBOM、镜像/IaC 检查、渗透负向用例
> 目录：`acceptance-evidence/dual-system-v1/sprint7/security/`
> 环境：Ubuntu VM `192.168.31.152`；API `127.0.0.1:8080`（PID 手工进程），Nginx `127.0.0.1:8088`
> 权威套件构建指纹：`backend/bin/api` SHA-256 `6106d8e4b09ed881864c9bc9212b29a45b698dc78825dcf936d317b6ed890a10`
> 采集窗口：2026-09-12T23:33 – 2026-09-13T00:12 (+07:00)
>
> ⚠️ **执行期间 VM 上存在并发部署循环**，`backend/bin/api` 约 40 分钟内被重建 ≥5 次
> （`753ba724` → `6106d8e4` → `bae72e0a` → `23a8dd77` → `47b95f29` → `7816e168`）。
> 本目录的**权威套件结果对应构建 `6106d8e4`**；此后产品侧又发生了 R-05 / R-03 相关改动，
> 其中引入一个 **P0 回归**，详见 `negative-tests/CURRENT-BUILD-FINDINGS.md`。

---

## 1. 交付物对照

| # | 要求 | 产出一 | 状态 |
|---|---|---|---|
| 1 | 威胁矩阵（§23 逐信任边界 STRIDE） | `threat-matrix.md` | ✅ 7 条边界 / 40+ 条威胁，逐条给出资产·威胁·控制（文件:行号/迁移/测试 ID）·残余风险·验证方式 |
| 2a | Go 依赖扫描 | `dependency-scan/go/govulncheck.txt`、`govulncheck-verbose.txt`、`command.txt`、`tool-versions.txt` | ✅ govulncheck v1.8.0（DB 2026-09-10），exit 3 = 发现漏洞 |
| 2b | 前端依赖扫描 | `dependency-scan/frontend/pnpm-audit.json`、`lockfile-fingerprint.txt` | ✅ pnpm audit --json（exit 1），lockfile 审计前后 md5 一致 |
| 2c | CVE 汇总与处置 | `dependency-scan/SUMMARY.md` | ✅ **15 条**（P0=2 / P1=5 / P2=3 / P3=1 / 无等级 4），含可达性判定 |
| 3 | SBOM | `sbom/backend.cdx.json`、`backend.spdx.json`、`frontend.cdx.json`、`frontend.spdx.json`、`syft-version.txt`、`commands.txt`、`SUMMARY.md` | ✅ syft 1.51.1，CycloneDX 1.7 + SPDX；backend 56 / frontend 184 组件；对应构建 `bae72e0a…`（初版误扫陈旧文件 `<project>/bin/api`，已更正，见 `sbom/SCAN-TARGET-STABILITY-REVIEW-NOTE.md`） |
| 4a | 镜像/IaC：仓库配置扫描 | `image-iac/misconfig/`、`image-iac/SUMMARY.md` | ❌ **NOT_VERIFIED（不是 PASS）** —— 见 §4：trivy 与 checkov **结构性不支持** compose/systemd/nginx，KICS 未跑完；**不得把 `num=0` 当作配置基线通过** |
| 4b | 镜像扫描 | `image-iac/images/*.json`、`*.txt`、`image-digests.txt` | ✅ 4 个 compose 镜像全扫：**CRITICAL 20 / HIGH 203 / MEDIUM 331 / LOW 201 / UNKNOWN 8** |
| 5 | 渗透负向用例 | `tests/run-sprint7-sec-negative.sh` + `negative-tests/` | ✅ **PASS 87 / FAIL 3 / NOT_VERIFIED 0**（`result.json`） |
| 6 | AUDIT-002 日志秘密扫描 | `secret-scan/scan-secrets.sh` + `REPORT.md` + `raw/` | ✅ **0 条真实明文命中**（(c) 类 = 0） |
| 7 | 应用账号分离实测 | `application-account-separation.md` + `raw-privilege-measurement.txt` | ⚠️ **PARTIAL**：运行角色隔离成立；连接账号仍是超级用户（GAP-01） |

---

## 2. 一句话结论

**S7-SEC-01 的执行面已全部落地并可复现；但 §23.8 安全发布门当前不通过**，阻断项为 4 条 P1：
R-09（依赖 P0=2/P1=5）、**R-10（基础镜像 CRITICAL 20 / HIGH 203）**、R-07（应用连接账号仍为超级用户）、
R-05（会话 Cookie 在 **HTTP 入口**缺 `Secure`/`__Host-`；HTTPS 入口实测已正确下发）。

另外，任务执行期间产品侧并发的 R-05 改动引入了一个 **P0 回归**（HTTP 入口下所有 Cookie 鉴权写请求 403 `csrf_failed`），
见 §7 与 `negative-tests/CURRENT-BUILD-FINDINGS.md`。

---

## 3. 负向用例结果（`negative-tests/result.json`）

```json
{ "suite": "run-sprint7-sec-negative.sh", "test_id": "S7-SEC-01",
  "pass": 87, "fail": 3, "not_verified": 0,
  "criteria": "fail==0 and not_verified==0 and pass>0",
  "status": "FAIL" }
```

**同一套件在两个连续构建上复现同一结果**（构建在运行期间验证稳定）：

| 轮次 | 运行时刻 (UTC) | `backend/bin/api` SHA-256 | 结果 |
|---|---|---|---|
| 1 | 2026-09-12T16:49:01Z | `753ba724…6f21c78` | PASS 72 / FAIL 10（首轮含 7 条脚本断言口误，已修正） |
| 2 | 2026-09-12T16:52:46Z | `753ba724…6f21c78` | PASS 87 / FAIL 3 / NV 0 |
| 3 | 2026-09-12T16:57:13Z | `6106d8e4…ed890a10` | **PASS 87 / FAIL 3 / NV 0**（build-before = build-after，无并发部署） |

> 说明：VM 上存在并发部署循环，API 于 23:54:21(+07) 重启（pid 2799693）。第 2 轮对应 `753ba724` 构建，
> 第 3 轮对应磁盘上当前的 `6106d8e4` 构建；两轮 FAIL 项完全一致，说明结论不依赖单次构建。
> 第 1 轮日志保留为 `negative-tests/run.prior-build-753ba724.log` 与 `result.prior-build-753ba724.json`。

**3 条 FAIL 全部是真实缺陷，不是脚本问题：**

| 检查 | 期望 | 实测 | 判定 |
|---|---|---|---|
| `AUTH-013c` | 会话 Cookie 带 `__Host-` 前缀 | 0 个 | §23.2 偏离（R-05, P1） |
| `AUTH-013d` | 会话 Cookie 带 `Secure` | 0 个 | §23.2 偏离（R-05, P1） |
| `RBAC-008h` | 创建 API Key 省略 `allowed_ips` 成功 | HTTP 500 `INTERNAL_ERROR` | DEF-01 契约缺陷（P2） |

**第 1 轮曾出现的 7 条脚本侧误判（已修正，不作为发现）**：`AUTH-010a`（inet 类型带 `/32` 掩码，改用 `host()`）、
`AUTH-012a/b`（把同源路径 `/../../etc/passwd`、`/%2F%2F…` 误判为开放重定向，已改为按「是否跨源」判定）、
`SEC-004c`（错误码实为 `RATE_LIMIT_EXCEEDED`，符合 §21.3 第 1380 行的 `RATE_LIMIT_` 前缀约定）、
`RBAC-008b`（未带 `allowed_ips` 导致拿到静态 Key，scope 未被检验；补上后 scope 拒绝实测通过）、
`IDOR-f`（fixture-viewer 本身就是 acme 两个工作区的成员，跨工作区读属合法；已改为用「仅绑定单一工作区」的临时账号构造）、
`AUTHZ-f`（撤销接口需要 `confirm:true`，且过期与撤销混用了同一会话；已拆成两个独立会话）、
`CLEANUP-b`（investigator 无 `case:delete`；案件改由 Owner 创建）。

**覆盖的 12 类负向场景（全部真实触发并断言副作用）：**
AUTH-009 伪造 `X-User-*`（5 条）· AUTH-010 伪造 `X-Forwarded-For`（4 条，含 `CF-Connecting-IP` 优先性）· AUTH-012 开放重定向（3 条 × 6 载荷）·
SEC-001 CSRF（7 条）· SEC-002 XSS/CSP（9 条）· SEC-003 SSRF（7 条）· SEC-004 限流 429+Retry-After（6 条）·
RBAC-008 API Key audience/scope（9 条）· IDOR 跨组织/跨工作区（9 条）· 未认证访问后台 10 端点（2 条）·
审计只读性 `blockchain_app`（11 条）· 认证绕过：空/篡改/过期/撤销会话（8 条）· CLEANUP 无残留（5 条）

**副作用声明（可审计）**：套件只创建并清理自己拥有的对象（1 个测试案件、1 个测试 API Key、1 个临时 Viewer）；
对 Provider 端点做过一次**可逆**写入并验证已还原（`SEC-003f`/`CLEANUP-d`）；
未改动任何既有用户数据、未改 `deploy/.env`、未重启服务。清理项全部 PASS（`CLEANUP-a..e`）。

---

## 4. 镜像 / IaC 扫描的重要限定

trivy `config` 的 misconfig 扫描器集合为 `azure-arm, cloudformation, dockerfile, helm, kubernetes, terraform, ansible`。
本仓库的 `deploy/` 下是 **systemd unit、nginx conf、compose.yaml**——**都不在该集合内**，
因此 `trivy config /w/deploy` 的原始日志明确写着 `Detected config files num=0`、
`Supported files for scanner(s) not found`，其 JSON 里没有任何 `Results`。

**因此 `trivy-config.txt` 的 "0" 是「0 个文件被扫描」，不是「0 个漏洞」。**

**这是结构性不支持，不是参数问题**（已用受控自测证明）：
- 故意写坏的临时 `Dockerfile` 与 `k8s.yaml` **能被识别**（DS-0002/0004/0026/0029/0031、KSV-* 共 19 项）；
- 故意写坏的 `compose.yaml` 与 `docker-compose.yaml` **完全不被识别**；
- checks bundle（580 个 rego）里 `compose|docker-compose|docker_compose` **0 命中**；换 `trivy-checks:1` 重跑亦然；
- checkov 3.3.17 同样 `resource_count: 0`（复制为 `docker-compose.yml` 再扫仍为 0）；
- KICS（本应支持 compose）**未跑完**（镜像拉取超时）→ 归入 NOT_VERIFIED。

因此 **IaC 配置面结论 = NOT_VERIFIED（不是 PASS）**，详见 `image-iac/SUMMARY.md`。
它同时给出了人工阅读线索（非工具结论）：4 个数据面镜像均无 `cap_drop`/`read_only`/`no-new-privileges`；
`minio`/`nats` 使用可变 tag；CSP 与 `nosniff` 只加在 `/admin/` 与 `/app-v2/` 两个 location，根路径与 `/api/` 未加；
`blockchain-vite.service` 以 `--host 0.0.0.0` 监听而其余服务均为 `127.0.0.1`；6 个 `.service` 均无 `NoNewPrivileges`/`ProtectSystem`/`PrivateTmp`。

---

## 5. 证据文件树

```text
sprint7/security/
├─ README.md                          本文件
├─ threat-matrix.md                   §23 逐信任边界 STRIDE 威胁矩阵
├─ application-account-separation.md  ADR-017 §5 实测报告 + 可执行 gate
├─ dependency-scan/                   govulncheck + pnpm audit + SUMMARY.md + 44 个原始证据
├─ sbom/                              CycloneDX/SPDX × backend/frontend + syft 版本 + 命令日志
├─ image-iac/                         4 个镜像 trivy 扫描（json+txt）+ misconfig + raw 日志
├─ negative-tests/                    result.json / run.log / files/（49 个原始请求响应与 DB 查询）
└─ secret-scan/                       scan-secrets.sh / REPORT.md / raw/（12 个文件）
```

配套可重跑脚本（与既有 Sprint 套件同目录）：
- `acceptance-evidence/dual-system-v1/tests/run-sprint7-sec-negative.sh`（本任务）
- `acceptance-evidence/dual-system-v1/tests/run-sprint7-secret-scan.sh`（AUDIT-002）

---

## 6. 未能验证 / 外部阻塞（如实声明）

| 项 | 原因 | 影响 |
|---|---|---|
| 真实 Cloudflare 边缘行为 | 本环境为本机 Nginx 模拟；无法验证生产 Cloudflare 的 header 注入与 TLS | AUTH-010 的结论按「可信代理」语义分档给出，并单列 R-01 |
| 真实 IdP / OIDC | `AUTH_OIDC_ENABLED` 未开，`/api/auth/v1/oidc/start` 返回 501 | AUTH-012c 只验证「未启用时不产生站外跳转」；OIDC 跳转逻辑未做 live 验证 |
| 依赖漏洞的运行时可利用性 | 需污点/动态分析，超出静态扫描范围 | 只证明「包被真实 import」+ govulncheck 的符号/包/模块级可达性 |
| 跨租户负向的资源类型穷尽 | 本轮覆盖案件/评论/工作区/后台端点/API Key | findings/evidence/report/canvas/download 的跨租户负向仍依赖 S5 套件 |
| 角色停用后旧请求不可写（§23.8） | 属 RBAC-006，本轮未覆盖 | 需 S3/S5 套件继续守门 |
| ClickHouse/MinIO/NATS 的账号分离 | 本轮只覆盖 PostgreSQL | 建议纳入后续 sprint |
| docker hub 直连 | `registry-1.docker.io` 不可达（TLS 超时），syft 镜像经 `docker.1ms.run` 拉取（digest 已记录）；trivy 默认漏洞库源 `mirror.gcr.io` 超时、`ghcr.io` token DENIED，DB 经 `docker.m.daocloud.io` 兜底 | SBOM 的**工具来源**可追溯；**漏洞库无法与上游逐字节校验**，不能排除镜像站同步延迟导致漏掉极新 CVE |
| IaC 配置面 | trivy/checkov 结构性不支持 compose/systemd/nginx；KICS 未跑完 | **该门禁项不得判 PASS**，需补扫后关闭 |

---

## 7. 任务执行期间产品侧的并发改动（含一个新引入的 P0）

详见 `negative-tests/CURRENT-BUILD-FINDINGS.md`。摘要：

| ID | 级别 | 内容 | 状态 |
|---|---|---|---|
| **F-01** | **P0** | **HTTP 入口（直连 `:8080`）下所有 Cookie 鉴权写请求 403 `csrf_failed`**，即使 token 完全正确；读请求正常。根因：`checkCSRF` 用 `cfg.CookieName(audience)`（无条件加 `__Host-` 前缀）读会话 Cookie，而写入端 `SetCookiesFor` 用 `CookieNameFor(audience, secure)`（HTTP 下不加前缀）→ 名字不一致。**决定性对照：把会话 Cookie 名换成 `__Host-cs_admin_session` 后同一请求 200** | 新引入的回归，**需立即修复**；会打红所有直连 8080 的既有验收套件 |
| F-02 | — | Provider 端点写入**已被拒绝**（先前报告的 **R-03** 看来已修） | 待作者确认并补单测 |
| F-03 | P3 | 跨组织**写**评论由 404 变 403，与**读**的 404 不一致（§23.4 允许两者，但不一致会泄露存在性位） | 建议统一 |
| F-04 | P2 | `admin@local.display_name` 被第三方测试写入了 XSS 载荷 `<img src=x onerror=...>`；`display_name` 服务端**未做标签剥离**（对比 `casework/cases.go:1013` `sanitizeComment`） | 需清理数据 + 统一字段净化策略 |
| F-05 | — | 环境不稳定（≥5 次重建 + API 重启），部分轮次出现 `000/502` | 权威结果取构建 `6106d8e4`；建议部署循环停止后重跑一次 |

**R-05 的正面结论**：HTTPS 入口（`X-Forwarded-Proto: https`）实测已正确下发
`__Host-cs_admin_session` + `Secure` + `HttpOnly` + `SameSite=Lax`，且该入口下写请求 200。HTTP 入口不下发前缀属 `SecureMode=auto` 的**设计行为**（但 F-01 使该档位下的写操作不可用）。
