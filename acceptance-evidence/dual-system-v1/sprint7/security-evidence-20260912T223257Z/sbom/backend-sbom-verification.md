# 后端 SBOM 传输与逐字节校验记录

本文件闭合先前的 **NV-S1**（曾记为「约 26 MB 超过传输沙箱额度」）。该记录**已撤回**：
监控通道验证了系统 `scp -o BatchMode=yes` 免密可用，两份原始 SBOM 已按该通道取回本证据目录。

## 1. 传输命令（实际执行）

```bash
scp -o BatchMode=yes -o StrictHostKeyChecking=no \
  euripides@192.168.31.152:/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z/sbom/backend.cdx.json \
  "E:\Code\远程虚拟机\harness\blockchain-platform\acceptance-evidence\dual-system-v1\sprint7\security-evidence-20260912T223257Z\sbom\backend.cdx.json"        # exit 0
scp -o BatchMode=yes -o StrictHostKeyChecking=no \
  euripides@192.168.31.152:/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z/sbom/backend.spdx.json \
  "E:\Code\远程虚拟机\harness\blockchain-platform\acceptance-evidence\dual-system-v1\sprint7\security-evidence-20260912T223257Z\sbom\backend.spdx.json"       # exit 0
```

两次 `scp` **退出码均为 0**。

## 2. 远端 sha256sum（VM 侧实测）

```text
remote_utc=2026-09-12T22:59:27Z
--- sha256sum (remote) ---
dc623ed9c56e0fd92b8e30dd3ba8af7f431c94bf89d4ed318854ffd222049093  sbom/backend.cdx.json
a0c02fcf36088beb1d31c1bf4762d0adc7d949b453104d5ab01f7fd8b183a0d9  sbom/backend.spdx.json
de86a3c690bb8f2fc723a3806be8024ffd2fa71da8eca9966c3112c309adce03  sbom/backend-prod.cdx.json
8c198786237ee2b27c01084290d2fea77c1c891d450186f9f441a4e45f76f721  sbom/backend-prod.spdx.json
--- stat ---
sbom/backend.cdx.json 10839209 bytes
sbom/backend.spdx.json 15737827 bytes
--- vs big-artifacts.sha256.txt ---
sbom/backend.cdx.json: OK
sbom/backend.spdx.json: OK
images/clickhouse-clickhouse-server-26.7.json: OK
images/minio-minio-latest.json: OK
images/nats-2-alpine.json: OK
images/postgres-18.json: OK
```

## 3. 本地 Get-FileHash（Windows 侧实测）

```text
local_utc=2026-09-12T22:59:20Z
--- Get-FileHash (windows) ---
dc623ed9c56e0fd92b8e30dd3ba8af7f431c94bf89d4ed318854ffd222049093  sbom/backend.cdx.json
a0c02fcf36088beb1d31c1bf4762d0adc7d949b453104d5ab01f7fd8b183a0d9  sbom/backend.spdx.json
de86a3c690bb8f2fc723a3806be8024ffd2fa71da8eca9966c3112c309adce03  sbom/backend-prod.cdx.json
8c198786237ee2b27c01084290d2fea77c1c891d450186f9f441a4e45f76f721  sbom/backend-prod.spdx.json
--- file sizes ---
backend.cdx.json  10839209 bytes
backend.spdx.json  15737827 bytes
```

## 4. 比对结论

| 文件 | 远端字节 | Windows 字节 | 远端 sha256 | Windows sha256 | 与 `big-artifacts.sha256.txt` | 结论 |
|---|---|---|---|---|---|---|
| `sbom/backend.cdx.json` | 10,839,209 | 10,839,209 | `dc623ed9…049093` | `dc623ed9…049093` | 一致 | ✅ **逐字节相同** |
| `sbom/backend.spdx.json` | 15,737,827 | 15,737,827 | `a0c02fcf…83a0d9` | `a0c02fcf…83a0d9` | 一致 | ✅ **逐字节相同** |

`sha256sum -c big-artifacts.sha256.txt` 在 VM 侧对上述两项返回 **OK**。

## 5. 说明

- 这两份文件是**未去污染**的宽目录 SBOM（10,003 组件，含历史备份二进制），其定位见 `sbom/README.md` §2：
  **仅作取证与对比，不得用作生产依赖证明**；生产范围请用 `backend-prod.cdx.json`（190 组件）。
- 之所以仍完整取回，是为了让「污染」这一结论本身可被独立复核。
