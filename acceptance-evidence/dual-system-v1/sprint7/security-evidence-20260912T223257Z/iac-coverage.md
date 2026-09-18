# IaC 覆盖报告 — compose / systemd / nginx / 监控配置

- 证据目录：`acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z/iac/`
- 原始日志：`iac/iac-run.log`、`iac/iac-normalized.log`、`iac/kics-deploy.log`、`iac/checkov-deploy.json`、`iac/trivy-config-deploy.*`、`iac/results.json`

---

## 0. 最重要的结论（请先读这一段）

上一轮把 IaC 扫描记为 **NOT_VERIFIED**。本轮**实际执行后确认：这个 NOT_VERIFIED 是正确的**，
而且**根因已经被定位** —— 不是「扫了没发现问题」，而是**checkov 与 trivy 一个文件都没扫到**。

| 扫描器 | 版本 | 对 `deploy/` 的实际覆盖 | 证据 |
|---|---|---|---|
| **checkov** | 3.3.17 | **resource_count = 0（零个文件）** | `iac/checkov-deploy.json` |
| **trivy config** | 0.74.0 | **Detected config files num = 0（零个文件）** | `iac/trivy-config-deploy.txt` |
| **KICS** | v2.1.20 | **files_scanned=2, files_parsed=1（真实覆盖 compose.yaml）** | `iac/results.json` |

> **「扫描到 0 个文件」绝不等同于「0 个问题」。**
> checkov 的 `resource_count: 0` 与 trivy 的 `Detected config files num=0` 是**工具没扫到东西**，
> 而不是「配置干净」。本报告不会把这两行当作通过。

### 0.1 根因（已用实验隔离）

1. **文件名约定**：checkov 与 trivy 的 docker-compose 支持要求文件名为 `docker-compose.yml` / `docker-compose.yaml`，
   而本仓库使用 `compose.yaml`。
2. **实验验证**：把 `deploy/compose.yaml` **逐字节复制**为证据目录内的 `iac/normalized/docker-compose.yml`
   （`cmp` 通过，sha256 相同 `215cf15e…694d66`，**仓库文件未被修改**）后重跑：
   - trivy 仍然 `Detected config files num=0` → **文件名不是 trivy 的唯一原因，trivy 0.74.0 OSS 版不具备 docker-compose 扫描能力**；
   - checkov 仍然 `resource_count: 0` → 同理，`--framework docker_compose` 在本版本甚至不是合法取值（argparse 直接报错，见 `iac/checkov-compose.stderr.txt`）。
3. **工具能力边界**：`systemd` 单元（`.service`/`.timer`）与 `nginx` 配置**不在** checkov / trivy / kics 任一者的支持范围内。

因此：**compose 由 KICS 提供自动化覆盖；systemd 与 nginx 只能人工结构化核对。**

---

## 1. 目标文件清单与 sha256（逐文件）

| 文件 | 字节 | sha256 | 自动化覆盖 |
|---|---|---|---|
| `deploy/compose.yaml` | 1910 | `215cf15e37b5977c4e66ce25314abe81ae98eb2a4359985dc2ae686261694d66` | **KICS（1 个文件，10 条 MEDIUM）** |
| `deploy/nginx-blockchain-platform.conf` | 5826 | `200ef752cc577569eb6c13d4b5eab791607f5a50496ce5780f8afde50cc9ccab` | ❌ 无（人工核对） |
| `deploy/monitoring/dual-system-alerts.yaml` | 4143 | `1728ae7ed7ee058472274659bc189f46532c514bf13a485f484bbdc715d8735d` | ❌ 无（人工核对） |
| `deploy/blockchain-api.service` | 790 | `0dfc95e6c88db0137a4b6bda362b6e0d7cfbcc122f26c38d360c19aa61cf5516` | ❌ 无（人工核对） |
| `deploy/blockchain-worker.service` | 742 | `00a8b99ee41ef21f520779edf7c28fe869578940634bf9c70ca7f3102f88d3a7` | ❌ 无（人工核对） |
| `deploy/blockchain-admin-console.service` | 958 | `f81588cb370c71862d43e2a764aab8066c555f2c60798291d11f96dd5b2cd912` | ❌ 无（人工核对） |
| `deploy/blockchain-user-console.service` | 966 | `6fe9149e0052745b40275eb436c0a33975623bab25b5fbc075a22085508fa867` | ❌ 无（人工核对） |
| `deploy/blockchain-vite.service` | 799 | `e208fb4044d6a1e0ab7a057bba48ce8e7972f06b0e10ceacab961ffb714bb31b` | ❌ 无（人工核对） |
| `deploy/blockchain-mcp.service` | 565 | `8073d8e5e816f526e6e4f74d09d886b8f27dc76b199ec220ccbb2a77dad8fa1d` | ❌ 无（人工核对） |
| `deploy/blockchain-e2e-tls.service` | 1646 | `7cdae70d85c191883f836df8de2dbf53546c3e390db68927f77cd8159834fe8b` | ❌ 无（人工核对） |
| `deploy/blockchain-containers-watchdog.service` | 269 | `b0cd75b4a65b3d6c5102de461e67e771431e1a3b41f579de1b7374e40ebac6a6` | ❌ 无（人工核对） |
| `deploy/blockchain-containers-watchdog.timer` | 212 | `fb2a24b0fbc33839d1efd6e91dafc5e6cad418c8dbe60de61eb02391fcdfb13f` | ❌ 无（人工核对） |
| `deploy/.env.example` | 8488 | `22426c160740a00fa784af1f146d7cda8eab18bd1b0e9ba2d92f1ca83111d820` | ❌ 无（人工核对；**内容问题见 `secret-scan.md`**） |

**部署机实际生效的 nginx 配置与仓库版本 sha256 一致**：
`/etc/nginx/sites-enabled/blockchain-platform.conf` = `200ef752…c9ccab`（仓库 `deploy/nginx-blockchain-platform.conf` 同值）→ 仓库配置即线上配置。

---

## 2. KICS 对 compose.yaml 的自动化结果（真实覆盖）

扫描器：**KICS v2.1.20**（镜像 `checkmarx/kics:latest`，本机已有，未拉取）

```text
files_scanned  : 2
lines_scanned  : 194
files_parsed   : 1        <- compose.yaml
queries_total  : 308
queries_failed_to_execute : 0
total_counter  : 10
severity_counters: CRITICAL 0 / HIGH 0 / MEDIUM 10 / LOW 0 / INFO 0
```

10 条 MEDIUM 命中集中在 3 条规则：

| 规则 | Query ID | 含义 | 涉及服务 |
|---|---|---|---|
| Container Capabilities Unrestricted | `ce76b7d0-9e77-464d-b86f-c5c48e03e22d` | 未 `drop` 不必要 capability | postgres / clickhouse / minio / nats |
| Healthcheck Not Set | `698ed579-b239-4f8f-a388-baa4bcb13ef8` | 未定义健康检查 | `minio`、`nats`（postgres、clickhouse 已定义） |
| Security Opt Not Set | `610e266e-6c12-4bca-9925-1ed0cd29742b` | 未设置 `security_opt`（如 `no-new-privileges`） | 全部服务 |

原始产物：`iac/results.json`、`iac/results.html`、`iac/kics-deploy.log`。

---

## 3. 人工结构化核对（systemd / nginx / compose / 监控）

### 3.1 `deploy/compose.yaml` — 已核对项

| # | 核对项 | 结果 |
|---|---|---|
| 1 | 端口是否仅绑定回环 | ✅ 全部 `127.0.0.1:${PORT}:…`，无 `0.0.0.0` 暴露 |
| 2 | 是否硬编码口令 | ✅ 全部走 `${VAR}` 插值，无明文（**但 `.env.example` 的内容问题见 `secret-scan.md`**） |
| 3 | 是否使用可变 `latest` | ❌ **`minio/minio:latest`（§29.1 违规）** |
| 4 | 健康检查 | ⚠ 仅 postgres / clickhouse 有；minio、nats 无 |
| 5 | `cap_drop` | ❌ 无 |
| 6 | `security_opt: no-new-privileges` | ❌ 无 |
| 7 | `read_only: true` 根文件系统 | ❌ 无 |
| 8 | 资源限制（`cpus` / `mem_limit`） | ❌ 无 |
| 9 | 显式非 root `user:` | ❌ 无（依赖镜像默认） |
| 10 | 数据卷落盘位置 | ✅ 显式挂载 `/data/blockchain-platform/*` |
| 11 | 网络 | ✅ 显式命名网络 `blockchain-platform` |
| 12 | `restart` 策略 | ✅ `unless-stopped` |

**未覆盖项**：无（该文件由 KICS 自动化 + 上表人工双覆盖）。

### 3.2 systemd 单元（8 个）— 已核对项

| # | 核对项 | 结果 |
|---|---|---|
| 1 | 沙箱加固指令（`NoNewPrivileges=` / `PrivateTmp=` / `ProtectSystem=` / `ProtectHome=` / `ReadWritePaths=` / `CapabilityBoundingSet=` / `RestrictAddressFamilies=` / `ProtectKernelTunables=` …） | ❌ **8 个单元全部一条都没有** |
| 2 | 专用 `User=` / `Group=` | ❌ 均未指定（跟随用户会话/默认） |
| 3 | 资源限制（`MemoryMax=` / `CPUQuota=` / `LimitNOFILE=`） | ❌ 均未指定 |
| 4 | 秘密是否写入单元文件 | ✅ 走 `EnvironmentFile=…/deploy/.env`，单元内无明文口令 |
| 5 | 重启策略 | ⚠ `Restart=on-failure` + `StartLimitIntervalSec=0` → **无限重启、无速率限制** |
| 6 | 日志落盘位置 | ❌ `StandardOutput=append:/tmp/*.log` —— 落在 `/tmp`，重启易失且权限面较宽 |
| 7 | 监听地址 | ⚠ `blockchain-vite.service` 用 `--host 0.0.0.0`（**开发服务器监听所有网卡**）；api/worker/两个 console 均绑定 `127.0.0.1` |
| 8 | 依赖顺序 | ✅ `After=` / `Wants=` 声明完整 |
| 9 | 执行路径 | ✅ 均为绝对路径 |
| 10 | 自签 TLS 单元 | ⚠ `blockchain-e2e-tls.service` 使用 `socat` + `verify=0` 自签证书；仅监听 `127.0.0.1:8448`，注释明确标注「仅用于验收」 |
| 11 | watchdog | ⚠ 每 30s 以 `oneshot` 执行仓库内脚本 `scripts/backup/containers-watchdog.sh` |

**未覆盖项**：以上均为人工阅读判定；**没有任何自动化工具对这些单元做过校验**。

### 3.3 ⚠ systemd 单元在部署机上**根本未安装**（重要事实）

实测（`iac/systemd-and-services.txt`）：

```text
$ systemctl list-unit-files | grep -i blockchain
(无输出)
$ ls /etc/systemd/system/ | grep -i blockchain
(无输出)
$ sha256sum /etc/systemd/system/blockchain-*.service
sha256sum: '/etc/systemd/system/blockchain-*.service': No such file or directory
$ systemctl is-active blockchain-api.service blockchain-worker.service \
    blockchain-admin-console.service blockchain-user-console.service \
    blockchain-vite.service blockchain-mcp.service \
    blockchain-e2e-tls.service blockchain-containers-watchdog.service
inactive  (x8)
```

平台实际上是**裸进程**在跑（`ps` 实测）：

```text
pid 3413432  /home/euripides/projects/blockchain-platform/backend/bin/api
pid 3413434  /home/euripides/projects/blockchain-platform/backend/bin/worker
pid 2619253  vite preview --config apps/user-console/… --port 3001
pid 2619252  vite preview --config apps/admin-console/… --port 3002
pid 3744085  vite --host 0.0.0.0            (port 3000, 0.0.0.0:3000 已确认 LISTEN)
pid 1304370  node dist/server.js            (127.0.0.1:8090, MCP)
+ 系统 nginx 1.24.0 (master + 4 workers)
```

**含义**：`deploy/*.service` 是仓库内的期望态配置，**不是当前线上运行态**。
任何基于这些单元的加固或验收结论都**不能**自动外推到当前运行的进程。要闭合必须先把单元真正安装并让服务托管运行。

### 3.4 `deploy/nginx-blockchain-platform.conf` — 已核对项

| # | 核对项 | 结果 |
|---|---|---|
| 1 | 监听地址 | ✅ `listen 127.0.0.1:8088` 仅回环，公网入口由 cloudflared 隧道终止 |
| 2 | CSP（SEC-002 第二道防线） | ⚠ **仅覆盖 `location ^~ /admin/` 与 `location ^~ /app-v2/`**；`location /`、`/api/`、`/mcp`、`/openapi.json`、`/docs`、`/redoc` **均无 CSP** |
| 3 | `X-Content-Type-Options: nosniff` | ⚠ 同上，仅两个 console 前缀 |
| 4 | `Referrer-Policy` | ⚠ 同上，仅两个 console 前缀 |
| 5 | `frame-ancestors 'none'`（防点击劫持） | ✅ 已包含在 CSP 内（两个 console 前缀） |
| 6 | `base-uri` / `form-action` 限制 | ✅ 已包含在 CSP 内 |
| 7 | HSTS（`Strict-Transport-Security`） | ❌ 未设置（TLS 在 Cloudflare 侧终止，需在 CF 或此处补） |
| 8 | `server_tokens off`（版本隐藏） | ❌ 未设置 |
| 9 | 速率限制（`limit_req`） | ❌ 未见（SEC-004 限流在应用层实现，此处未叠加） |
| 10 | 请求体大小 | ✅ `client_max_body_size 100m` 显式设置 |
| 11 | 代理超时 | ✅ 各 location 显式设置读写超时 |
| 12 | **访问日志是否泄露查询串秘密** | ✅ **`log_format` 使用 `$uri`（不含查询串）而非 `$request_uri`** —— 这是**有意为之的良好设计**，是 `secret-scan.md` 中 nginx 日志 0 命中的直接原因 |
| 13 | 上游地址 | ✅ 全部 `127.0.0.1:*`，无外部上游 |
| 14 | `proxy_set_header X-Forwarded-Proto https` | ✅ 硬编码 `https`，与 cloudflared 链路一致（会话 Cookie 的 `Secure`/`__Host-` 依赖此） |

**未覆盖项**：以上全为人工阅读判定，**没有任何自动化工具校验过该 nginx 配置**。

### 3.5 `deploy/monitoring/dual-system-alerts.yaml` — 已核对项

- 文件存在（4143 字节，sha256 `1728ae7ed7ee058472274659bc189f46532c514bf13a485f484bbdc715d8735d`）。
- KICS 的 `files_scanned: 2` 中**只有 1 个文件被解析**（compose.yaml），该告警文件**未被解析**。
- **未覆盖项**：告警规则内容未做逐条语义核对（不在本轮 §23.8/§31 证据清单范围内）。

---

## 4. 覆盖矩阵（一目了然）

| 文件类型 | 文件数 | checkov | trivy config | KICS | 人工 | 覆盖判定 |
|---|---|---|---|---|---|---|
| compose | 1 | ❌ 0 文件 | ❌ 0 文件 | ✅ 10 MEDIUM | ✅ | **已覆盖** |
| systemd 单元 | 8 | ❌ | ❌ | ❌ | ✅ | **仅人工（自动化 NOT_VERIFIED）** |
| nginx 配置 | 1 | ❌ | ❌ | ❌ | ✅ | **仅人工（自动化 NOT_VERIFIED）** |
| 监控告警 | 1 | ❌ | ❌ | ⚠ 未解析 | ⚠ 未逐条 | **NOT_VERIFIED** |
| `.env.example` | 1 | ❌ | ❌ | ❌ | ✅ | **仅人工（见 `secret-scan.md`）** |

---

## 5. 要闭合还差什么

| # | 动作 | 说明 |
|---|---|---|
| 1 | 为 `deploy/` 补一个**真正支持 docker-compose + systemd + nginx** 的检查器 | 例：把 compose 改名/软链为 `docker-compose.yml` 让 CI 识别；systemd 用 `systemd-analyze security`；nginx 用 `gixy`/`nginx-amplify` 或自建规则 |
| 2 | `minio/minio:latest` 固定 digest | 见 `images.md` §3 |
| 3 | compose 补 `cap_drop` / `security_opt: no-new-privileges` / minio+nats 健康检查 | KICS 的 10 条 MEDIUM |
| 4 | 决定 systemd 单元是否真的要上线 | 当前线上是裸进程，单元未安装，`systemd-analyze security` 无从运行 |
| 5 | 给 8 个单元补沙箱加固指令 | `NoNewPrivileges`、`ProtectSystem=strict`、`ProtectHome`、`PrivateTmp`、`CapabilityBoundingSet=`、专用 `User=` |
| 6 | `blockchain-vite.service` 去掉 `--host 0.0.0.0` | 当前 3000 端口实际监听所有网卡 |
| 7 | nginx 把 CSP / nosniff / Referrer-Policy 提升到 `server` 级 | 现仅两个 console 前缀有 |
| 8 | 补 HSTS 与 `server_tokens off` | — |
| 9 | 把上述检查并入 CI | §23.8 明确要求「IaC 检查进入 CI」 |
