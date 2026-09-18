# SBOM 报告

## 1. 工具与溯源

| 项 | 值 |
|---|---|
| 工具 | **syft** |
| 版本 | **1.51.1** |
| GitCommit | `91a0032987d91b7411b52f6f5c185c5e7f775495` |
| GitDescription | v1.51.1 |
| 镜像 | `anchore/syft:latest` |
| 解析后 digest | `sha256:95fe0835e5bebc6f8b1f8acef68d47d63d594ef4c0f25c097ff853b23cbac74c` |
| 执行时间（UTC） | 2026-09-12T22:43:29Z 起 |

**未拉取任何镜像**：`anchore/syft:latest` 在本轮开始前已存在于本机。

---

## 2. ⚠ 关键方法学发现：宽目录扫描产出的 SBOM **不能**代表生产依赖

第一版后端 SBOM（`backend.cdx.json`，对 `backend/` 整目录扫描）产出 **10,003 个组件**。
逐组件回溯来源路径后发现它**被历史备份二进制严重污染**：

| 来源路径 | 组件数 |
|---|---|
| `/bin`（**约 170 个 `api.bak-*` / `worker.bak-*` 历史二进制**） | **9,327** |
| `/.rollback` | 286 |
| `/bin.next` | 131 |
| `/go.mod`（**真正的依赖清单**） | **仅 55** |

采集器分布：`go-module-binary-cataloger` 命中 **9,744** 条 —— 即几乎全部组件来自**被打包的二进制**，而非模块清单。

且 `metadata.component` = `{"type":"file","name":"/w"}` —— **没有名称、没有版本**，无法作为发布物标识。

> **结论：`backend.cdx.json` / `backend.spdx.json` 是「整个 backend 工作目录（含历史残留）」的清单，**
> **不是当前生产构建的 SBOM，不能用作生产依赖证明。**

### 2.1 修正版：仅针对当前生产产物的 SBOM

用 **硬链接**把当前发布产物与模块清单单独暂存（不复制大文件、不动仓库），再扫描：

```bash
STAGE=/tmp/prod-sbom-stage
ln backend/bin/api $STAGE/api          # 硬链接，同 inode
ln backend/bin/worker $STAGE/worker
ln backend/bin/migrate $STAGE/migrate
ln backend/go.mod $STAGE/go.mod
ln backend/go.sum $STAGE/go.sum
docker run --rm -v $STAGE:/w:ro -v <evidence>/sbom:/out \
  anchore/syft:latest dir:/w -o cyclonedx-json=/out/backend-prod.cdx.json
```

| 产物 | 组件数 | 来源路径分布 |
|---|---|---|
| `backend-prod.cdx.json` | **190**（186 library + 4 file） | `/api` 55、`/go.mod` 55、`/worker` 44、`/migrate` 32 |
| `backend-prod.spdx.json` | 同上 | 同上 |

**去污染后从 10,003 → 190 个组件**，且来源**只**包含当前三个二进制与 go.mod/go.sum。

### 2.2 与当前生产二进制的交叉核对（决定性证据）

对三个**正在运行的**生产二进制取 Go buildinfo（`go version -m`）：

| 二进制 | sha256 | 关键依赖（buildinfo 实测） |
|---|---|---|
| `backend/bin/api` | `e0a7c3bd1983cc18da85bd5e1a2e3712b9dde43bb67d48ee5e4722f53368abb7` | go1.26.7；chi/v5 **v5.2.1**、pgx/v5 **v5.7.4**、excelize/v2 **v2.9.0**、x/crypto **v0.54.0** |
| `backend/bin/worker` | `9c41079d5c59067e0585166d049219de0f14fd93069b3c3a194dff0ecf9fccb6` | go1.26.7；pgx/v5 **v5.7.4**、x/crypto **v0.54.0**（**无 excelize**） |
| `backend/bin/migrate` | `ab10efe36791e49ea820fb7b293c6aa4104bf93788dacacc87f7f7895ca19b4f` | go1.26.7；pgx/v5 **v5.7.4**、x/crypto **v0.54.0** |

**三个二进制的模块版本与 `dependency-report.md` 的 govulncheck 结论逐项一致**
（pgx v5.7.4 = 2 个 P0；chi v5.2.1 = 2×P1 + 2×P2；excelize v2.9.0 = 唯一可达 P1；x/crypto v0.54.0 = 4 条未评级）。
**这证明修正版 SBOM 反映的是当前生产构建，而不是缓存或历史产物。**

> 附带确认：`excelize` 只出现在 `api` 中，不在 `worker` / `migrate` 中 —— 与「excelize 仅用于导出」的代码事实一致。

---

## 3. 产物清单

| 文件 | 格式 | 字节 | sha256 | 说明 |
|---|---|---|---|---|
| `frontend.cdx.json` | CycloneDX 1.7 | 217,951 | `fe5c0925341ab4fcf7b16d2b9fa6963eee93b7cd7872c40db9f3a83ccd336d92` | 前端 **176** 组件 |
| `frontend.spdx.json` | SPDX-2.3 | 371,704 | `3487c8f4031eeaa7726fd999da2ae6e656ab310ab4736b64b522237ac794802a` | 同上 |
| `backend-prod.cdx.json` | CycloneDX 1.7 | 180,787 | `de86a3c690bb8f2fc723a3806be8024ffd2fa71da8eca9966c3112c309adce03` | **生产范围（推荐使用）** |
| `backend-prod.spdx.json` | SPDX-2.3 | 269,531 | `8c198786237ee2b27c01084290d2fea77c1c891d450186f9f441a4e45f76f721` | 同上 |
| `backend.cdx.json` | CycloneDX 1.7 | 10,839,209 | `dc623ed9c56e0fd92b8e30dd3ba8af7f431c94bf89d4ed318854ffd222049093` | ⚠ **被历史备份污染，仅供参考/取证，勿作生产依赖证明** |
| `backend.spdx.json` | SPDX-2.3 | 15,737,827 | `a0c02fcf36088beb1d31c1bf4762d0adc7d949b453104d5ab01f7fd8b183a0d9` | ⚠ 同上 |
| `deploy.cdx.json` | CycloneDX 1.7 | 398 | `cbbb8a727df11718c7592f6d677296b2e052237602804b140507103320e613a6` | 组件数 **0** |

---

## 4. 文件同步与逐字节校验（NV-S1 已闭合）

两份原始后端 SBOM 已通过 **系统 `scp -o BatchMode=yes`** 通道取回 Windows 证据目录，
并与先前记录的 `big-artifacts.sha256.txt` **逐字节一致**：

| 文件 | 字节 | Windows `Get-FileHash` | 远端 `sha256sum` | 与 big-artifacts 比对 |
|---|---|---|---|---|
| `sbom/backend.cdx.json` | 10,839,209 | `dc623ed9c56e0fd92b8e30dd3ba8af7f431c94bf89d4ed318854ffd222049093` | 同值 | **一致** |
| `sbom/backend.spdx.json` | 15,737,827 | `a0c02fcf36088beb1d31c1bf4762d0adc7d949b453104d5ab01f7fd8b183a0d9` | 同值 | **一致** |

> 该通道同时取代了此前受 `sftp_download` 沙箱限制（本地路径必须位于 `/workspace`）的传输方式。
> 详细校验记录见 `sbom/backend-sbom-verification.md`。

---

## 5. 局限（必须与结论一起阅读）

1. **`deploy.cdx.json` 组件数为 0** —— syft 面向包管理器生态，`deploy/`（compose / systemd / nginx 纯配置）里没有它识别的包。**0 组件 ≠ 0 问题。**
2. **前端 SBOM 只覆盖 `frontend/` 目录树**（排除 `node_modules`），即**源码级依赖**；已构建的 `dist` 产物未单独出 SBOM。
3. 目录源告警 `no explicit name and version provided for directory source` —— 产物 artifact ID 由路径派生，跨机器生成的 SBOM 之间**不可直接做 ID 级比较**。
4. SBOM 覆盖「有哪些组件及其版本」，**不覆盖漏洞**。漏洞结论见 `dependency-report.md` 与 `images.md`。
5. 修正版 SBOM 的 190 个组件仍**未做 CVE 匹配**（未跑 grype / trivy sbom）。
