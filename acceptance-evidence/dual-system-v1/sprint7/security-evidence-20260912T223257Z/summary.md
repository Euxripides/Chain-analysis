# Sprint 7 安全 / 供应链证据汇总

| 项 | 值 |
|---|---|
| 证据 ID | `security-evidence-20260912T223257Z` |
| 采集窗口（UTC） | 2026-09-12T22:32:57Z – **23:00:12Z**（实际最后一次采集命令完成时刻，非预填预计值） |
| 依据 | `docs/dual-system-development-plan.md` §23.8 / §27.6 / §29.1 / §31，`docs/acceptance-audit.md` 第 7 条 |
| 采集方式 | **只读**；未改源码、未升级依赖、未部署、未重启服务 |
| **总体判定** | **FAIL** |
| **是否等于发布门通过** | **否 —— `not_a_clearance: true`** |

> 本报告交付的是**可复现的原始测量**。**扫描报告交付 ≠ P0/P1 清零**。
> 报告中的严重度计数**按实测如实呈现，未向任何目标值调整**，也不构成风险接受。

---

## 1. 逐维度判定

| 维度 | 判定 | 一句话结论 |
|---|---|---|
| 依赖漏洞 | **FAIL** | **P0=2、P1=5（分开计数）**；门禁要求 0 |
| SBOM | PASS（有保留） | syft 1.51.1；前端 176 组件；**后端宽目录 SBOM 被历史备份污染（10,003 组件）→ 已另出仅含当前生产产物的修正版（190 组件）** |
| 容器镜像 | **FAIL** | compose **未 pin 任何 digest**，且本机**无可引用的 manifest digest**；20 CRITICAL / 203 HIGH |
| IaC 覆盖 | **PARTIAL_FAIL** | checkov 与 trivy config **各扫到 0 个文件**；KICS 真实覆盖 compose（10 MEDIUM）；systemd / nginx **无任何自动化覆盖** |
| 秘密扫描 | **FAIL** | 日志 **0 命中（PASS）**；但 **7 个生产秘密内嵌于 3 个发布二进制**，且生产库口令在 git 跟踪文件里 |

---

## 2. 实测关键数字（全部来自本轮真实执行的命令）

| 指标 | 实测值 |
|---|---|
| 依赖 **P0（CRITICAL）** | **2** |
| 依赖 **P1（HIGH）** | **5** |
| 依赖 P2（MODERATE） | 3 |
| 依赖 P3（LOW） | 1 |
| 依赖未评级（无公开严重度） | 4 |
| 前端 `xlsx` 无 npm 修复版本的公告数 | 2 |
| 在范围镜像 **CRITICAL** | **20** |
| 在范围镜像 **HIGH** | **203** |
| 在范围镜像总命中 | 763（含 331 MEDIUM / 201 LOW / 8 UNKNOWN） |
| 无上游修复的镜像命中 | 83 |
| compose 中 `@sha256` pin 数 | **0** |
| 有可引用 manifest digest 的镜像数 | **0** |
| 使用浮动 `latest` 的镜像数 | **1**（`minio/minio:latest`） |
| **内嵌于发布二进制的生产秘密数** | **7** |
| 运行日志秘密命中 | **0** |
| 已扫描日志行数 | 191,904（69 个来源 / 30 MB） |
| checkov 实际扫描的 IaC 文件数 | **0** |
| trivy config 实际扫描的 IaC 文件数 | **0** |
| KICS 实际解析的 IaC 文件数 | **1**（compose.yaml） |
| KICS 在 compose 上的 MEDIUM | 10 |
| 有沙箱加固的 systemd 单元 | **0 / 8** |
| 宿主上已安装的 systemd 单元 | **0** |

### 2.1 优先级映射的来源（必须声明）

§23.8 只写「P0/P1 漏洞为 0」，**文档内没有严重级→P0/P1 的映射表**。
本报告显式采用 **P0=CRITICAL、P1=HIGH、P2=MODERATE、P3=LOW**，并标注为**报告约定而非计划原文**。
**P0 与 P1 分开计数，从未合并后宣称 P0=0。**

可达性修正视角（供发布负责人复核，不替代原始计数）：P0=0、P1=3 → **仍然 FAIL**。

---

## 3. 五个必须知道的发现

### 3.1 ❌ 生产库口令被编译进发布二进制

`POSTGRES_PASSWORD`、`CLICKHOUSE_PASSWORD`、`MINIO_ROOT_PASSWORD` 等 **7 个生产秘密**
以字节形式存在于 **`backend/bin/api`、`backend/bin/worker`、`backend/bin/migrate`** 三个发布产物中，
其中一项 90 字符 DSN 还内嵌了 43 字符的 `blockchain_api` 应用账号口令。
（`secret/build-product-secret-check.json`，值为 sha256，未输出明文）

### 3.2 ❌ 同一批口令还写在 **git 跟踪**的 `deploy/.env.example`

`.gitignore` 已忽略 `.env`，真实 `.env` 未被提交 —— 但**同样的口令被复制进了会被提交的 `.env.example`**，绕过了该防线。

### 3.3 ❌ 镜像不可变性与发布证据要求不满足（§29.1）

- `deploy/compose.yaml` 全部以 **tag** 引用（L3 / L20 / L43 / L56），**`@sha256` 出现 0 次**；
- 本机 **4/4 镜像的 `RepoDigests` 与其 config digest 逐字节相同** → **不存在可引用的 registry manifest digest**；
- `docker manifest inspect` 在本环境失败。

**结论：任何 `docker compose pull` 或重新部署都可能静默换入新内容。**
另外 `minio/minio:latest` 是浮动的 `latest`，明确违反 §29.1。

### 3.4 ⚠ 「扫描 0 个文件」被如实区分于「0 个问题」

checkov `resource_count: 0`、trivy `Detected config files num=0` —— **这是工具没扫到，不是配置干净**。
本报告不把这两行当作通过；只有 KICS 对 compose 的覆盖被计为真实覆盖，
systemd（8 个）与 nginx（1 个）**逐条记入 NOT_VERIFIED**。

### 3.5 ⚠ systemd 单元在宿主上**根本没有安装**

8 个 `blockchain-*.service` 全部 `inactive`，`/etc/systemd/system/` 下不存在这些文件。
平台实际以**裸进程**运行（`backend/bin/api` pid 3413432、`worker` pid 3413434、`vite --host 0.0.0.0` 占用 3000）。
因此 `deploy/*.service` 是**期望态**而非**运行态**，基于它们的加固结论不能外推到当前进程。

### 3.6 ⚠ 宽目录 SBOM 不能代表生产依赖（本轮方法学修正）

第一版后端 SBOM 扫描的是 `backend/` **整目录**，产出 **10,003 个组件** —— 其中 **9,327 个来自 `/bin`**，
即约 **170 个 `api.bak-*` / `worker.bak-*` 历史备份二进制**；真正来自 `/go.mod` 的只有 **55 个**。
`metadata.component` 为 `{"type":"file","name":"/w"}`，**无名称无版本**。

因此已另出**仅含当前生产产物**的修正版 SBOM：**190 个组件**（来源仅 `/api` 55、`/go.mod` 55、`/worker` 44、`/migrate` 32）。

**交叉验证**：对三个**正在运行的**二进制取 `go version -m`，得到
chi/v5 **v5.2.1**、pgx/v5 **v5.7.4**、excelize/v2 **v2.9.0**、x/crypto **v0.54.0**（go1.26.7），
与 `dependency-report.md` 的 govulncheck 结论**逐项一致** → 修正版 SBOM 确实反映**当前生产构建**。
详见 `sbom/README.md`。

---

## 4. NOT_VERIFIED 明细（21 项，逐条保留，未因报告完成而收敛）

| ID | 未验证项 | 原因（含已尝试命令） |
|---|---|---|
| NV-D1 | 前端第二个独立公告扫描器 | 未安装；仅 pnpm audit 覆盖 npm 公告 |
| NV-D2 | 4 条 `golang.org/x/crypto` 公告的严重度 | `vuln.go.dev`、OSV、GHSA 均未发布该值；GHSA 无别名、`CVE-2026-56854` 返回 HTTP 404 → **记 UNKNOWN，不推测** |
| NV-D3 | 2 条 `xlsx` HIGH 在产物包内的可达性 | xlsx 为动态 import；未做调用图分析 |
| ~~NV-S1~~ | ~~后端 SBOM 未落到 Windows 树~~ | **已撤回**：`scp -o BatchMode=yes` 通道免密可用，两份文件已取回并逐字节校验一致（见 `sbom/backend-sbom-verification.md`）。原先「传输沙箱不足」的判断有误 |
| NV-S2 | `deploy.cdx.json` 内容 | syft 对纯配置目录报 **0 组件** —— 0 组件 ≠ 0 问题 |
| NV-S3 | 修正版生产 SBOM 未做 CVE 匹配 | 未对该 190 组件的 SBOM 跑 grype / `trivy sbom` |
| NV-S4 | 旧的污染版后端 SBOM 是否已被下游消费 | 需先确认再决定是否废弃 |
| NV-I1 | 上游漏洞库逐字节一致性 | `mirror.gcr.io` 超时、`ghcr.io` DENIED，实际用 daocloud 镜像 |
| NV-I2 | 是否存在可引用的 registry manifest digest | 4/4 `RepoDigest == config digest`；`docker manifest inspect postgres:18` 失败 |
| NV-I3 | **应用构建产物的漏洞扫描覆盖** | 只做了清单/哈希/秘密扫描，**未对二进制或前端产物跑任何漏洞扫描器** |
| NV-I4 | **宿主 systemd 配置面覆盖** | 无扫描器支持；且单元未安装，`systemd-analyze security` 无从运行 |
| NV-I5 | **宿主 nginx 配置面覆盖** | checkov / trivy config / kics 均不支持 nginx 配置语法 |
| NV-I6 | mobsf 镜像扫描 | Java DB 不可达中断；且不在本项目范围 |
| NV-C1 | checkov 对 `deploy/` 的覆盖 | `resource_count: 0`；`--framework docker_compose` 在本版本非法（argparse 报错） |
| NV-C2 | trivy config 对 `deploy/` 的覆盖 | `Detected config files num=0`，**改名为 `docker-compose.yml` 后仍为 0** |
| NV-C3 | 8 个 systemd 单元的自动化覆盖 | 三个扫描器都不支持 systemd |
| NV-C4 | nginx 配置的自动化覆盖 | 同上 |
| NV-C5 | 监控告警规则语义 | KICS 扫 2 个文件但只解析 1 个；该文件未被解析，未逐条复核 |
| NV-C6 | 部署态与仓库态的 systemd/nginx 一致性 | 单元未安装，无从比对 |
| NV-X1 | gitleaks / trufflehog 独立复核 | 两个主机上均 MISSING（已尝试 `Get-Command` / `command -v`） |
| NV-X2 | Git 历史秘密扫描 | VM 部署树不是 git 仓库；仅查了 Windows 工作树的跟踪文件清单 |
| NV-X3 | 前端产物包的熵分析 | 仅做了模式匹配与取值存在性检测 |

---

## 5. 要闭合还差什么（按优先级）

| # | 动作 | 影响 |
|---|---|---|
| 1 | **轮换** `POSTGRES_PASSWORD` / `CLICKHOUSE_PASSWORD` / `MINIO_ROOT_PASSWORD` / `blockchain_api` 口令 | 已随二进制分发，必须视为泄露 |
| 2 | 移除「把真实 DSN 作为编译期默认值」的写法，缺环境变量时**快速失败** | 根因修复 |
| 3 | 后端 `pgx/v5` → ≥ v5.9.2；`chi/v5` → ≥ v5.3.0；`excelize/v2` → ≥ v2.11.0 | 清 2×P0 + 3×P1 |
| 4 | 前端 `xlsx@0.18.5`：**npm 无修复版本**，**状态 OPEN（未关闭、未豁免）** → 换库 **或** 官方发行包 + 来源与完整性校验 + **真实 CSV/XLSX/JSONL 导出回归** + 复扫。**书面风险接受不能关闭它** | 2×P1，无法靠升级闭合 |
| 5 | `deploy/compose.yaml` 改为 **digest pin**（`image: repo@sha256:…`），复扫并记录 pinned digest | §29.1 |
| 6 | 处置 20 CRITICAL / 203 HIGH（83 项上游无修复 → **逐项决策，不得一键风险接受**） | 供应链 |
| 7 | 补 `cap_drop` / `security_opt` / minio+nats 健康检查 | KICS 10 MEDIUM |
| 8 | 决定 systemd 单元是否上线；上线后补沙箱加固并跑 `systemd-analyze security` | NV-C3 / NV-I4 |
| 9 | nginx 安全头提升到 server 级，补 HSTS 与 `server_tokens off`；引入 gixy | NV-C4 / NV-I5 |
| 10 | 对应用构建产物补跑漏洞扫描 | NV-I3 |
| 11 | `deploy/.env.example` 改占位符 + 清理 git 历史 | §23.8 |
| 12 | 上述全部纳入 CI | §23.8 明确要求「依赖漏洞、SBOM、容器镜像和 IaC 检查进入 CI」 |

**当前状态：§23.8 安全发布门不满足；本报告不构成发布门通过。**

---

## 6. 只读约束遵守情况

| 约束 | 状态 |
|---|---|
| 不修改 package.json / go.mod / 任何源码 | ✅ 0 个文件被修改（PRE/POST sha256 一致） |
| 不升级/安装依赖 | ✅ 0 个 |
| 不 `docker pull` / `build` / `rm` / `stop` | ✅ 0 次 |
| 不重启 systemd 服务 | ✅ 0 次 |
| 不写生产库 | ✅ 0 次 |
| 不删除任何文件 | ✅ 0 个 |
| 不触碰 `sprint7/dr/` PITR 文件 | ✅ 未读未写；宿主机上无关的 `pitr-*` 容器完全未触碰 |

**说明**：扫描器以 `docker run --rm` 运行，所用镜像**本轮开始前已存在于本机**（未拉取），
仓库以 `:ro` 只读挂载，仅新建的证据目录可写。

### 6.1 完整性证明

`backend/go.mod`、`backend/go.sum`、`frontend/pnpm-lock.yaml` 及 6 个 `package.json`
在每次扫描前后 sha256 **完全一致**，见：

- `deps/windows-host/commands.log`（Windows 侧 PRE/POST）
- `deps/vm-deps-run.log`（VM 侧 PRE/POST）

---

## 7. 本目录文件索引

| 文件 | 内容 |
|---|---|
| `summary.json` / `summary.md` | 本汇总（机器可读 / 人读） |
| `dependency-inventory.json` / `dependency-report.md` | 依赖清单与漏洞（P0/P1 分列） |
| `images.json` / `images.md` | 镜像引用形态、tag 可变性、digest、严重度 |
| `iac-coverage.md` | IaC 覆盖矩阵与逐文件人工核对 |
| `secret-scan.md` | 秘密扫描（日志 / 构建产物 / 仓库） |
| `sbom/` | SBOM 产物 + `README.md`（工具与局限） |
| `deps/`、`iac/`、`secret/`、`images/`、`artifacts/` | 原始测量产物 |
| `raw/` | 全部扫描脚本与运行日志（可原样重跑） |
| `SHA256SUMS.vm.txt` | VM 侧逐文件 sha256 清单（98/100 已在 Windows 侧比对通过） |
| `MANIFEST.md` | 本目录全部文件 sha256 |
