# S7-SEC-01 依赖漏洞扫描汇总（dependency-scan）

| 项目 | 值 |
|---|---|
| 任务 | S7-SEC-01 依赖漏洞扫描证据 |
| 执行时间 | 2026-09-12 23:34 – 23:40 (+07:00) |
| 执行主机 | dataserver / 192.168.31.152（用户 euripides） |
| 项目根 | /home/euripides/projects/blockchain-platform |
| 证据根 | `acceptance-evidence/dual-system-v1/sprint7/security/dependency-scan/` |
| 扫描范围 | backend（Go module）+ frontend（pnpm workspace，根 lockfile 覆盖全部 workspace 成员） |

---

## 1. 结论：§23.8 安全发布门「P0/P1 漏洞为 0」→ **不满足（FAIL）**

`docs/dual-system-development-plan.md:1859`「P0/P1 漏洞为 0」为硬性发布门。实测 P0=2、P1=5，**门禁不通过**。

### 1.1 总数与分级计数

| 指标 | 数值 |
|---|---:|
| 漏洞记录总数（Go 12 + 前端 3） | **15** |
| **P0（CRITICAL）** | **2** |
| **P1（HIGH）** | **5** |
| P2（MODERATE） | 3 |
| P3（LOW / INFO） | 1 |
| 无公开严重级（NOT_VERIFIED） | 4 |
| 其中 govulncheck 判定「代码实际可达」 | 3（HIGH×1、MODERATE×1、LOW×1） |
| 其中前端直接依赖命中 | 3（HIGH×2、MODERATE×1） |

**严重级→优先级映射说明（重要，非文档原文）**：开发计划 §23.8 只写「P0/P1 漏洞为 0」，未给出「漏洞严重级 → P0/P1」的映射表。本报告采用通用映射 **P0=CRITICAL、P1=HIGH、P2=MODERATE、P3=LOW**（该映射为本报告显式采用的约定，非文档明文规定），并同时给出可达性修正视角（§1.2）供负责人复核。

### 1.2 可达性修正视角（supplementary，不改变 1.1 的门禁结论）

若只统计「govulncheck 判定代码实际可达」+「前端直接依赖且源码真实 import」：

| 修正后指标 | 数值 | 说明 |
|---|---:|---|
| P0 | 0 | 两个 CRITICAL 的 pgx 漏洞（GO-2026-4771/4772）govulncheck 判定为「imported package，代码未调用受影响符号」 |
| P1 | 3 | GO-2026-5960（excelize，可达）+ GHSA-4r6h-8v6p-xvw6、GHSA-5pgg-2g8v-p4x9（xlsx，前端直接依赖且被动态 import） |
| P2 | 2 | GO-2025-3770（chi，可达）+ GHSA-fgmj-fm8m-jvvx（echarts，前端直接依赖且被 import） |

即便采用可达性修正，P1 仍为 3 > 0 → **发布门依旧不满足**。

---

## 2. 工具与版本（原始证据见 `go/tool-versions.txt`、`frontend/tool-versions.txt`）

| 工具 | 版本 |
|---|---|
| Go | go1.26.7 linux/amd64（`/usr/local/go/bin/go`） |
| govulncheck | v1.8.0（`/tmp/gtools/govulncheck`，非项目工作区） |
| 漏洞库 | https://vuln.go.dev，DB updated 2026-09-10 14:48:42 +0000 UTC |
| Node | v24.20.0（nvm） |
| npm | 11.19.0 |
| pnpm | 11.24.0 |
| backend 模块 | `github.com/blockchain-platform/blockchain-platform`，`go 1.26.0` |
| frontend 包管理 | pnpm workspace，lockfileVersion '9.0'，根 lockfile 单一 importer `.` |

---

## 3. 实测命令与退出码（完整清单见 `go/command.txt`、`frontend/pnpm-audit.command.txt`）

| # | 命令 | 退出码 | 说明 |
|---|---|---:|---|
| 1 | `GOBIN=/tmp/gtools GOPROXY=https://goproxy.cn,direct /usr/local/go/bin/go install golang.org/x/vuln/cmd/govulncheck@latest` | 0 | 安装成功（主方案即成功，未使用 docker 备选） |
| 2 | `cd backend && GOPROXY=https://goproxy.cn,direct go mod download` | 0 | 预热模块缓存，未改 go.mod/go.sum |
| 3 | `cd backend && GOPROXY=https://goproxy.cn,direct /tmp/gtools/govulncheck ./...` | **3** | 3 = 发现漏洞（govulncheck 约定，非命令失败） |
| 4 | `cd backend && GOPROXY=https://goproxy.cn,direct /tmp/gtools/govulncheck -show verbose ./...` | **3** | 同上，含可达性证据 |
| 5 | `cd frontend && CI=true pnpm audit --json` | **1** | 1 = 发现漏洞（pnpm 约定），stderr 为空（0 字节） |
| 6 | `md5sum go.mod go.sum`（扫描前后各一次） | 0 | 前后完全一致（`60ec9012…` / `f2d26700…`） |
| 7 | `md5sum pnpm-lock.yaml`（审计前后各一次） | 0 | 前后完全一致（`5d3380128e266db079a91119e0201ffb`，size 54095，mtime 未变） |

**未修改任何 lockfile / go.mod / go.sum / 源码**：三方指纹前后逐字节一致（见 `go/go-mod-fingerprint.txt`、`frontend/lockfile-fingerprint.txt`）。

---

## 4. Go 依赖漏洞明细（backend，govulncheck v1.8.0）

govulncheck 输出分三段：`=== Symbol Results ===`（**代码实际可达**，3 条）、`=== Package Results ===`（导入了包但未调用受影响符号，5 条）、`=== Module Results ===`（依赖了模块但未导入包，4 条）。

| # | 漏洞 ID | 别名（CVE / GHSA） | 包 | 当前版本 | 修复版本 | 严重级（来源） | 可达性 | 处置结论 |
|---|---|---|---|---|---|---|---|---|
| 1 | GO-2026-5960 | CVE-2026-54063 / GHSA-h69g-9hx6-f3v4 | github.com/xuri/excelize/v2 | v2.9.0 | v2.11.0 | **HIGH**（GHSA 发布值；CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H） | **可达（YES）** `internal/api/export.go:414` 等 4 条 trace | **升级** excelize → ≥ v2.11.0（P1，必修） |
| 2 | GO-2026-5004 | CVE-2026-41889 / GHSA-j88v-2chj-qfwx | github.com/jackc/pgx/v5 | v5.7.4 | v5.9.2 | LOW（GHSA 发布值；CVSS:4.0/AV:N/AC:H/AT:P/PR:L/UI:N/VC:N/VI:L/VA:N/SC:N/SI:N/SA:N） | **可达（YES）** `migrations/migrations.go:57:23` → `pgxpool.Conn.QueryRow` → `sanitize.SanitizeSQL` | **升级** pgx → ≥ v5.9.2（与 #7/#8 同一次升级一并解决） |
| 3 | GO-2025-3770 | CVE-2025-71405 / GHSA-vrw8-fxc6-2r93 | github.com/go-chi/chi/v5 | v5.2.1 | v5.2.2 | MODERATE（GHSA 发布值；CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:A/VC:L/VI:L/VA:N/SC:N/SI:N/SA:N） | **可达（YES）** `internal/api/server.go:274` 等 42 条 trace | **升级** chi → ≥ v5.3.0（覆盖 #3/#4/#5/#6） |
| 4 | GO-2026-5777 | CVE-2026-72816 / GHSA-rjr7-jggh-pgcp | github.com/go-chi/chi/v5 | v5.2.1 | v5.3.0 | **HIGH**（GHSA 发布值；CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:H/VA:N/SC:N/SI:N/SA:N/E:P） | 不可达（Package Results） | **升级** chi → ≥ v5.3.0（P1，含在 chi 升级内） |
| 5 | GO-2026-5775 | CVE-2026-72817 / GHSA-9g5q-2w5x-hmxf | github.com/go-chi/chi/v5 | v5.2.1 | v5.3.0 | **HIGH**（GHSA 发布值；CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:H/VA:N/SC:N/SI:N/SA:N/E:P） | 不可达（Package Results） | **升级** chi → ≥ v5.3.0（P1，含在 chi 升级内） |
| 6 | GO-2026-5774 | CVE-2026-72815 / GHSA-3fxj-6jh8-hvhx | github.com/go-chi/chi/v5 | v5.2.1 | v5.3.0 | MODERATE（GHSA 发布值；CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:L/VA:N/SC:N/SI:N/SA:N） | 不可达（Package Results） | 升级 chi → ≥ v5.3.0（含在 chi 升级内） |
| 7 | GO-2026-4772 | CVE-2026-33816 / GHSA-9jj7-4m8r-rfcm | github.com/jackc/pgx/v5 | v5.7.4 | v5.9.0 | **CRITICAL**（GHSA 发布值；CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H） | 不可达（Package Results，未调用受影响符号） | **升级** pgx → ≥ v5.9.2（P0；同一次升级同时清除 #2） |
| 8 | GO-2026-4771 | CVE-2026-33815 / GHSA-xgrm-4fwx-7qm8 | github.com/jackc/pgx/v5 | v5.7.4 | v5.9.0 | **CRITICAL**（GHSA 发布值；CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H） | 不可达（Package Results，未调用受影响符号） | **升级** pgx → ≥ v5.9.2（P0；同一次升级同时清除 #2） |
| 9 | GO-2026-6355 | CVE-2026-56855（无 GHSA） | golang.org/x/crypto | v0.54.0 | v0.56.0 | 源未发布定性等级；CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H（CVE 记录） | 不可达（Module Results） | 升级 x/crypto → ≥ v0.56.0（低成本，建议随批次升级） |
| 10 | GO-2026-6354 | CVE-2026-78662（无 GHSA） | golang.org/x/crypto | v0.54.0 | v0.56.0 | 源未发布定性等级；CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:N/A:N（CVE 记录） | 不可达（Module Results） | 升级 x/crypto → ≥ v0.56.0（低成本，建议随批次升级） |
| 11 | GO-2026-6303 | CVE-2026-56854（无 GHSA） | golang.org/x/crypto | v0.54.0 | v0.55.0 | **NOT_VERIFIED** —— api.osv.dev 对该 GO ID 与 CVE 均未返回 severity/database_specific.severity | 不可达（Module Results） | 升级 x/crypto → ≥ v0.56.0（自然覆盖） |
| 12 | GO-2026-5932 | 无别名 | golang.org/x/crypto（openpgp 子包） | v0.54.0 | N/A（无修复） | **NOT_VERIFIED** —— 官方未发布严重级（「unmaintained, unsafe by design」） | 不可达（Module Results，未导入 openpgp 包） | 接受风险：openpgp 未被导入；如未来引入需替换实现 |

govulncheck 原文结论：`Your code is affected by 3 vulnerabilities from 3 modules.`（另 5 条在导入包、4 条在依赖模块中，但代码未调用）。

### 4.1 Go 侧修复批次（4 次依赖升级可清零全部可达项）

| 升级 | 版本变更 | 消除条目 |
|---|---|---|
| pgx | v5.7.4 → **≥ v5.9.2** | #2、#7、#8（含 2×P0） |
| chi | v5.2.1 → **≥ v5.3.0** | #3、#4、#5、#6 |
| excelize | v2.9.0 → **≥ v2.11.0** | #1（P1，且为唯一可达的 HIGH） |
| golang.org/x/crypto | v0.54.0 → **≥ v0.56.0** | #9、#10、#11（#12 无修复，未被导入） |

---

## 5. 前端依赖漏洞明细（frontend，pnpm audit --json）

`pnpm audit` 元数据：dependencies 58 / devDependencies 117 / optionalDependencies 53 / totalDependencies 175；`metadata.vulnerabilities = {info:0, low:0, moderate:1, high:2, critical:0}`。

| # | 通告 ID | 包 | 当前版本 | 修复版本 | 严重级 | 直接依赖路径 | 可达性 | 处置结论 |
|---|---|---|---|---|---|---|---|---|
| 1 | GHSA-4r6h-8v6p-xvw6（Prototype Pollution in sheetJS，CWE-1321） | xlsx | 0.18.5 | `<0.19.3` 受影响；`patched_versions: null`（npm 上不存在修复版） | **HIGH** | **直接依赖** `paths: [".>xlsx"]`（frontend/package.json `"xlsx": "^0.18.5"`） | 源码真实使用：`src/utils/detail-table.ts:115`、`src/pages/DownloadPage.tsx:90` `await import('xlsx')` | **待跟踪/需替换** —— 见 §5.1，npm 无可用修复版 |
| 2 | GHSA-5pgg-2g8v-p4x9（SheetJS ReDoS，CWE-1333） | xlsx | 0.18.5 | `<0.20.2` 受影响；`patched_versions: null` | **HIGH** | **直接依赖** `paths: [".>xlsx"]` | 同上（同一包） | **待跟踪/需替换** —— 见 §5.1 |
| 3 | GHSA-fgmj-fm8m-jvvx（Apache ECharts XSS，CWE-79） | echarts | 5.6.0 | `>=6.1.0` | MODERATE | **直接依赖** `paths: [".>echarts"]`（frontend/package.json `"echarts": "5.6.0"`） | 源码真实使用：`src/features/api-control/TrendChart.tsx:2-5`（`echarts/core`、`echarts/charts`、`echarts/components`、`echarts/renderers`） | **升级** echarts 5.6.0 → ≥ 6.1.0（大版本升级，需回归图表用例） |

### 5.1 xlsx 的可修复性（关键风险，实测 npm registry）

| 检查 | 实测结果 | 退出码 |
|---|---|---:|
| `npm view xlsx dist-tags` | `{ latest: '0.18.5' }` | 0 |
| `npm view xlsx versions`（末位） | 最高版本即 `0.18.5` | 0 |
| `npm view xlsx@0.19.3 version` | **404 No match found for version 0.19.3** | 1 |
| `npm view xlsx@0.20.2 version` | **404 No match found for version 0.20.2** | 1 |

结论：npm 上 `xlsx` 已停止发布（latest 冻结在 0.18.5），两条 HIGH 通告的修复版本 **0.19.3 / 0.20.2 在 npm registry 上不存在**（SheetJS 已迁移至自建 CDN 分发）。因此 **不存在可通过 npm 完成的最小修复**；可选路径：(a) 迁移到 `exceljs` 等维护中的替代库；(b) 从 SheetJS 官方 CDN 采购/获取并锁定 ≥0.20.2 的 tarball 并纳入完整性校验（需供应链审批）；(c) 沿用 0.18.5 并做风险接受（记录在案，需负责人签字）。本报告不擅自给出实施动作。

### 5.2 审计覆盖范围证明

- `find` 递归（排除 node_modules）显示 frontend 下**只有一个 lockfile**：`./pnpm-lock.yaml`；
- `pnpm-lock.yaml` 的 `importers:` 段仅有一个 importer `.`；
- 5 个 workspace 成员 `apps/admin-console`、`apps/user-console`、`packages/api-client`、`packages/design-tokens`、`packages/ui` 的 package.json **均无 dependencies/devDependencies**；
- `xlsx` / `echarts` 声明位置全仓库仅出现在 `./package.json`。

⇒ 单次根目录 `pnpm audit` 覆盖前端 100% 依赖，无遗漏 importer。

---

## 6. 未验证项与阻塞（NOT_VERIFIED）

| 项 | 状态 | 原因 / 外部阻塞 |
|---|---|---|
| GO-2026-6303 的定性严重级 | **NOT_VERIFIED** | api.osv.dev 对 `GO-2026-6303` 与别名 `CVE-2026-56854` 均未返回 severity / database_specific.severity（无可引用发布值，拒绝推算） |
| GO-2026-5932 的严重级 | **NOT_VERIFIED** | 官方仅描述为 unmaintained/unsafe by design，未发布 CVSS 或严重级 |
| GO-2026-6354 / GO-2026-6355 的定性等级 | 部分 | 仅有 CVE 记录中的 CVSS 3.1 向量，未发布定性等级；本报告按原文列出向量，不自行换算等级 |
| 「P0/P1 漏洞」的官方映射表 | **NOT_VERIFIED** | `docs/dual-system-development-plan.md:1859` 只规定「P0/P1 漏洞为 0」，文档内无可引用的「漏洞严重级→P0/P1」映射表；本报告采用通用映射并显式标注 |
| Go 构建产物/容器镜像内依赖 | 不在本次范围 | 本任务仅覆盖源码依赖树；镜像层与 SBOM 由同 sprint 的 image-iac / sbom 证据项负责 |
| 前端运行时是否存在可利用触发路径（XSS/原型污染是否可被外部输入触达） | **NOT_VERIFIED** | 需要动态/污点分析，超出静态依赖扫描范围；本次仅证明「包被真实 import」 |

无命令级阻塞：安装 govulncheck 主方案成功，未触发 docker 备选方案。

---

## 7. 证据文件清单（全部位于 $SEC/dependency-scan/，`$SEC = acceptance-evidence/dual-system-v1/sprint7/security`）

### go/

| 文件 | 内容 |
|---|---|
| `go/govulncheck.txt` | govulncheck 默认模式原始输出（含 command / started_at / exit_code=3 / finished_at 头尾标注） |
| `go/govulncheck-verbose.txt` | `govulncheck -show verbose ./...` 原始输出，281 行，含 Symbol / Package / Module 三段可达性证据 |
| `go/tool-versions.txt` | go version、govulncheck -version、go env、go.mod 头部 |
| `go/command.txt` | 全部命令、参数、退出码、备注 |
| `go/go-mod-fingerprint.txt` | go.mod / go.sum 扫描前后 md5 对比（一致） |
| `go/go-mod-download.txt` | `go mod download` 原始输出与退出码 |
| `go/install-govulncheck.txt` | govulncheck 安装过程原始输出 |
| `go/vuln-table.txt` | 由 verbose 输出解析出的 12 条结构化漏洞表 |
| `go/osv/*.json` | 12 个 GO ID 的 OSV 原始记录 + 别名（GHSA/CVE）原始记录 |
| `go/osv/severity-map.json` | 每条 GO ID → 严重级来源与原始 CVSS 向量 |
| `go/osv/fetch-log.txt`、`go/osv/alias-fetch-log.txt` | OSV 抓取 HTTP 状态与解析日志 |

### frontend/

| 文件 | 内容 |
|---|---|
| `frontend/pnpm-audit.json` | `pnpm audit --json` 原始 JSON（2165 字节，3 条 advisories + metadata） |
| `frontend/pnpm-audit.stderr.txt` | stderr 原始输出（**0 字节，审计无 stderr 输出**） |
| `frontend/pnpm-audit.command.txt` | 命令行、起止时间、退出码=1 |
| `frontend/lockfile-fingerprint.txt` | pnpm-lock.yaml 审计前后 md5/size/mtime 对比，判定 IDENTICAL |
| `frontend/tool-versions.txt` | node/npm/pnpm 版本、lockfile 清单、pnpm-workspace.yaml、package.json |
| `frontend/workspace-coverage.txt` | 审计覆盖范围证明（lockfile/importers/workspace 成员/直接依赖声明位置） |
| `frontend/direct-usage.txt` | xlsx / echarts 在前端源码中的真实 import 位置 |
| `frontend/remediation-availability.txt` | npm registry 实测：修复版本是否存在 |

### 根

| 文件 | 内容 |
|---|---|
| `SUMMARY.md` | 本汇总文件 |

---

## 8. 复现方式（逐字节可重放）

```bash
SEC=/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security
export PATH=/usr/local/go/bin:/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH
export GOPROXY=https://goproxy.cn,direct GOTOOLCHAIN=local GOBIN=/tmp/gtools

# Go
go install golang.org/x/vuln/cmd/govulncheck@latest
cd /home/euripides/projects/blockchain-platform/backend
go mod download
/tmp/gtools/govulncheck ./...                 # exit 3 = vulns found
/tmp/gtools/govulncheck -show verbose ./...   # exit 3 = vulns found

# Frontend（CI=true 使 pnpm 保持 frozen-lockfile 语义，保证不写 lockfile）
cd /home/euripides/projects/blockchain-platform/frontend
md5sum pnpm-lock.yaml
CI=true pnpm audit --json                     # exit 1 = vulns found
md5sum pnpm-lock.yaml                         # 必须与上一次一致
```

---

## 9. 对发布门的直接建议（供负责人决策，本报告不擅自实施）

1. **P0 阻断项**：后端 `github.com/jackc/pgx/v5` v5.7.4 → ≥ v5.9.2 可一次清除 2 个 CRITICAL（GO-2026-4771/4772）+ 1 个可达 LOW（GO-2026-5004），成本低、无 API 破坏性变更预期。
2. **P1 阻断项**：`chi/v5` → ≥ v5.3.0（清 3 个 HIGH + 1 MODERATE）；`excelize/v2` → ≥ v2.11.0（清唯一可达 HIGH）。
3. **P1 无法用升级解决**：前端 `xlsx@0.18.5` 两条 HIGH 在 npm 上无修复版本，必须走「替换库 / 官方 CDN 分发 + 完整性校验 / 正式风险接受」三者之一，需供应链审批。
4. 前端 `echarts` 5.6.0 → ≥ 6.1.0 为 MODERATE，随批次升级并回归 `TrendChart`。
5. 以上全部落地并复跑本目录脚本后，方可宣称 §23.8「P0/P1 漏洞为 0」。**当前状态：不满足。**

