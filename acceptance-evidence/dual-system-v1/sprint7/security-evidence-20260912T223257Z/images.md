# 容器镜像证据 — 引用形态 / tag 可变性 / repo digest / 镜像 ID / 严重度分级

- 证据目录：`acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z/`
- 机器可读产物：`images.json`、`images-summary.json`、`images/*.json`、`images/*.txt`
- **只读声明**：未执行 `docker pull` / `build` / `rm` / `stop` / 重启任何容器或服务；扫描器镜像在本轮前已存在于本机。

---

## 0. 结论（先读）

> **当前部署不满足「镜像不可变、可作为发布证据」的要求（计划 §29.1）。**
>
> 原因有两条，**互相独立、都成立**：
> 1. `deploy/compose.yaml` **全部以 tag 引用镜像，没有任何 `@sha256` digest pin**（`grep -c '@sha256'` = **0**）；
> 2. 本机**没有可用于引用的 registry manifest digest** —— 见 §3 的实测。
>
> 因此 **任何 `docker compose pull` / 重新部署都可能静默换入新内容**，
> 而本报告的严重度计数只是**某一时刻快照**上的数字。
>
> 本报告**不构成发布门通过**（`not_a_clearance: true`）。

---

## 1. 扫描器溯源（可复现性前提）

| 项 | 值 |
|---|---|
| 扫描器 | **trivy** |
| 版本 | **0.74.0** |
| 扫描器镜像 | `aquasec/trivy:latest` |
| 扫描器镜像 digest | `sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969` |
| 扫描器镜像构建时间 | 2026-08-14T11:27:20.641164516Z |
| **漏洞库 DB Version** | **2** |
| **DB UpdatedAt** | **2026-09-12T13:01:09.044712903Z** |
| DB NextUpdate | 2026-09-13T13:01:09.044712565Z |
| DB DownloadedAt | 2026-09-12T16:47:08.604622754Z |
| DB 仓库 | `docker.m.daocloud.io/aquasec/trivy-db:2`（回退镜像） |
| **digest 解析时间（UTC）** | **2026-09-12T22:33Z**（`docker image inspect`） |
| **digest 复测时间（UTC）** | **2026-09-12T22:5xZ**（`docker images --digests` / `docker image inspect` 二次确认，结论一致） |

### 1.1 漏洞库溯源的重要保留意见

`mirror.gcr.io` 与 `ghcr.io` 在本环境**均不可达**（超时 / 拒绝），实际使用 daocloud 镜像。
该镜像是同一 OCI 构件的 pull-through 缓存，但**与上游的逐字节一致性未能验证** → 已列入 NV 清单。

### 1.2 命令与真实退出码

```text
docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
  -v trivy-cache:/root/.cache/trivy -v <evidence>/images:/out \
  aquasec/trivy:latest image --scanners vuln --image-src docker \
  --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL \
  --db-repository docker.m.daocloud.io/aquasec/trivy-db \
  --timeout 30m --cache-dir /root/.cache/trivy \
  --format json|table --output /out/<name>.json|txt <image-ref>
```

4 个在范围内镜像的 `JSON_EXIT_*` 与 `TABLE_EXIT_*` **全部为 0**（`raw/trivy-images-run.log`）。

---

## 2. 在范围镜像逐项表（四列如实填写）

| 镜像（运行容器） | ① 引用形态 | ② tag 是否可被重指 | ③ 是否有可引用的 repo digest | ④ 当前镜像 ID（config digest） |
|---|---|---|---|---|
| `postgres:18` (deploy-postgres-1) | **tag**（`compose.yaml:3`，无 @sha256） | **是**（版本 tag 同样可重指） | **无**（本机 RepoDigests 存在但与 config digest 逐字节相同，见 §3） | `sha256:4ef4dbc939d61acea57712655ddb4b4ab27419c913f94cca0cd57cb3ea3c2280` |
| `clickhouse/clickhouse-server:26.7` (deploy-clickhouse-1) | **tag**（`compose.yaml:20`，无 @sha256） | **是** | **无**（同上） | `sha256:800e82865530eb2f1c4bc1b960e43b435fd9b2d83b4bd04a2564a5cfd88fdb6e` |
| `minio/minio:latest` (deploy-minio-1) | **tag**（`compose.yaml:43`，无 @sha256） | **是**（且为浮动的 `latest`） | **无**（同上） | `sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e` |
| `nats:2-alpine` (deploy-nats-1) | **tag**（`compose.yaml:56`，无 @sha256） | **是** | **无**（同上） | `sha256:ad7a43eb7e3337c3c38ce5d784d1461791f95f730f252d2b25eee699752a0ca3` |
| `opensecurity/mobile-security-framework-mobsf:latest` (mobsf) | **tag**（**不在本项目范围**） | **是** | **无**（同上） | `sha256:dd1e194f5d7aec5c27c6feba8085bcfa5c56ae59f0e13b728d3a76ca354dd0ee` |

补充可核验事实：

- **compose 中 `@sha256` 出现次数 = 0**（`grep -c '@sha256' deploy/compose.yaml`）。
- **四个容器均为 `Config.Image` = tag 形式**，`Image` 字段 = 上面的镜像 ID：
  `deploy-postgres-1 Image=sha256:4ef4dbc9… ConfigImage=postgres:18`，其余三个同构。
- **没有任何镜像按 digest pin**：`images_pinned_by_digest = 0`，`images_referenced_by_repointable_tag = 5`。

### 2.1 这为什么重要

「resolve 出了 digest」**不等于**「镜像不可变」。任意 tag（包括 `18`、`26.7`、`2-alpine` 这类版本 tag）都可以被重新指向别的 digest。
resolved digest 只是一个**时间点快照**。要获得真正的不可变性，必须二选一：

1. 在 `deploy/compose.yaml` 中以 **`image: repo@sha256:...`** 形式固定；或
2. 使用**发布方承诺不可变**的 tag（如 digest 化 tag），并有可核验的上游 manifest digest。

当前**两者都不满足**。

---

## 3. ⚠ 实测：本机没有可引用的 registry manifest digest

### 3.1 实测输出

```text
$ docker image inspect postgres:18 --format 'RepoDigests={{json .RepoDigests}} Id={{.Id}}'
RepoDigests=["postgres@sha256:4ef4dbc939d61acea57712655ddb4b4ab27419c913f94cca0cd57cb3ea3c2280"]
Id=sha256:4ef4dbc939d61acea57712655ddb4b4ab27419c913f94cca0cd57cb3ea3c2280
```

逐镜像比对 `RepoDigest == Id`：

| 镜像 | RepoDigest 是否等于 config digest (`Id`) |
|---|---|
| `postgres:18` | **True** |
| `clickhouse/clickhouse-server:26.7` | **True** |
| `nats:2-alpine` | **True** |
| `minio/minio:latest` | **True** |

### 3.2 解释

**4/4 镜像的 `RepoDigests` 与其 config digest（镜像 ID）逐字节相同。**
这是镜像**由本地装载/importer 写入**（而非携带可核验的 registry manifest digest）时的典型特征 ——
真正的 registry manifest digest 与 config digest 是**两个不同的值**。

旁证：`docker manifest inspect postgres:18` 在本环境**失败/不可用**，无法从 registry 反查 manifest digest。

### 3.3 与另一份观察的调和（如实记录）

另一路复核报告 `docker inspect <container>` 看到的 `RepoDigests` 为 `<none>`。
本轮在**镜像级**（`docker image inspect <tag>`)测得该字段**非空但与 config digest 相同**。
两者指向**同一个实质结论**：**本机不存在可作为发布证据引用的、独立于 config digest 的 registry manifest digest。**
本报告以「实测值 + 其与 config digest 相同」这一更强、可复核的表述为准，不使用笼统的「resolved digest 已采集」说法来掩盖这一点。
另注明：`LastTagTime` 为 2026-08-31T11:1xZ（全部 4 个镜像），即本地打 tag 的时间。

---

## 4. 严重度分级（实测，未做任何调整）

| 镜像 | CRITICAL | HIGH | MEDIUM | LOW | UNKNOWN | 合计 | 无修复版本项 |
|---|---|---|---|---|---|---|---|
| `postgres:18` | **14** | **101** | 154 | 143 | 3 | 415 | 56 |
| `clickhouse/clickhouse-server:26.7` | 0 | 0 | 68 | 30 | 0 | 98 | 21 |
| `nats:2-alpine` | 0 | 2 | 8 | 12 | 1 | 23 | 0 |
| `minio/minio:latest` | **6** | **100** | 101 | 16 | 4 | 227 | 6 |
| **在范围合计（4 个镜像）** | **20** | **203** | **331** | **201** | **8** | **763** | **83** |

与上一轮的比对：上一轮记录的「CRITICAL 20 / HIGH 203（postgres:18 14C+101H、minio:latest 6C+100H）」
**在本轮被独立复现且完全一致**（14+0+0+6=20；101+0+2+100=203）。

> **这些数字不构成风险接受，也不因本报告交付而清零。**
> 20 CRITICAL / 203 HIGH 仍是未处置的开放项；升级/替换 + 复扫 + 功能回归需另行安排。

---

## 5. 范围外（明确排除，不计入任何合计）

| 镜像 | 引用形态 | tag 可变 | repo digest | 镜像 ID | 状态 |
|---|---|---|---|---|---|
| `opensecurity/mobile-security-framework-mobsf:latest` | tag | 是 | 无（同 §3） | `sha256:dd1e194f5d7aec5c27c6feba8085bcfa5c56ae59f0e13b728d3a76ca354dd0ee` | **不在本项目发布范围（宿主机额外库存）** |

`mobsf` 是宿主机上独立且与本项目无关的服务，**不纳入**本项目的发布门、P0/P1 计数或镜像严重度合计。
其 trivy 扫描在下载 Java DB 时因 `mirror.gcr.io` 不可达而中断 → **本轮不对该镜像声明任何严重度计数**。
未对该容器执行任何修改、停止或重启。

---

## 6. 平台自身构建产物（非容器镜像，但属发布范围）

| 产物 | sha256（完整清单见 `artifacts/artifact-inventory.json`） |
|---|---|
| `backend/bin/api` | `e0a7c3bd1983cc18da85bd5e1a2e3712b9dde43bb67d48ee5e4722f53368abb7` |
| `backend/bin/worker` | `9c41079d5c59067e0585166d049219de0f14fd93069b3c3a194dff0ecf9fccb6` |
| `backend/bin/migrate` | `ab10efe36791e49ea820fb7b293c6aa4104bf93788dacacc87f7f7895ca19b4f` |
| `frontend/apps/admin-console/dist/**`（27 个文件） | 见 `artifact-inventory.json` |
| `frontend/apps/user-console/dist/**`（39 个文件） | 见 `artifact-inventory.json` |
| nginx（宿主） | 版本 **1.24.0 (Ubuntu)**；`/etc/nginx/sites-enabled/blockchain-platform.conf` = `200ef752cc577569eb6c13d4b5eab791607f5a50496ce5780f8afde50cc9ccab` |

⚠ **这三个 Go 二进制内嵌了 7 个生产秘密**（含生产 Postgres / ClickHouse / MinIO 口令），详见 `secret-scan.md` §3。

---

## 7. NOT_VERIFIED 清单（逐条，未因报告完成而收敛）

| # | 未验证项 | 原因 | 已尝试的命令 | 闭合所需动作 |
|---|---|---|---|---|
| NV-1 | **上游漏洞库逐字节一致性** | `mirror.gcr.io` 超时、`ghcr.io` 被拒；实际使用 daocloud pull-through 镜像 | `trivy image --download-db-only --db-repository mirror.gcr.io/aquasec/trivy-db`（超时）；`--db-repository ghcr.io/aquasec/trivy-db`（DENIED） | 上游可达后复扫，或离线核验镜像 digest |
| NV-2 | **本机是否存在可引用的 registry manifest digest** | 4/4 镜像 `RepoDigest == config digest`；`docker manifest inspect` 失败 | `docker image inspect <tag> --format '{{json .RepoDigests}}|{{.Id}}'`；`docker images --digests`；`docker manifest inspect postgres:18` | 从可信 registry 重新以 manifest 方式拉取并记录 manifest digest，或直接 pin config digest |
| NV-3 | **应用构建产物的漏洞扫描覆盖** | 只做了清单/哈希与秘密扫描，**未对二进制或前端产物跑任何漏洞扫描器** | 无（未执行 trivy fs / grype / govulncheck 对已构建产物） | 对 `backend/bin/*` 与两个 dist 目录运行 `trivy fs` 或 `grype` |
| NV-4 | **宿主 systemd 配置面覆盖** | 本环境无任何扫描器支持 systemd 单元；且宿主上**根本没有安装** `blockchain-*.service`（8/8 inactive、文件不存在），`systemd-analyze security` 无从运行 | `systemctl list-unit-files \| grep -i blockchain`；`ls /etc/systemd/system/ \| grep -i blockchain`；`systemctl is-active blockchain-*.service` ×8 | 先决定单元是否上线；上线后运行 `systemd-analyze security` |
| NV-5 | **宿主 nginx 配置面覆盖** | 无受支持的扫描器（checkov / trivy config / kics 均不支持 nginx 配置语法） | `trivy config <dir>` → `Detected config files num=0`；`checkov -d <dir>` → `resource_count: 0` | 引入 gixy 等 nginx 专用 linter 并纳入 CI |
| NV-6 | **`minio/mc:latest` 等其它宿主机镜像** | 不在本项目运行范围，未扫描 | `docker images --digests` 仅做清单 | 与本项目发布门无关 |
| NV-7 | **mobsf 镜像扫描** | Java DB 镜像不可达导致扫描中断；且该镜像明确不在本项目范围 | trivy image（挂起于 `[javadb] Downloading Java DB... repo="mirror.gcr.io/aquasec/trivy-java-db:1"`） | 本项目发布门不要求 |

---

## 8. 要闭合还差什么

| # | 动作 |
|---|---|
| 1 | 在 `deploy/compose.yaml` 中把发布相关镜像**改为 digest pin**（`image: repo@sha256:…`），或改用有不可变承诺的 tag；然后**重新扫描并记录 pinned digest** |
| 2 | 取得可核验的上游 manifest digest（修复 registry 可达性或从可信源导入） |
| 3 | 处置 20 CRITICAL / 203 HIGH（其中 83 项上游无修复版本 —— 需逐项决策，**不能用风险接受一键抹平**） |
| 4 | 对应用构建产物补跑漏洞扫描（NV-3） |
| 5 | 把镜像扫描 + digest pin 校验纳入 CI（§23.8 要求「容器镜像检查进入 CI」） |

---

## 9. 判定

| 检查项 | 结果 |
|---|---|
| 镜像扫描是否真实执行 | ✅ 是（trivy 0.74.0，4/4 退出码 0，原始 JSON/表格留存） |
| 每个镜像是否有 tag 可变性 / pin 状态陈述 | ✅ 是（§2 四列逐项） |
| 是否存在可引用的 registry manifest digest | ❌ **否**（4/4 `RepoDigest == config digest`；`docker manifest inspect` 失败） |
| compose 是否 pin digest | ❌ **否**（`@sha256` 出现 0 次） |
| 是否使用了浮动的 `latest` | ❌ **是 —— `minio/minio:latest`** |
| 在范围严重度合计 | 20 CRITICAL / 203 HIGH / 331 MEDIUM / 201 LOW / 8 UNKNOWN = 763 |
| **该维度判定** | **FAIL** |
| **本报告是否等于发布门通过** | **否（`not_a_clearance: true`）** |
