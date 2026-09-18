# 秘密扫描报告 — AUDIT-002 / §23.8 / §27.6

- 证据目录：`acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z/secret/`
- 机器可读产物：`secret/secret-scan-logs.json`、`secret/secret-scan-repo.json`、`secret/build-product-secret-check.json`
- **脱敏声明：本报告只输出 `文件:行` 与被掩码的值（首 2 位 + `***` + 末 2 位）或值的 sha256，绝不输出任何真实密钥明文。**

---

## 0. 结论速览

| 扫描域 | 范围 | 明文命中 | 判定 |
|---|---|---|---|
| **运行日志**（AUDIT-002 本体） | 69 个来源 / 191,904 行 / 30,083,203 字节 | **0** | ✅ **PASS** |
| **构建产物**（§23.8「构建产物不含 secret」） | 114 个产物 / 18 个候选值 | **7 个生产秘密内嵌** | ❌ **FAIL** |
| **仓库源码 + 部署配置** | 1,258 个文件 | 164 候选（绝大多数为类型注解误报）**+ 1 项真实问题** | ❌ **FAIL**（详见 §3） |

**最重要的发现：生产环境的 Postgres / ClickHouse / MinIO 口令被编译进了发布用 Go 二进制，且同样的口令还写在 git 跟踪的 `deploy/.env.example` 里。**

---

## 1. 扫描器与工具可用性

| 工具 | 状态 | 说明 |
|---|---|---|
| `gitleaks` | ❌ 不可用 | Windows 主机与 VM 均 `command -v gitleaks` → MISSING |
| `trufflehog` | ❌ 不可用 | 同上 |
| `trivy fs --scanners secret` | ⚠ 未使用 | trivy 0.74.0 的 secret 扫描能力可用，但本环境走 docker 挂载路径复杂；改用下述自建规则 + checkov secrets framework 交叉验证 |
| **checkov secrets framework** | ✅ 已用 | checkov 3.3.17，`--framework secrets`，全仓库 → `iac/checkov-secrets.json` |
| **自建正则扫描器** | ✅ 已用 | `raw/scan-secrets.py`（仓库）、`raw/scan-logs.py`（日志）、`raw/check-build-secrets.py`（构建产物） |

已尝试命令（如实记录）：`Get-Command gitleaks` / `Get-Command trufflehog`（Windows）、`command -v gitleaks trufflehog`（VM）→ 全部 MISSING。

---

## 2. 运行日志扫描（AUDIT-002 本体）— ✅ 0 命中

### 2.1 范围与规模

| 项 | 值 |
|---|---|
| 扫描来源数 | **69** |
| 扫描行数 | **191,904** |
| 扫描字节数 | **30,083,203**（约 28.7 MiB） |
| 读取失败来源 | **0** |
| **`password`/`token`/`key`/`cookie` 明文命中总数** | **0** |
| 分类明细 | `{}` （无任何命中，故无分类） |

### 2.2 覆盖的日志来源

| 类别 | 数量 | 具体 |
|---|---|---|
| nginx access（含历史轮转与 `.gz`） | 6 | `blockchain-platform.access.log` 及 `.1` / `.2.gz` / `.3.gz` / `.4.gz` / `.5.gz` |
| nginx error | 3 | `blockchain-platform.error.log` 及 `.1` / `.2.gz` |
| 后端 API 日志 | 30+ | `backend/logs/api-v5.log` … `api-v57.log` |
| 后端 Worker 日志 | 25+ | `backend/logs/worker-v5.log` … `worker-v59.log` |
| 前端日志 | 1 | `frontend/logs/vite-v5.log` |
| 应用运行输出 | 2 | `outputs/api-live-20260909-025358.log`、`outputs/worker-live-20260909-025358.log` |
| **容器日志** | 4 | `docker logs` 取 `deploy-postgres-1`（20,000 行）、`deploy-clickhouse-1`、`deploy-minio-1`、`deploy-nats-1` |

### 2.3 匹配规则（12 类）

`url_query_token`、`authorization_hdr`、`bearer_token`、`set_cookie`、`cookie_pair`、`json_password`、`kv_password`、`aws_key`、`jwt`、`pg_conn`、`private_key`，并对命中做占位符/模板/已脱敏二次分类。

### 2.4 为什么是 0 —— 两个可验证的设计原因（不是巧合）

1. **nginx 的 `log_format` 使用 `$uri` 而不是 `$request_uri`**（`deploy/nginx-blockchain-platform.conf:6`）。
   `$uri` **不含查询串**，因此形如 `/api/v1/download?token=…` 的敏感参数**不会**进入访问日志。
   这是一处**有意的良好设计**，直接解释了 `url_query_token` 规则 0 命中。
2. 静态材料中未发现内联口令，`deploy/.env` 通过 `EnvironmentFile=` 注入进程，而非写入日志。

---

## 3. ❌ 构建产物与仓库配置：生产口令暴露（本轮最重要的发现）

### 3.1 方法

`raw/check-build-secrets.py` 从 `deploy/.env` 与 `backend/.env` 中取出**每一个**秘密键的值（键名匹配 `PASSWORD|SECRET|TOKEN|API_?KEY|ACCESS_?KEY|_DSN|_URL|PRIVATE`，长度 ≥ 8 且非模板引用），
然后在**每一个构建产物**中做**字节子串存在性测试**。
**全程只输出「存在/不存在」与值的 sha256，从不打印值本身。**

| 项 | 值 |
|---|---|
| 测试的构建产物数 | **114**（3 个 Go 二进制 + 66 个前端 dist 文件 + 其余） |
| 测试的去重秘密值数 | **18** |
| **出现在构建产物中的秘密值** | **7** |

### 3.2 命中清单（值为 sha256，非明文）

| # | 秘密键 | 值长度 | 值 sha256（前 16） | 出现在 |
|---|---|---|---|---|
| 1 | **`POSTGRES_PASSWORD`** / `POSTGRES_DSN_PASSWORD` | 17 | `8cafc51c210a2240` | `backend/bin/api`、`backend/bin/worker`、`backend/bin/migrate` |
| 2 | **`CLICKHOUSE_PASSWORD`** / `CLICKHOUSE_DSN_PASSWORD` | 17 | `8690ac3034f20c0f` | `backend/bin/api`、`backend/bin/worker`、`backend/bin/migrate` |
| 3 | **`MINIO_ROOT_PASSWORD`** | 20 | `c0b83d9d44f5108f` | `backend/bin/api`、`backend/bin/worker`、`backend/bin/migrate` |
| 4 | `CLICKHOUSE_URL` | 67 | `c3a933ca551eab96` | `backend/bin/api`、`backend/bin/worker`、`backend/bin/migrate` |
| 5 | `NATS_URL` | 21 | `083f1b6f64b76c77` | `backend/bin/api`、`backend/bin/worker`、`backend/bin/migrate` |
| 6 | `ANKR_MANAGEMENT_MCP_URL` | 24 | `a90f0a3debaea02a` | `backend/bin/api`、`backend/bin/worker`、`backend/bin/migrate` |
| 7 | **`MIGRATION_DATABASE_URL`** / `DATABASE_URL` | 90 | `7eeb2d7fd07ad41f` | `backend/bin/api`、`backend/bin/worker`、`backend/bin/migrate` |

第 7 项尤其值得注意：该 90 字符 DSN **内嵌了 43 字符的 `blockchain_api` 应用账号口令**（`49da98fb5866d6a2`），
因此该高权限应用角色口令也随二进制一起分发。

### 3.3 对照：**没有**进入构建产物的秘密（11 个）

`ANKR_API_KEY`(64) 、`ANKR_API_KEY`(45)、`DATABASE_URL`(120)、`VITE_API_BASE_URL`、`ANKR_MANAGEMENT_FRONTEND_URL`、`CHAINBASE_API_KEY`、`TRONGRID_API_KEY`、`GPT_ACTIONS_API_KEY`、`MCP_API_KEY`、`REST_API_BASE_URL`、`POSTGRES_DSN_PASSWORD`(43，作为独立键)**未**出现在任何构建产物中。

### 3.4 ⚠ 同一批口令还写在 **git 跟踪**的 `deploy/.env.example` 里

实测（`raw/compare-env-creds.py`，只输出 sha256）：

| 值 sha256（前 12） | 出现在 |
|---|---|
| `8cafc51c210a` | `deploy/.env`（生产）、`backend/.env`、**`deploy/.env.example`**、以及 `backend/bin/{api,worker,migrate}` |
| `8690ac3034f2` | `deploy/.env`（生产）、`backend/.env`、**`deploy/.env.example`**、以及 `backend/bin/{api,worker,migrate}` |

**关键事实**：`deploy/.env.example` **是被 git 跟踪的文件**。

```text
$ git ls-files | grep -E '\.env|secret|credential'
acceptance-evidence/dual-system-v1/tests/sprint4/ids.env
deploy/.env.example                          <-- 含生产库口令
```

好消息是 `.gitignore` 已包含 `.env`，所以真实的 `deploy/.env` / `backend/.env` **本身**未被提交；
但**同一批口令被复制进了会被提交的 `.env.example`**，等于绕过了这条防线。

### 3.5 判定

| 检查项 | 需求 | 实测 | 判定 |
|---|---|---|---|
| §23.8「构建产物不含 secret」 | 0 | **7 个生产秘密内嵌于 3 个发布二进制** | ❌ **FAIL** |
| §23.8「日志秘密扫描 0 命中」 | 0 | **0** | ✅ PASS |
| AUDIT-002「password/token/key/cookie 0 明文（日志）」 | 0 | **0** | ✅ PASS |
| 仓库配置不含生产口令 | — | `.env.example`（git 跟踪）含 Postgres/ClickHouse 生产口令 | ❌ **FAIL** |

### 3.6 修复方向（需人工决策，本代理不改代码）

1. 移除 `internal/config` 中把真实 DSN 作为**编译期默认值**的写法 —— 缺环境变量时应**快速失败**，而不是回落到一个可用凭据。
2. **轮换** `POSTGRES_PASSWORD`、`CLICKHOUSE_PASSWORD`、`MINIO_ROOT_PASSWORD`、`blockchain_api` 口令 —— 它们已经随二进制分发出去了，必须视为已泄露。
3. `deploy/.env.example` 改为纯占位符（`<set-me>`），并考虑用 `git filter-repo` 清理历史。
4. 在 CI 增加「构建产物秘密扫描」门禁（本目录脚本可直接复用）。

---

## 4. 仓库源码扫描 — 164 候选（绝大多数为误报，已人工分类）

| 项 | 值 |
|---|---|
| 扫描文件数 | 1,258（按扩展名/大小跳过 391） |
| 原始命中 | 279 |
| 分类 | `CANDIDATE` 164、`PLACEHOLDER` 34、`TOO_SHORT` 38、`ENV_LOOKUP` 26、`ENV_VAR_NAME` 16、`NUMERIC_OR_PATH` 1 |
| 规则命中分布 | `api_key_assign` 122、`token_assign` 73、`password_assign` 29、`connection_string` 28、`cookie_assign` 26、`secret_assign` 1 |

**164 个 `CANDIDATE` 中绝大多数是 TypeScript/Go 类型注解误报**，例如
`password: string`、`apiKey: string`、`token: TokenView`、`token: Token[]` 等 —— 值是类型名而非秘密。
另有 57 条落在 `.codex-backups/`（历史备份树，非发布路径）。

**例外（需要处理）**：`connection_string` 规则在 `deploy/.env.example`、`backend/.env`、`deploy/.env` 中的命中，
正是 §3 描述的生产口令问题；在 `backend/bin/*` 中的命中是编译进二进制的同一批值。

### 4.1 checkov secrets framework 交叉验证

checkov 3.3.17 `--framework secrets` 全仓库跑出 **13 条 `CKV_SECRET_6`（Base64 High Entropy String）**，
全部位于 `artifacts/v142_p0/**/*.json`（P0 取证脚本的历史输出）。
逐条核对 JSON 键名后确认为 **`"page_token_hash"` 字段 —— 分页游标的哈希值，不是凭据**。
**判定：13 条均为误报，不构成凭据泄露。**

---

## 5. 复核用命令（可原样重跑）

```bash
# 日志（AUDIT-002 本体）
python3 raw/scan-logs.py /home/euripides/projects/blockchain-platform <evidence>/secret

# 仓库源码
python3 raw/scan-secrets.py /home/euripides/projects/blockchain-platform <evidence>/secret

# 构建产物内嵌秘密（§23.8）
python3 raw/check-build-secrets.py /home/euripides/projects/blockchain-platform <evidence>/secret

# 生产 .env 与构建产物/示例文件的口令同值比对
python3 raw/compare-env-creds.py /home/euripides/projects/blockchain-platform
```
