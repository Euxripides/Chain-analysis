# 依赖清单与漏洞报告 — Sprint 7 安全发布门（§23.8 / §27.6 / §31）

- 证据目录：`acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z/`
- 采集时间（UTC）：2026-09-12T22:32Z – 2026-09-12T22:52Z
- 采集方式：**只读**。未修改任何 package.json / go.mod / 源码；未安装或升级任何依赖；未改 .npmrc / lockfile。
- 配套机器可读汇总：`dependency-inventory.json`

---

## 0. 结论速览

| 维度 | 实测值 | 判定 |
|---|---|---|
| **P0（CRITICAL）** | **2** | 门禁要求 0 → **FAIL** |
| **P1（HIGH）** | **5** | 门禁要求 0 → **FAIL** |
| P2（MODERATE） | 3 | — |
| P3（LOW） | 1 | — |
| 未评级（UNKNOWN，无公开严重度） | 4 | 不计入 P0/P1，但已如实列出 |
| 合计去重漏洞 | 15 | — |

> **P0 与 P1 分开计数，从未合并后宣称 P0=0。** P0=2 全部来自后端；P1=5 = 后端 3 + 前端 2。

### 0.1 优先级映射的来源（必须声明）

`docs/dual-system-development-plan.md:1859` 只写「P0/P1 漏洞为 0」，**文档内没有**「漏洞严重级 → P0/P1」的映射表。
本报告**显式采用**通用映射，并将其标注为**报告约定而非计划原文**：

| 优先级 | 严重级 |
|---|---|
| P0 | CRITICAL |
| P1 | HIGH |
| P2 | MODERATE |
| P3 | LOW |

### 0.2 可达性修正视角（供发布负责人复核，不替代上面的原始计数）

| 视角 | P0 | P1 | 门禁 |
|---|---|---|---|
| 原始严重级计数 | 2 | 5 | FAIL |
| 仅计 govulncheck 判定「代码实际可达（Symbol Results）」的后端项，前端 xlsx 仍计入 | 0 | 3 | **仍 FAIL** |

两个视角下门禁**都不通过**。

---

## 1. 前端（pnpm workspace）

### 1.1 扫描溯源

| 项 | 值 |
|---|---|
| 扫描器 | `pnpm audit` |
| 版本 | pnpm 11.24.0 / node v24.20.0 |
| 执行主机 | VM dataserver（部署机），cwd = `/home/euripides/projects/blockchain-platform/frontend` |
| registry | `https://registry.npmjs.org`（**仅本条命令**用 `--registry=` 覆盖；未改任何 `.npmrc`） |
| 命令 | `CI=true pnpm audit --json --registry=https://registry.npmjs.org` |
| **真实退出码** | **1** |
| 退出码含义 | 1 = **发现漏洞**，不是执行失败。产物是合法 advisory JSON（2165 字节） |
| 结束时间 UTC | 2026-09-12T22:42:36Z |
| stdout | `deps/official-registry-pnpm-audit.json` |
| stderr | `deps/official-registry-pnpm-audit.stderr.txt`（0 字节） |
| lockfile sha256（扫描前后一致） | `a11d5a0ec48ef455271427d048a066dc99a2b607f71beaa170188b22766a94b5` |

**扫描前后 `pnpm-lock.yaml` 与 `package.json` 的 sha256 完全一致 → 证明本次审计未改动依赖定义。**

### 1.2 失败的第一次尝试（原样保留，不得当作漏洞结果）

| 项 | 值 |
|---|---|
| 产物 | `deps/windows-host/pnpm-audit.json`、`deps/windows-attempt1-npmmirror-FAILED-pnpm-audit.json` |
| 字节数 / sha256 | 192 / `4b7bccb843bdbb045a531327caa65951450dbaa041611ae82f3e928fedccf9d6` |
| 内容 | `ERR_PNPM_AUDIT_ENDPOINT_NOT_EXISTS`（`registry.npmmirror.com` 不提供 `-/npm/v1/security/advisories/bulk`） |
| 处置 | **原样保留为失败证据**。这不是漏洞清单，其中的 0 不代表 0 漏洞。 |

根因是 Windows 主机的 pnpm 解析到 npmmirror 镜像，该镜像不实现 npm audit 批量公告接口。改用官方 registry 后正常返回。

### 1.3 依赖规模

| 项 | 数量 |
|---|---|
| dependencies | 58 |
| devDependencies | 117 |
| optionalDependencies | 53 |
| **totalDependencies** | **175** |

pnpm audit 自带严重级统计：`info 0 / low 0 / moderate 1 / high 2 / critical 0`。

### 1.4 逐条漏洞

| # | 包 | 版本 | 严重度 | 优先级 | 修复版本 | Advisory | 说明 |
|---|---|---|---|---|---|---|---|
| 1 | `xlsx` | 0.18.5 | HIGH | **P1** | **无** | [GHSA-4r6h-8v6p-xvw6](https://github.com/advisories/GHSA-4r6h-8v6p-xvw6) | Prototype Pollution（CWE-1321），影响 `<0.19.3` |
| 2 | `xlsx` | 0.18.5 | HIGH | **P1** | **无** | [GHSA-5pgg-2g8v-p4x9](https://github.com/advisories/GHSA-5pgg-2g8v-p4x9) | ReDoS（CWE-1333），影响 `<0.20.2` |
| 3 | `echarts` | 5.6.0 | MODERATE | P2 | `>=6.1.0` | [GHSA-fgmj-fm8m-jvvx](https://github.com/advisories/GHSA-fgmj-fm8m-jvvx) | XSS（CWE-79），影响 `<6.1.0` |

**关于 xlsx：npm 上没有修复版本。** 依据是审计输出中两条公告同时给出
`"patched_versions": null` 与 `"patched_versions_unpublished": true`。
因此这两条 **无法** 通过「升级依赖」闭合。

> **状态：OPEN（未关闭，未豁免）。** 仅仅出具一份书面风险接受**不能**让这两条消失，
> **不得**据此宣称 P1=0。

真正关闭必须同时具备：

1. **替换掉 `xlsx`**（改用其他表格库），**或**改用 SheetJS 官方发行包（须给出**来源渠道**）；
2. **完整性校验**：对替换后的产物做 hash 和/或签名校验；
3. **真实的导出功能回归**：必须跑**真实**的 CSV / XLSX / JSONL 导出用例，证明导出路径仍然可用；
4. **复扫**：重新扫描后这两条公告确实消失。

另需强调：**工具退出码为 0、以及「上游没有修复版本」，都不等于安全门槛通过。**

---

## 2. 后端（Go modules）

### 2.1 扫描溯源

| 项 | 值 |
|---|---|
| 扫描器 | `govulncheck` |
| 扫描器版本 | **v1.8.0** |
| 漏洞库 | `https://vuln.go.dev`，**db_last_modified = 2026-09-10T14:48:42Z** |
| Go 工具链 | go1.26.7 linux/amd64 |
| scan_level / scan_mode | `symbol` / `source` |
| 模块数 | 159（`go list -m all`，exit 0） |

命令与真实退出码：

```text
/usr/local/go/bin/go list -m all                          exit 0   159 modules
/tmp/gtools/govulncheck -version                          exit 0   govulncheck@v1.8.0
/tmp/gtools/govulncheck ./...                             exit 3   (3 = 发现漏洞)
/tmp/gtools/govulncheck -show verbose ./...               exit 3
/tmp/gtools/govulncheck -format json ./...                exit 0   731940 bytes
```

> **这是真实扫描产出，不是安装日志。** `deps/govulncheck.json` 为 731,940 字节的机器可读结果流，含 `config` / `SBOM`（56 个模块）/ 241 条 `osv` 记录 / 68 条 `finding` 记录。
> `exit 0` 出现在 `-format json` 那次调用上，是因为该调用正常产出结果流；`exit 3` 是 govulncheck 对「发现漏洞」的标准退出码。

govulncheck 自述结论（原文）：

```text
Your code is affected by 3 vulnerabilities from 3 modules. This scan also found 5
vulnerabilities in packages you import and 4 vulnerabilities in modules you require,
but your code doesn't appear to call these vulnerabilities.
```

### 2.2 严重度的取得方式（关键方法说明）

`vuln.go.dev` 的 Go 记录**不含严重度字段**（只有 `review_status`）。
因此本报告逐条取 GHSA 别名，向 `https://api.osv.dev/v1/vulns/<GHSA-ID>` 取回权威严重度（OSV 镜像 GHSA 的 rating）。

- 8 条取到严重度；
- **4 条 `golang.org/x/crypto` 公告既无 GHSA 别名、OSV 也不提供严重度 → 记为 `UNKNOWN`，绝不用推测值填充。**

原始记录留存：`deps/osv/*.json`（Go 记录）、`deps/osv-ghsa/*.json`（GHSA 记录）、`deps/osv-severity-table.json`（汇总）。

### 2.3 逐条漏洞（12 条去重）

| # | OSV | 严重度 | 优先级 | 模块 | 当前版本 | 修复版本 | 可达性 | 别名 |
|---|---|---|---|---|---|---|---|---|
| 1 | GO-2026-4771 | **CRITICAL** | **P0** | `github.com/jackc/pgx/v5` | v5.7.4 | 5.9.0 | package | CVE-2026-33815 |
| 2 | GO-2026-4772 | **CRITICAL** | **P0** | `github.com/jackc/pgx/v5` | v5.7.4 | 5.9.0 | package | CVE-2026-33816 |
| 3 | GO-2026-5960 | **HIGH** | **P1** | `github.com/xuri/excelize/v2` | v2.9.0 | v2.11.0 | **symbol（可达）** | CVE-2026-54063 |
| 4 | GO-2026-5775 | **HIGH** | **P1** | `github.com/go-chi/chi/v5` | v5.2.1 | 5.3.0 | package | CVE-2026-72817 |
| 5 | GO-2026-5777 | **HIGH** | **P1** | `github.com/go-chi/chi/v5` | v5.2.1 | 5.3.0 | package | CVE-2026-72816 |
| 6 | GO-2025-3770 | MODERATE | P2 | `github.com/go-chi/chi/v5` | v5.2.1 | 5.2.2 | **symbol（可达）** | CVE-2025-71405 |
| 7 | GO-2026-5774 | MODERATE | P2 | `github.com/go-chi/chi/v5` | v5.2.1 | 5.3.0 | package | CVE-2026-72815 |
| 8 | GO-2026-5004 | LOW | P3 | `github.com/jackc/pgx` | v5.7.4 | 5.9.2 | **symbol（可达）** | CVE-2026-41889 |
| 9 | GO-2026-5932 | UNKNOWN | — | `golang.org/x/crypto` | v0.54.0 | 无 | module | 无 |
| 10 | GO-2026-6303 | UNKNOWN | — | `golang.org/x/crypto` | v0.54.0 | 0.55.0 | module | CVE-2026-56854 |
| 11 | GO-2026-6354 | UNKNOWN | — | `golang.org/x/crypto` | v0.54.0 | 0.56.0 | module | CVE-2026-78662 |
| 12 | GO-2026-6355 | UNKNOWN | — | `golang.org/x/crypto` | v0.54.0 | 0.56.0 | module | CVE-2026-56855 |

可达性含义（govulncheck 定义）：`symbol` = 代码中确实调用到受影响符号；`package` = 导入了受影响包但未调用该符号；`module` = 仅在依赖模块中，未导入相关包。

可达性示例（原文摘录）：

```text
GO-2026-5960  internal/api/export.go:442:26: api.writeXLSXSheet calls excelize.File.NewSheet
GO-2026-5004  migrations/migrations.go:57:23: migrations.ApplyPostgres calls pgxpool.Conn.QueryRow,
              which eventually calls sanitize.SanitizeSQL
```

### 2.4 主机一致性

| 文件 | Windows 工作树 sha256 | VM 部署目录 sha256 |
|---|---|---|
| `backend/go.sum` | `34147fb9944ea49b8470e4c0e0da85eb53ce60706975b2d5fe34c2011ee4b6b2` | 同左（**一致**） |
| `backend/go.mod` | `0091b9f5…ef3465` | `4e0f98fa…44c36c`（**不同**） |

`go.mod` 差异经逐行比对**只有一处**：Windows 侧 `github.com/prometheus/client_model v0.6.2` 缺 `// indirect` 注释，**版本完全相同**，不构成依赖集差异。

---

## 3. 要闭合还差什么

| # | 动作 | 影响 | 成本 |
|---|---|---|---|
| 1 | 后端 `pgx/v5` v5.7.4 → **≥ v5.9.2** | 清 2×P0（CRITICAL）+ 1×P3 | 低（预期无破坏性 API 变更） |
| 2 | 后端 `chi/v5` v5.2.1 → **≥ v5.3.0** | 清 3×P1（5775/5777/5774；5774 为 P2） | 低 |
| 3 | 后端 `xuri/excelize/v2` v2.9.0 → **≥ v2.11.0** | 清唯一**可达**的 P1 | 低 |
| 4 | 前端 `xlsx@0.18.5` — **npm 无修复版本** | 2×P1 **无法通过升级闭合**；**状态 OPEN，未豁免** | 高：换库或官方发行包 + 来源/完整性校验 + **真实 CSV/XLSX/JSONL 导出回归** + 复扫 |
| 5 | 前端 `echarts` 5.6.0 → **≥6.1.0** | 清 P2 | 中（主版本升级，需回归图表） |
| 6 | 补齐 4 条 x/crypto 公告的人工 CVSS 评级 | 消除 UNKNOWN | 低 |
| 6b | **不得用风险接受关闭 xlsx 两条 HIGH** | 保持 OPEN，直至代码实际变更且导出回归通过 | — |
| 7 | 落地后**复跑本目录命令** | 才能宣称 §23.8 通过 | 低 |

**当前状态：§23.8「P0/P1 漏洞为 0」不满足。**

---

## 4. 附件（全部为本轮真实产物）

| 文件 | 说明 |
|---|---|
| `deps/official-registry-pnpm-audit.json` | 官方 registry 审计结果（权威） |
| `deps/official-registry-pnpm-audit.txt` | 同上，表格形式 |
| `deps/official-registry-audit.log` | 含退出码/时间/registry/sha256 的完整溯源日志 |
| `deps/official-registry-pnpm-audit-prod.json` | 仅生产依赖视图 |
| `deps/windows-host/*` | Windows 主机首次失败尝试（原样保留） |
| `deps/govulncheck.json` | govulncheck 机器可读结果（731,940 字节） |
| `deps/govulncheck.txt` | govulncheck 文本结果 |
| `deps/govulncheck-verbose.txt` | 含全部 trace 的详细结果 |
| `deps/govulncheck-parsed.json` | 本报告的解析产物（去重 12 条 + 可达性分类） |
| `deps/osv-severity-table.json` | 严重度解析表与其来源 |
| `deps/osv/`、`deps/osv-ghsa/` | 原始 OSV / GHSA 记录 |
| `deps/go-list-m-all.txt` / `.json` | 159 个模块清单 |
| `deps/vm-deps-run.log` | VM 侧完整命令日志（含 PRE/POST 完整性指纹） |
| `deps/windows-host/commands.log` | Windows 侧命令日志（含 PRE/POST 完整性指纹） |
