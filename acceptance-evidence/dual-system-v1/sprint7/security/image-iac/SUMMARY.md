# S7-SEC-01 — 镜像 / IaC 扫描汇总（trivy）

生成时间（UTC）：2026-09-12T17:04:51.474Z

执行机：euripides@192.168.31.152（dataserver, Linux 6.8.0-139-generic）

证据根目录：/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/image-iac

## 0. 一句话结论

- 镜像漏洞：4 个 compose 镜像共 CRITICAL 20 / HIGH 203 / MEDIUM 331 / LOW 201 / UNKNOWN 8（合计 763）。
- misconfig：trivy 的 config 扫描器根本没有扫描任何文件（Detected config files num=0）。
  这是「0 个文件被扫描」而不是「0 个问题」。trivy-checks 不支持 docker-compose / systemd unit / nginx conf，
  而本仓库恰好只有这些类型的配置（无 Dockerfile、无 k8s、无 terraform/helm/ansible）。
- 因此 IaC 配置面在本轮属于 NOT_VERIFIED（工具未覆盖），不能据此判定为「干净」。

## 1. 工具与镜像溯源

| 项 | 值 |
|---|---|
| trivy | aquasec/trivy:latest，Version: 0.74.0 |
| trivy 镜像 digest | sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969 |
| trivy 镜像 build 时间 | 2026-08-14T11:27:20Z (amd64/linux) |
| 漏洞库版本 | Version: 2 |
| 漏洞库 UpdatedAt | 2026-09-12T13:01:09Z |
| 漏洞库 NextUpdate | 2026-09-13T13:01:09Z |
| 漏洞库 DownloadedAt | 2026-09-12T16:47:08Z |
| 漏洞库实际来源 | docker.m.daocloud.io/aquasec/trivy-db:2（DaoCloud 公共镜像，兜底镜像站） |
| checks bundle | docker.m.daocloud.io/aquasec/trivy-checks:2（同样为兜底镜像站） |
| docker engine | 29.7.2 (API 1.55) |

### 1.1 镜像 digest（被扫描的实际制品）

| compose 服务 / image 引用 | RepoDigest |
|---|---|
| postgres:18 | postgres@sha256:4ef4dbc939d61acea57712655ddb4b4ab27419c913f94cca0cd57cb3ea3c2280 |
| clickhouse/clickhouse-server:26.7 | clickhouse/clickhouse-server@sha256:800e82865530eb2f1c4bc1b960e43b435fd9b2d83b4bd04a2564a5cfd88fdb6e |
| nats:2-alpine | nats@sha256:ad7a43eb7e3337c3c38ce5d784d1461791f95f730f252d2b25eee699752a0ca3 |
| minio/minio:latest | minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e |

原始行见 images/image-digests.txt

### 1.2 漏洞库（DB）获取过程 —— 必须显式声明的限制

本轮曾经两次失败，最终靠国内镜像站兜底：

1. TRY 1（失败）默认源 mirror.gcr.io/aquasec/trivy-db:2 → FATAL: dial tcp 192.178.163.82:443: connect: connection timed out（exit 1），耗时约 7 分钟。
   证据：raw/db-download-try1-ghcr.log、misconfig/trivy-config-run.log。
2. TRY 1b（失败）上游源 ghcr.io/aquasec/trivy-db:2 → GET https://ghcr.io/token?...: DENIED: requested access to the resource is denied（exit 1）。
   证据：raw/db-download-try1-ghcr.log。
3. TRY 2（成功）docker.m.daocloud.io/aquasec/trivy-db:2 → Artifact successfully downloaded（exit 0），112.93 MiB / 49s。
   证据：raw/db-download-try2-daocloud.log。

对 CVE 覆盖完整性的影响（如实声明）：

- 镜像站是 OCI 制品的 pull-through 缓存，DB 内容与上游 digest 一致，不是重新构建的库；
  下载后 trivy --version 回读的 UpdatedAt = 2026-09-12T13:01:09Z，即扫描当天（2026-09-12）的当日库，距下载时刻约 3.7 小时。
- 残余风险：镜像站缓存可能存在同步延迟。本轮无法独立校验 DB 的 digest 与上游是否逐字节一致
  （ghcr.io 不可达、mirror.gcr.io 超时，无法比对）。因此不能排除漏掉 2026-09-12 13:01Z 之后披露的极新 CVE。
- 该限制对 4 个镜像一视同仁，不影响镜像之间的横向比较。
- 另：trivy 对部分漏洞使用其他厂商的 severity
  （WARN Using severities from other vendors for some vulnerabilities），severity 分级可能与 NVD 原始分级不同。

## 2. 实测命令与退出码

完整清单见同目录 commands.txt。核心命令：

    # DB 兜底下载（成功那一版）
    sudo -n docker run --rm -v trivy-cache:/root/.cache/trivy aquasec/trivy:latest \
      image --download-db-only --db-repository docker.m.daocloud.io/aquasec/trivy-db \
      --cache-dir /root/.cache/trivy                                  # exit 0

    # misconfig（仓库根；仓库以只读方式挂载 :ro，证据目录单独挂载为可写）
    sudo -n docker run --rm --entrypoint /bin/sh \
      -v /home/euripides/projects/blockchain-platform:/w:ro \
      -v .../security/image-iac:/out -v trivy-cache:/root/.cache/trivy \
      aquasec/trivy:latest /out/raw/scan-misconfig-inner.sh           # exit 0，num=0

    # 镜像扫描（4 个镜像，JSON + table 各一次）
    trivy image --scanners vuln --image-src docker \
      --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL \
      --db-repository docker.m.daocloud.io/aquasec/trivy-db \
      --format json --output /out/images/NAME.json REF               # exit 0 x4

## 3. 镜像漏洞 —— severity 矩阵

镜像引用逐字取自 deploy/compose.yaml（第 3/20/43/56 行），未做任何假设；
4 个容器均在生产运行中（docker ps 显示 Up 3 days / Up 32 minutes）。

| 镜像 | CRITICAL | HIGH | MEDIUM | LOW | UNKNOWN | 合计 |
|---|---:|---:|---:|---:|---:|---:|
| postgres:18 | 14 | 101 | 154 | 143 | 3 | 415 |
| clickhouse/clickhouse-server:26.7 | 0 | 0 | 68 | 30 | 0 | 98 |
| nats:2-alpine | 0 | 2 | 8 | 12 | 1 | 23 |
| minio/minio:latest | 6 | 100 | 101 | 16 | 4 | 227 |
| 合计 | 20 | 203 | 331 | 201 | 8 | 763 |

计数口径：Results[].Vulnerabilities[] 逐条计数（同一 CVE 命中多个包 / 多个二进制会重复计数），
与 trivy table 输出行数口径一致。去重后的唯一 CVE 数见 3.1。

### 3.1 去重后的唯一 CVE 数

| 镜像 | 唯一 CRITICAL CVE | 唯一 HIGH CVE | HIGH/CRIT 中有修复版本 | 无修复版本 |
|---|---:|---:|---:|---:|
| postgres:18 | 5 | 42 | 58 | 57 |
| clickhouse/clickhouse-server:26.7 | 0 | 0 | 0 | 0 |
| nats:2-alpine | 0 | 1 | 2 | 0 |
| minio/minio:latest | 4 | 55 | 100 | 6 |

## 4. 全部 CRITICAL / HIGH 明细

### 4.1 postgres:18（CRITICAL 14 / HIGH 101）

| severity | CVE | 包 | 当前版本 | 修复版本 | 所在 target |
|---|---|---|---|---|---|
| CRITICAL | CVE-2026-13221 | libperl5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-42496 | libperl5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-8376 | libperl5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-6653 | libxml2 | 2.12.7+dfsg+really2.9.14-2.1+deb13u3 | NO-FIX | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-13221 | perl-base | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-42496 | perl-base | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-8376 | perl-base | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-13221 | perl-modules-5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-42496 | perl-modules-5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-8376 | perl-modules-5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-13221 | perl | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-42496 | perl | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2026-8376 | perl | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| CRITICAL | CVE-2025-68121 | stdlib | v1.24.6 | 1.24.13, 1.25.7, 1.26.0-rc.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-76642 | bsdutils | 1:2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78408 | bsdutils | 1:2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78409 | bsdutils | 1:2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78410 | bsdutils | 1:2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-24882 | dirmngr | 2.4.7-21+deb13u1+b4 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-24882 | gnupg-l10n | 2.4.7-21+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-24882 | gnupg | 2.4.7-21+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-24882 | gpg-agent | 2.4.7-21+deb13u1+b4 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-24882 | gpgconf | 2.4.7-21+deb13u1+b4 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-24882 | gpg | 2.4.7-21+deb13u1+b4 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-24882 | gpgsm | 2.4.7-21+deb13u1+b4 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-41992 | gzip | 1.13-1 | 1.13-1+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-54369 | libacl1 | 2.3.2-2+b1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-76642 | libblkid1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78408 | libblkid1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78409 | libblkid1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78410 | libblkid1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-76642 | liblastlog2-2 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78408 | liblastlog2-2 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78409 | liblastlog2-2 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78410 | liblastlog2-2 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-76642 | libmount1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78408 | libmount1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78409 | libmount1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78410 | libmount1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2025-69720 | libncursesw6 | 6.5+20250216-2 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-86145 | libpcre2-8-0 | 10.46-1~deb13u1 | 10.46-1~deb13u2 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-89161 | libpcre2-8-0 | 10.46-1~deb13u1 | 10.46-1~deb13u2 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-42497 | libperl5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-48962 | libperl5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-57432 | libperl5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-57433 | libperl5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-9538 | libperl5.40 | 5.40.1-6 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-76642 | libsmartcols1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78408 | libsmartcols1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78409 | libsmartcols1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78410 | libsmartcols1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-11822 | libsqlite3-0 | 3.46.1-7+deb13u1 | 3.46.1-7+deb13u2 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-11824 | libsqlite3-0 | 3.46.1-7+deb13u1 | 3.46.1-7+deb13u2 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-14456 | libssl3t64 | 3.5.6-1~deb13u2 | 3.5.7-1~deb13u2 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-16742 | libsystemd0 | 257.13-1~deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2025-69720 | libtinfo6 | 6.5+20250216-2 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-16742 | libudev1 | 257.13-1~deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-76642 | libuuid1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78408 | libuuid1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78409 | libuuid1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78410 | libuuid1 | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-74860 | libxml2 | 2.12.7+dfsg+really2.9.14-2.1+deb13u3 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-86140 | libxml2 | 2.12.7+dfsg+really2.9.14-2.1+deb13u3 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-76642 | login | 1:4.16.0-2+really2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78408 | login | 1:4.16.0-2+really2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78409 | login | 1:4.16.0-2+really2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78410 | login | 1:4.16.0-2+really2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-76642 | mount | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78408 | mount | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78409 | mount | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78410 | mount | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2025-69720 | ncurses-base | 6.5+20250216-2 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2025-69720 | ncurses-bin | 6.5+20250216-2 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-14456 | openssl-provider-legacy | 3.5.6-1~deb13u2 | 3.5.7-1~deb13u2 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-14456 | openssl | 3.5.6-1~deb13u2 | 3.5.7-1~deb13u2 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-42497 | perl-base | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-48962 | perl-base | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-57432 | perl-base | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-57433 | perl-base | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-9538 | perl-base | 5.40.1-6 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-42497 | perl-modules-5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-48962 | perl-modules-5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-57432 | perl-modules-5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-57433 | perl-modules-5.40 | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-9538 | perl-modules-5.40 | 5.40.1-6 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-42497 | perl | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-48962 | perl | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-57432 | perl | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-57433 | perl | 5.40.1-6 | 5.40.1-6+deb13u1 | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-9538 | perl | 5.40.1-6 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2025-61726 | stdlib | v1.24.6 | 1.24.12, 1.25.6 | usr/local/bin/gosu |
| HIGH | CVE-2025-61729 | stdlib | v1.24.6 | 1.24.11, 1.25.5 | usr/local/bin/gosu |
| HIGH | CVE-2026-25679 | stdlib | v1.24.6 | 1.25.8, 1.26.1 | usr/local/bin/gosu |
| HIGH | CVE-2026-27145 | stdlib | v1.24.6 | 1.25.11, 1.26.4 | usr/local/bin/gosu |
| HIGH | CVE-2026-32280 | stdlib | v1.24.6 | 1.25.9, 1.26.2 | usr/local/bin/gosu |
| HIGH | CVE-2026-32281 | stdlib | v1.24.6 | 1.25.9, 1.26.2 | usr/local/bin/gosu |
| HIGH | CVE-2026-32283 | stdlib | v1.24.6 | 1.25.9, 1.26.2 | usr/local/bin/gosu |
| HIGH | CVE-2026-33811 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-33814 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-33818 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-39820 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-39821 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-39822 | stdlib | v1.24.6 | 1.25.12, 1.26.5, 1.27.0-rc.2 | usr/local/bin/gosu |
| HIGH | CVE-2026-39836 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-42499 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-42504 | stdlib | v1.24.6 | 1.25.11, 1.26.4 | usr/local/bin/gosu |
| HIGH | CVE-2026-56853 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-56858 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-56859 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-56860 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-56862 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/local/bin/gosu |
| HIGH | CVE-2026-76642 | util-linux | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78408 | util-linux | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78409 | util-linux | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |
| HIGH | CVE-2026-78410 | util-linux | 2.41.5-0+deb13u1 | NO-FIX | postgres:18 (debian 13.6) |

### 4.2 clickhouse/clickhouse-server:26.7（CRITICAL 0 / HIGH 0）

无 CRITICAL/HIGH。

### 4.3 nats:2-alpine（CRITICAL 0 / HIGH 2）

| severity | CVE | 包 | 当前版本 | 修复版本 | 所在 target |
|---|---|---|---|---|---|
| HIGH | CVE-2026-14456 | libcrypto3 | 3.5.7-r0 | 3.5.8-r0 | nats:2-alpine (alpine 3.22.5) |
| HIGH | CVE-2026-14456 | libssl3 | 3.5.7-r0 | 3.5.8-r0 | nats:2-alpine (alpine 3.22.5) |

### 4.4 minio/minio:latest（CRITICAL 6 / HIGH 100）

| severity | CVE | 包 | 当前版本 | 修复版本 | 所在 target |
|---|---|---|---|---|---|
| CRITICAL | CVE-2026-33322 | github.com/minio/minio | v0.0.0-20250907161309-07c3a429bfed+dirty | NO-FIX | usr/bin/minio |
| CRITICAL | CVE-2026-33419 | github.com/minio/minio | v0.0.0-20250907161309-07c3a429bfed+dirty | NO-FIX | usr/bin/minio |
| CRITICAL | CVE-2026-33186 | google.golang.org/grpc | v1.71.0 | 1.79.3 | usr/bin/mc |
| CRITICAL | CVE-2026-33186 | google.golang.org/grpc | v1.72.0 | 1.79.3 | usr/bin/minio |
| CRITICAL | CVE-2025-68121 | stdlib | v1.24.6 | 1.24.13, 1.25.7, 1.26.0-rc.3 | usr/bin/mc |
| CRITICAL | CVE-2025-68121 | stdlib | v1.24.6 | 1.24.13, 1.25.7, 1.26.0-rc.3 | usr/bin/minio |
| HIGH | CVE-2026-41602 | github.com/apache/thrift | v0.21.0 | 0.23.0 | usr/bin/minio |
| HIGH | CVE-2026-43871 | github.com/apache/thrift | v0.21.0 | 0.24.0 | usr/bin/minio |
| HIGH | CVE-2026-32285 | github.com/buger/jsonparser | v1.1.1 | 1.1.2 | usr/bin/minio |
| HIGH | CVE-2026-34986 | github.com/go-jose/go-jose/v4 | v4.1.0 | 4.1.4 | usr/bin/minio |
| HIGH | CVE-2025-62506 | github.com/minio/minio | v0.0.0-20250907161309-07c3a429bfed+dirty | 0.0.0-20251015170045-c1a49490c78e | usr/bin/minio |
| HIGH | CVE-2026-34204 | github.com/minio/minio | v0.0.0-20250907161309-07c3a429bfed+dirty | NO-FIX | usr/bin/minio |
| HIGH | CVE-2026-39414 | github.com/minio/minio | v0.0.0-20250907161309-07c3a429bfed+dirty | NO-FIX | usr/bin/minio |
| HIGH | CVE-2026-40344 | github.com/minio/minio | v0.0.0-20250907161309-07c3a429bfed+dirty | NO-FIX | usr/bin/minio |
| HIGH | CVE-2026-41145 | github.com/minio/minio | v0.0.0-20250907161309-07c3a429bfed+dirty | NO-FIX | usr/bin/minio |
| HIGH | CVE-2026-42151 | github.com/prometheus/prometheus | v0.303.0 | 0.311.3 | usr/bin/mc |
| HIGH | CVE-2026-42151 | github.com/prometheus/prometheus | v0.303.0 | 0.311.3 | usr/bin/minio |
| HIGH | CVE-2026-42154 | github.com/prometheus/prometheus | v0.303.0 | 0.311.3, 0.305.2 | usr/bin/mc |
| HIGH | CVE-2026-42154 | github.com/prometheus/prometheus | v0.303.0 | 0.311.3, 0.305.2 | usr/bin/minio |
| HIGH | CVE-2026-79921 | github.com/rabbitmq/amqp091-go | v1.10.0 | 1.13.0 | usr/bin/minio |
| HIGH | CVE-2026-24051 | go.opentelemetry.io/otel/sdk | v1.35.0 | 1.40.0 | usr/bin/minio |
| HIGH | CVE-2026-39883 | go.opentelemetry.io/otel/sdk | v1.35.0 | 1.43.0 | usr/bin/minio |
| HIGH | CVE-2025-47913 | golang.org/x/crypto | v0.40.0 | 0.43.0 | usr/bin/mc |
| HIGH | CVE-2025-47913 | golang.org/x/crypto | v0.37.0 | 0.43.0 | usr/bin/minio |
| HIGH | CVE-2026-39828 | golang.org/x/crypto | v0.40.0 | 0.52.0 | usr/bin/mc |
| HIGH | CVE-2026-39828 | golang.org/x/crypto | v0.37.0 | 0.52.0 | usr/bin/minio |
| HIGH | CVE-2026-39829 | golang.org/x/crypto | v0.40.0 | 0.52.0 | usr/bin/mc |
| HIGH | CVE-2026-39829 | golang.org/x/crypto | v0.37.0 | 0.52.0 | usr/bin/minio |
| HIGH | CVE-2026-39830 | golang.org/x/crypto | v0.40.0 | 0.52.0 | usr/bin/mc |
| HIGH | CVE-2026-39830 | golang.org/x/crypto | v0.37.0 | 0.52.0 | usr/bin/minio |
| HIGH | CVE-2026-39831 | golang.org/x/crypto | v0.40.0 | 0.52.0 | usr/bin/mc |
| HIGH | CVE-2026-39831 | golang.org/x/crypto | v0.37.0 | 0.52.0 | usr/bin/minio |
| HIGH | CVE-2026-39832 | golang.org/x/crypto | v0.40.0 | 0.52.0 | usr/bin/mc |
| HIGH | CVE-2026-39832 | golang.org/x/crypto | v0.37.0 | 0.52.0 | usr/bin/minio |
| HIGH | CVE-2026-39835 | golang.org/x/crypto | v0.40.0 | 0.52.0 | usr/bin/mc |
| HIGH | CVE-2026-39835 | golang.org/x/crypto | v0.37.0 | 0.52.0 | usr/bin/minio |
| HIGH | CVE-2026-42508 | golang.org/x/crypto | v0.40.0 | 0.52.0 | usr/bin/mc |
| HIGH | CVE-2026-42508 | golang.org/x/crypto | v0.37.0 | 0.52.0 | usr/bin/minio |
| HIGH | CVE-2026-46595 | golang.org/x/crypto | v0.40.0 | 0.52.0 | usr/bin/mc |
| HIGH | CVE-2026-46595 | golang.org/x/crypto | v0.37.0 | 0.52.0 | usr/bin/minio |
| HIGH | CVE-2026-46597 | golang.org/x/crypto | v0.40.0 | 0.52.0 | usr/bin/mc |
| HIGH | CVE-2026-46597 | golang.org/x/crypto | v0.37.0 | 0.52.0 | usr/bin/minio |
| HIGH | CVE-2026-56854 | golang.org/x/crypto | v0.40.0 | 0.55.0 | usr/bin/mc |
| HIGH | CVE-2026-56854 | golang.org/x/crypto | v0.37.0 | 0.55.0 | usr/bin/minio |
| HIGH | CVE-2026-25681 | golang.org/x/net | v0.42.0 | 0.55.0 | usr/bin/mc |
| HIGH | CVE-2026-25681 | golang.org/x/net | v0.39.0 | 0.55.0 | usr/bin/minio |
| HIGH | CVE-2026-27136 | golang.org/x/net | v0.42.0 | 0.55.0 | usr/bin/mc |
| HIGH | CVE-2026-27136 | golang.org/x/net | v0.39.0 | 0.55.0 | usr/bin/minio |
| HIGH | CVE-2026-33814 | golang.org/x/net | v0.42.0 | 0.53.0 | usr/bin/mc |
| HIGH | CVE-2026-33814 | golang.org/x/net | v0.39.0 | 0.53.0 | usr/bin/minio |
| HIGH | CVE-2026-39821 | golang.org/x/net | v0.42.0 | 0.55.0 | usr/bin/mc |
| HIGH | CVE-2026-39821 | golang.org/x/net | v0.39.0 | 0.55.0 | usr/bin/minio |
| HIGH | CVE-2026-46600 | golang.org/x/net | v0.42.0 | 0.56.0 | usr/bin/mc |
| HIGH | CVE-2026-46600 | golang.org/x/net | v0.39.0 | 0.56.0 | usr/bin/minio |
| HIGH | CVE-2026-56852 | golang.org/x/text | v0.27.0 | 0.39.0 | usr/bin/mc |
| HIGH | CVE-2026-56852 | golang.org/x/text | v0.24.0 | 0.39.0 | usr/bin/minio |
| HIGH | CVE-2026-84304 | google.golang.org/grpc | v1.71.0 | 1.83.1 | usr/bin/mc |
| HIGH | CVE-2026-84304 | google.golang.org/grpc | v1.72.0 | 1.83.1 | usr/bin/minio |
| HIGH | CVE-2026-84445 | google.golang.org/grpc | v1.71.0 | 1.82.2, 1.83.2, 1.85.0-dev.0.20260825072537-93e31b48545e | usr/bin/mc |
| HIGH | CVE-2026-84445 | google.golang.org/grpc | v1.72.0 | 1.82.2, 1.83.2, 1.85.0-dev.0.20260825072537-93e31b48545e | usr/bin/minio |
| HIGH | GHSA-hrxh-6v49-42gf | google.golang.org/grpc | v1.71.0 | 1.82.1 | usr/bin/mc |
| HIGH | GHSA-hrxh-6v49-42gf | google.golang.org/grpc | v1.72.0 | 1.82.1 | usr/bin/minio |
| HIGH | CVE-2026-54369 | libacl | 2.3.1-4.el9 | 2.4.0-1.el9_8 | minio/minio:latest (redhat 9.6) |
| HIGH | CVE-2026-4878 | libcap | 2.48-9.el9_2 | 2.48-10.el9_8.1 | minio/minio:latest (redhat 9.6) |
| HIGH | CVE-2025-61726 | stdlib | v1.24.6 | 1.24.12, 1.25.6 | usr/bin/mc |
| HIGH | CVE-2025-61726 | stdlib | v1.24.6 | 1.24.12, 1.25.6 | usr/bin/minio |
| HIGH | CVE-2025-61729 | stdlib | v1.24.6 | 1.24.11, 1.25.5 | usr/bin/mc |
| HIGH | CVE-2025-61729 | stdlib | v1.24.6 | 1.24.11, 1.25.5 | usr/bin/minio |
| HIGH | CVE-2026-25679 | stdlib | v1.24.6 | 1.25.8, 1.26.1 | usr/bin/mc |
| HIGH | CVE-2026-25679 | stdlib | v1.24.6 | 1.25.8, 1.26.1 | usr/bin/minio |
| HIGH | CVE-2026-27145 | stdlib | v1.24.6 | 1.25.11, 1.26.4 | usr/bin/mc |
| HIGH | CVE-2026-27145 | stdlib | v1.24.6 | 1.25.11, 1.26.4 | usr/bin/minio |
| HIGH | CVE-2026-32280 | stdlib | v1.24.6 | 1.25.9, 1.26.2 | usr/bin/mc |
| HIGH | CVE-2026-32280 | stdlib | v1.24.6 | 1.25.9, 1.26.2 | usr/bin/minio |
| HIGH | CVE-2026-32281 | stdlib | v1.24.6 | 1.25.9, 1.26.2 | usr/bin/mc |
| HIGH | CVE-2026-32281 | stdlib | v1.24.6 | 1.25.9, 1.26.2 | usr/bin/minio |
| HIGH | CVE-2026-32283 | stdlib | v1.24.6 | 1.25.9, 1.26.2 | usr/bin/mc |
| HIGH | CVE-2026-32283 | stdlib | v1.24.6 | 1.25.9, 1.26.2 | usr/bin/minio |
| HIGH | CVE-2026-33811 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/bin/mc |
| HIGH | CVE-2026-33811 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/bin/minio |
| HIGH | CVE-2026-33814 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/bin/mc |
| HIGH | CVE-2026-33814 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/bin/minio |
| HIGH | CVE-2026-33818 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/mc |
| HIGH | CVE-2026-33818 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/minio |
| HIGH | CVE-2026-39820 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/bin/mc |
| HIGH | CVE-2026-39820 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/bin/minio |
| HIGH | CVE-2026-39821 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/mc |
| HIGH | CVE-2026-39821 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/minio |
| HIGH | CVE-2026-39822 | stdlib | v1.24.6 | 1.25.12, 1.26.5, 1.27.0-rc.2 | usr/bin/mc |
| HIGH | CVE-2026-39822 | stdlib | v1.24.6 | 1.25.12, 1.26.5, 1.27.0-rc.2 | usr/bin/minio |
| HIGH | CVE-2026-39836 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/bin/mc |
| HIGH | CVE-2026-39836 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/bin/minio |
| HIGH | CVE-2026-42499 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/bin/mc |
| HIGH | CVE-2026-42499 | stdlib | v1.24.6 | 1.25.10, 1.26.3 | usr/bin/minio |
| HIGH | CVE-2026-42504 | stdlib | v1.24.6 | 1.25.11, 1.26.4 | usr/bin/mc |
| HIGH | CVE-2026-42504 | stdlib | v1.24.6 | 1.25.11, 1.26.4 | usr/bin/minio |
| HIGH | CVE-2026-56853 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/mc |
| HIGH | CVE-2026-56853 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/minio |
| HIGH | CVE-2026-56858 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/mc |
| HIGH | CVE-2026-56858 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/minio |
| HIGH | CVE-2026-56859 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/mc |
| HIGH | CVE-2026-56859 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/minio |
| HIGH | CVE-2026-56860 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/mc |
| HIGH | CVE-2026-56860 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/minio |
| HIGH | CVE-2026-56862 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/mc |
| HIGH | CVE-2026-56862 | stdlib | v1.24.6 | 1.25.13, 1.26.6, 1.27.0-rc.3 | usr/bin/minio |

## 5. misconfig —— 真实覆盖结论（关键）

### 5.1 trivy 实际扫到了什么

trivy config /w（仓库根）与 trivy config /w/deploy 的输出：

    INFO  [misconfig] Misconfiguration scanning is enabled
    INFO  [checks-client] Using existing checks from cache  path=/root/.cache/trivy/policy/content
    INFO  Detected config files  num=0          <-- deploy/ 下同样是 0
    WARN  [report] Supported files for scanner(s) not found.  scanners=[misconfig]

misconfig/trivy-config.json 全文（没有 Results 字段）：

    {
      "SchemaVersion": 2,
      "Trivy": { "Version": "0.74.0" },
      "ReportID": "01a09684-7d0d-7dfc-839e-443a4d45f171",
      "CreatedAt": "2026-09-12T16:47:40.301919471Z",
      "ArtifactName": "/w",
      "ArtifactType": "filesystem"
    }

misconfig/trivy-config-deploy.txt 表格明写 Target: - / Type: - / Misconfigurations: -，
图例注明 - 表示 Not scanned（未扫描），0 才表示 Clean。

结论：本轮 trivy misconfig 的 severity 计数全部无意义 —— 不是 0 个问题，而是 0 个文件进入扫描。
因此 misconfig 的 severity 分布为：CRITICAL/HIGH/MEDIUM/LOW 均不可用（N/A），规则 ID（DS002 等）无任何触发。

### 5.2 根因：trivy 对这三类文件结构性不支持

用受控自测（故意写坏的样例）验证，证据 raw/trivy-selftest-compose.log：

| 被测文件 | 是否被 trivy 识别 | 说明 |
|---|---|---|
| plain/Dockerfile | 识别 | 报出 DS-0002(HIGH)/DS-0004(MEDIUM)/DS-0026(LOW)/DS-0029(HIGH)/DS-0031(CRITICAL) |
| plain/k8s.yaml | 识别 | 报出 KSV-0001/0003/0004/0009/0011/0012/0013/0020/0021/0030/0104/0106/0110/0118 等 19 项 |
| plain/compose.yaml | 未识别 | 未出现在任何 target 中 |
| plain/docker-compose.yaml | 未识别 | 未出现在任何 target 中 |

对 checks bundle 本体取证（raw/trivy-checksbundle-compose-grep.log）：

- bundle 内 rego 策略共 580 个；顶层 policies/ 只有 cloud / docker / kubernetes / test —— 没有 compose 命名空间；
- 全库 grep -ril "compose|docker-compose|docker_compose" → 0 命中；
- 换用更老的策略包 aquasec/trivy-checks:1 重跑，target 仍只有 Dockerfile + k8s.yaml
  （raw/trivy-compose-coverage-probe.log）。

结论：trivy 无 docker-compose 检查项，非配置问题、非版本问题、无法通过参数绕过。

仓库侧事实（raw/dockerfiles-list.txt 为空、raw/compose-images.txt、raw/nginx-conf.txt、raw/systemd-units.txt）：

- 无任何 Dockerfile（全仓库 find -name Dockerfile* 结果为空）；无 k8s / helm / terraform / ansible 制品；
- 唯一的 IaC 制品是 deploy/compose.yaml；另有 6 个 deploy/*.service 与 deploy/nginx-blockchain-platform.conf。
- 这些恰好全部落在 trivy config 支持范围之外，结果必然是 num=0。

补充：哪些文件被扫了 / 哪些没被扫

| 文件类别 | 具体文件 | 是否被 trivy config 扫描 |
|---|---|---|
| Dockerfile | 无（仓库不存在） | N/A |
| docker-compose | deploy/compose.yaml | 否（工具不支持） |
| systemd unit | deploy/*.service（6 个） | 否（工具不支持） |
| nginx conf | deploy/nginx-blockchain-platform.conf | 否（工具不支持） |
| k8s / helm / terraform / ansible / cloudformation / azure-arm | 无 | N/A |

### 5.3 替代覆盖尝试（如实报告：全部失败或未完成）

| 手段 | 命令 | 结果 |
|---|---|---|
| checkov 3.3.17（bridgecrew/checkov:latest, digest sha256:41c4701c6a56d8952e5aba7a420f871c8b70b57da94eb4f142dcdf7295bb0be3） | checkov -d /w/deploy -o json | exit 0 但 resource_count: 0 / failed: 0 / passed: 0 —— 同样未识别 compose.yaml。自测中 checkov 也只识别 Dockerfile 与 k8s.yaml。证据 misconfig/checkov-compose.json、raw/checkov-supplementary.log |
| checkov 换名重试 | 复制为 docker-compose.yml / compose.yml 再扫 | 仍 resource_count: 0 |
| KICS（checkmarx/kics:latest，本应原生支持 docker-compose） | kics scan -p /w/deploy ... | NOT_VERIFIED —— 镜像拉取阶段超出执行窗口被杀，未产出任何结果（/tmp/kics-real 不存在）。未完成，不能据此下任何结论。 |
| 人工核对 | 逐行阅读 deploy/compose.yaml、6 个 .service、nginx conf | 已完成阅读但未产出结构化核对表；原始文本已存证 raw/compose.yaml.txt、raw/systemd-units.txt、raw/nginx-conf.txt。NOT_VERIFIED（未形式化）。 |

结论：IaC 配置面的结论是 NOT_VERIFIED，不是 PASS。请勿把 num=0 记为「无配置问题」。

### 5.4 人工阅读中已可直接观察、但未经工具背书的点（仅作线索，非工具结论）

以下几项是阅读 deploy/compose.yaml、nginx conf、.service 时肉眼可见的事实，未经自动化规则校验，
列此仅为后续补扫提供线索：

- minio/minio:latest 使用可变 tag（latest），无法固定制品 digest，供应链可复现性弱；
- 4 个数据面镜像均未声明 cap_drop / read_only / security_opt / no-new-privileges 等加固项；
- nginx-blockchain-platform.conf 的 CSP 头只加在 /admin/ 与 /app-v2/ 两个 location，
  根路径 /（转发 3000）与 /api/ 代理段未加 CSP / X-Content-Type-Options / Referrer-Policy；
- blockchain-vite.service 以 --host 0.0.0.0 监听（其余服务均为 127.0.0.1）；
- 6 个 .service 单元未见 NoNewPrivileges / ProtectSystem / PrivateTmp 等 systemd 沙箱硬化项。

## 6. P0 / P1 计数与处置结论

分级口径（本轮自定义，供 S7 台账使用）：

- P0 = CRITICAL 镜像漏洞（4 个容器均在产线运行，且均为数据面组件）
- P1 = HIGH 镜像漏洞
- P0-配置面 = IaC 配置未覆盖 —— 单列，因为它是证据缺口而非已确认缺陷

| 级别 | 数量 | 说明 |
|---|---:|---|
| P0（CRITICAL） | 20 | postgres:18 → 14；minio:latest → 6；clickhouse / nats → 0 |
| P1（HIGH） | 203 | postgres:18 → 101；minio:latest → 100；nats:2-alpine → 2；clickhouse → 0 |
| P0-配置面 | 1 项 | trivy misconfig 覆盖 = 0 文件；checkov 同样不识别 compose；KICS 未验证 → 配置面证据缺失 |

### 处置结论

1. postgres:18 与 minio:latest 是本轮风险主体（CRITICAL 20 全部落在这两个镜像；HIGH 201/203 也在这两个）。
   两者都是基础镜像层过期，不是本项目自研代码引入的问题。
2. postgres:18 的 14 条 CRITICAL 高度集中：perl / perl-base / perl-modules-5.40 / libperl5.40 各 3 条
   （CVE-2026-13221 / CVE-2026-42496 / CVE-2026-8376），共享同一修复版本 5.40.1-6+deb13u1；
   另有 libxml2 CVE-2026-6653（NO-FIX）与 gosu 内嵌 Go stdlib CVE-2025-68121（修复版 1.24.13+）。
   建议：重新拉取 postgres:18 或升级 debian 点版本，可一次性清掉大部分。
3. minio 的 CRITICAL 中 2 条是 MinIO 自身（CVE-2026-33322 / CVE-2026-33419，均 NO-FIX），
   其余为内嵌 Go 依赖（grpc CVE-2026-33186、stdlib CVE-2025-68121）。
   生产用 latest 且镜像构建于 2025-09-07（CreatedAt 2025-09-07T18:42:37Z，已近一年未更新）。
   建议：固定版本并升级 MinIO 发行版，停止使用 latest。
4. nats:2-alpine 仅 2 条 HIGH，且同一 CVE（CVE-2026-14456，OpenSSL）修复版本明确（3.5.8-r0），低代价可修。
5. clickhouse:26.7 无 CRITICAL/HIGH（68 MEDIUM / 30 LOW），当前 tag 相对健康。
6. P0-配置面必须先补扫（KICS 或等价的 compose 专用扫描器），否则 S7 的 IaC 门禁无法关闭。
   在此之前，不得把 image-iac/misconfig/* 的 0 结果当作配置基线通过。

## 7. NOT_VERIFIED 清单（未验证项）

| # | 项 | 原因 |
|---|---|---|
| 1 | IaC/配置面 misconfig（compose.yaml、6 个 systemd unit、nginx conf） | trivy 与 checkov 均不识别 docker-compose / systemd / nginx；KICS 未能跑完 → 工具未覆盖，结论缺失 |
| 2 | KICS 兜底扫描 | checkmarx/kics:latest 拉取超时被杀，无任何输出；/tmp/kics-real、/tmp/kics-selftest 均不存在 |
| 3 | 漏洞库 digest 与上游一致性 | ghcr.io 匿名 token 被拒（DENIED）、mirror.gcr.io 与 gcr.io TCP 超时，无法与上游逐字节比对 DB → 镜像站缓存同步延迟导致的漏报无法排除 |
| 4 | 2026-09-12 13:01Z 之后披露的 CVE | 受上一条限制，极新 CVE 可能缺失 |
| 5 | secret 扫描 | 本轮有意只跑 vuln 与 misconfig，未跑 secret 扫描（避免把 deploy/.env 内容写入证据）；密钥泄露面需由专项任务覆盖 |
| 6 | 镜像层文件级合规（suid、world-writable 等） | 不在本轮范围 |
| 7 | mobsf 容器（opensecurity/mobile-security-framework-mobsf:latest） | 与 compose 无关、非本项目制品，按要求未扫 |
| 8 | 运行时/动态验证 | 本轮为静态镜像扫描，不涉及运行时行为 |

## 8. 证据文件清单（绝对路径）

根目录：/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/image-iac/

| 文件 | 内容 |
|---|---|
| SUMMARY.md | 本文件 |
| commands.txt | 每条命令 + 退出码 + DB 时间 |
| trivy-version.txt | trivy 版本、镜像 digest、创建时间 |
| misconfig/trivy-config.json | 仓库根 misconfig 原始 JSON（无 Results → num=0） |
| misconfig/trivy-config.txt | 仓库根 misconfig table |
| misconfig/trivy-config-deploy.json / .txt | deploy/ 定向 misconfig（同为 0） |
| misconfig/trivy-config-run.log | misconfig 全过程日志（含 num=0 与失败重试） |
| misconfig/trivy-config-help.txt | trivy config --help（证明 0.74.0 无 --scanners，需用 --misconfig-scanners） |
| misconfig/checkov-compose.json / .txt | checkov 兜底结果（resource_count 0） |
| misconfig/checkov-repo-dockerfile.json | checkov 仓库根结果（0） |
| images/postgres-18.json | postgres:18 原始漏洞 JSON（14 CRIT / 101 HIGH / 合计 415） |
| images/postgres-18.txt | postgres:18 table 摘要 |
| images/clickhouse-clickhouse-server-26.7.json | clickhouse/clickhouse-server:26.7 原始漏洞 JSON（0 CRIT / 0 HIGH / 合计 98） |
| images/clickhouse-clickhouse-server-26.7.txt | clickhouse/clickhouse-server:26.7 table 摘要 |
| images/nats-2-alpine.json | nats:2-alpine 原始漏洞 JSON（0 CRIT / 2 HIGH / 合计 23） |
| images/nats-2-alpine.txt | nats:2-alpine table 摘要 |
| images/minio-minio-latest.json | minio/minio:latest 原始漏洞 JSON（6 CRIT / 100 HIGH / 合计 227） |
| images/minio-minio-latest.txt | minio/minio:latest table 摘要 |
| images/trivy-images-run.log | 4 个镜像扫描全过程（含逐条退出码） |
| images/image-digests.txt | 4 个镜像的 RepoDigest / ImageId / CreatedAt |
| raw/compose.yaml.txt | deploy/compose.yaml 原文（镜像 tag 依据） |
| raw/compose-images.txt | 从 compose 提取的 image 行（含行号） |
| raw/dockerfiles-list.txt | Dockerfile 查找结果（空） |
| raw/systemd-units.txt | 6 个 .service 原文 |
| raw/nginx-conf.txt | nginx conf 原文 |
| raw/db-download-try1-ghcr.log | DB 下载 TRY1/TRY1b 失败原文 |
| raw/db-download-try2-daocloud.log | DB 下载 TRY2 成功原文 |
| raw/trivy-selftest-compose.log | 受控自测：Dockerfile/k8s 被识别、compose 未被识别 |
| raw/trivy-compose-coverage-probe.log | checks bundle 与 :1 版本探测 |
| raw/trivy-checksbundle-compose-grep.log | bundle 内 compose 关键字 grep（0 命中） |
| raw/checkov-supplementary.log | checkov 兜底全过程 |
| raw/summary.json | 机器可读汇总（全部镜像 + misconfig 结构化数据） |
| raw/summary.txt | 汇总脚本的终端输出 |
| raw/summarize.cjs / raw/gen-summary.cjs | 统计与本文生成脚本 |
| raw/scan-images-inner.sh / raw/scan-misconfig-inner.sh | 容器内实际执行脚本 |

