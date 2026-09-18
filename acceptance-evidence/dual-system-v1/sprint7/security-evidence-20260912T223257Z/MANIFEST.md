# MANIFEST — 证据目录逐文件 sha256

- 证据目录：`acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z/`
- Windows 树：`E:\Code\远程虚拟机\harness\blockchain-platform\acceptance-evidence\dual-system-v1\sprint7\security-evidence-20260912T223257Z`
- VM 同名目录：`/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z`
- 文件总数：**135**；总字节：**37520883**
- 完整清单：`SHA256SUMS.windows.txt`（本文件为其人读摘要）
- 采集窗口（UTC）：2026-09-12T22:32:57Z – **23:00:12Z**（实际最后一次采集时刻）

## 交付物（顶层）

| 文件 | 字节 | sha256 |
|---|---|---|
| `big-artifacts.sha256.txt` | 568 | `5ecdf3d52c4c98af4961a4e77cd0f1c42bc6f9404b4534d4454b2e722b5778b7` |
| `dependency-inventory.json` | 15866 | `30b0031914743c812ee4b4bbb852339873904bafb26e93fbb5119d329f92ba2c` |
| `dependency-report.md` | 12091 | `482b462439495de9c8ba70881c14e0f986717792eeaa4812585a088e86a83f3d` |
| `iac-coverage.md` | 13702 | `e305efb401edbfcd07ad6af86fde64349eda1c4a6e32b8d171a7ff5fe39402e0` |
| `images.json` | 26886 | `682a87262e67498f5f5dbee23379157bfc6ef50c56222702f99e5f065e5a37c5` |
| `images.md` | 13326 | `60269d49b7ead4ad96adf520c6b456d055124a7afe32fb92df325bd60a495843` |
| `images-summary.json` | 44960 | `b017f8be79dbd91310d004e7f0c71192b8d664b13a8e767fd316db0b507c81f5` |
| `secret-scan.md` | 10399 | `32704af55337be944cc72dce0e07331b57dcf4de16eed548ec5b3194e73c6248` |
| `SHA256SUMS.vm.txt` | 9612 | `212ddd6ee3631b4b617d687839df9ac90cfe327a8d3c820406d65bf16e92b229` |
| `SHA256SUMS.windows.txt` | 12693 | `468059f96394a67369dd1caa91e62d1ca4571617874ee2127c89fcaefb7f175d` |
| `summary.json` | 36808 | `c985b51895fc3202e7c28825b7f6afa605bdbd924df3a991a9ec57d3086ce677` |
| `summary.md` | 12462 | `f6927455ba9da0cca16b71280a0019dcbfc7c70e0b37c5c263cd68b02c22a4a0` |
| `summary.part1.json` | 877 | `da2cb8fde50a7d0b66bb289e5d87623ef60d11c4d2fa76b12fb3cc9706ff2ecb` |
| `summary.partA.json` | 1962 | `a9dadf83b2a1a2f94e976e0041e24cb4fe4b9adb844aa55b39b4f7d09686431f` |
| `summary.partB.json` | 5031 | `7b8efa83f1546cddd2be2e6948072f44bede011920e8d6c4be82584a28e7784d` |
| `summary.partC.json` | 8861 | `b88e9f0ab27b36bee930d8ca87aef5b2fd645b21092094b3eba87e01d6ca0e81` |
| `summary.partD.json` | 10754 | `1bb75602edc5712e54da8fc529832deff3fa2c85b576d3919bf18f5a7ced4de6` |

## SBOM 目录

| 文件 | 字节 | sha256 | 说明 |
|---|---|---|---|
| `sbom/backend.cdx.json` | 10839209 | `dc623ed9c56e0fd92b8e30dd3ba8af7f431c94bf89d4ed318854ffd222049093` | ⚠ 宽目录版，被历史备份污染（10,003 组件），仅作取证 |
| `sbom/backend.spdx.json` | 15737827 | `a0c02fcf36088beb1d31c1bf4762d0adc7d949b453104d5ab01f7fd8b183a0d9` | ⚠ 同上 |
| `sbom/backend-prod.cdx.json` | 180787 | `de86a3c690bb8f2fc723a3806be8024ffd2fa71da8eca9966c3112c309adce03` | **生产范围修正版，190 组件（推荐使用）** |
| `sbom/backend-prod.spdx.json` | 269531 | `8c198786237ee2b27c01084290d2fea77c1c891d450186f9f441a4e45f76f721` | 同上（SPDX） |
| `sbom/backend-sbom-verification.md` | 3445 | `c18ea9eb92babda0926fd83a3577545483bd7a3ca6c8d00312e094e3ca11c46e` | 传输与逐字节校验记录（闭合 NV-S1） |
| `sbom/deploy.cdx.json` | 398 | `cbbb8a727df11718c7592f6d677296b2e052237602804b140507103320e613a6` | 组件数 0（纯配置目录） |
| `sbom/frontend.cdx.json` | 217951 | `fe5c0925341ab4fcf7b16d2b9fa6963eee93b7cd7872c40db9f3a83ccd336d92` | 前端 176 组件 |
| `sbom/frontend.spdx.json` | 371704 | `3487c8f4031eeaa7726fd999da2ae6e656ab310ab4736b64b522237ac794802a` | 前端 176 组件（SPDX） |
| `sbom/prod-sbom-run.log` | 4164 | `92997e0ec38c2bd11e1f2cb70b06253d104b3faa7d82a481d2f5f9ac485ebb24` | 生产范围 SBOM 生成日志 |
| `sbom/README.md` | 6315 | `b5a6b03f5dbc9f6312b3d68bb62701f64e9029847a573f8fcf27d7a33408f0a0` | SBOM 报告（含污染分析与交叉核对） |
| `sbom/sbom-run.log` | 4309 | `1565573c05e6de2a790e06ed5fafbea0bab33fb9110474d578e9276db04e8eb3` | 首版 SBOM 生成日志 |

## VM → Windows 同步与逐字节比对

| 项 | 值 |
|---|---|
| 通道 A（首批） | VM 端 tar.gz → base64 分块 → Windows 解码 |
| 通道 A tarball sha256 | `60089d7a475853af2a90d97a22ea265e42e30b841b8b35cb122454d70af2dcea`、`cfb6f86b0a695f2f0c59629cce454c209ff40e44367f85ad6cb3936d6c1c9f84`（两端一致） |
| 通道 B（系统 scp） | `scp -o BatchMode=yes`，用于两份后端 SBOM（各约 10.8 MB / 15.7 MB），退出码均 0 |
| 逐文件比对（通道 A） | 以 VM 生成的 `SHA256SUMS.vm.txt` 为基准：**98/100 通过、0 不匹配** |
| 逐字节比对（通道 B） | 远端 `sha256sum -c big-artifacts.sha256.txt` 对 6 个文件全部 `OK`；Windows `Get-FileHash` 与远端 sha256sum 与 big-artifacts 三者一致 |
| 备注 | 先前记为「26 MB 超过传输沙箱」的 NV-S1 **已撤回**：该限制属 `sftp_download` 工具参数（本地路径须在 `/workspace`），并非主机能力限制 |

## 中间产物（保留，非交付物）

`summary.part*.json` 为构建 `summary.json` 时的分片，内容已完整并入 `summary.json`，保留以备追溯。

## 关键原始产物 sha256

| 文件 | 字节 | sha256 |
|---|---|---|
| `deps/official-registry-pnpm-audit.json` | 2165 | `8079a1074a84d130fb1a748a85e111fd634a007b29f8a3fff59fe6e4aa35524a` |
| `deps/official-registry-pnpm-audit.txt` | 6768 | `fc0e5187265de54bf74dc31617de103288d21075b709417571c1e28fcb9cf32f` |
| `deps/official-registry-audit.log` | 24783 | `735d5b22dad6b9ceaaa9e1950a08035cba40affa9efbc433e7237ba845a876bd` |
| `deps/govulncheck.json` | 731940 | `184d7d7f7fb5869461daddcf4f0b9560514c642904ab5ae750991b386bbfe662` |
| `deps/govulncheck.txt` | 6588 | `4799cff9ec23f5e0684407a4dd94d3538211f0fbfc4666bb14ff9ace0f0990bf` |
| `deps/govulncheck-parsed.json` | 5709 | `ee0a3b13f7e1515a7f17932cf683b857c660055c9c2768657319fc00117569f1` |
| `deps/osv-severity-table.json` | 6995 | `3bc3b7270e18270d11e708d43dc8a0eac8905c4fdeaae8a657d7d4932d6be723` |
| `deps/go-list-m-all.txt` | 6197 | `1333e21720a34236509d7729e9383875b9b7ad68e032be2732c5ba585de42320` |
| `deps/windows-host/pnpm-audit.json` | 192 | `4b7bccb843bdbb045a531327caa65951450dbaa041611ae82f3e928fedccf9d6` |
| `deps/windows-attempt1-npmmirror-FAILED-pnpm-audit.json` | 192 | `4b7bccb843bdbb045a531327caa65951450dbaa041611ae82f3e928fedccf9d6` |
| `images-summary.json` | 44960 | `b017f8be79dbd91310d004e7f0c71192b8d664b13a8e767fd316db0b507c81f5` |
| `iac/results.json` | 6879 | `100abba47b03e67311789de43c3da6290c650f899e3452a822cf4c9fc1cecc70` |
| `iac/checkov-deploy.json` | 138 | `eb21930427837719fb97ed79a9c6a4b20204df95c21098d5f766acd1d0e114bb` |
| `iac/trivy-config-deploy.json` | 233 | `afc70c6eaf3d9fe92ce7f60478e7b37c933b383113377ef3cf4fe70644cb958d` |
| `iac/checkov-secrets.json` | 24191 | `847f0093e1d5900ba1343a7a5db66684a3c0204404a7903067d04867b5d7390f` |
| `iac/systemd-and-services.txt` | 5383 | `e0826d737c63a13e76b3481daf8b57a1209bb3f8bb677ee10eaf579520e3ad82` |
| `secret/secret-scan-logs.json` | 14269 | `15630276091e3406baacfbb71ea367e2878d8059bf3db019f64cd8329408896f` |
| `secret/secret-scan-repo.json` | 91802 | `a46d57c060c6de0f0b782778eeeef92310574bc9b5f9458f1e7ac7a11a583ca5` |
| `secret/build-product-secret-check.json` | 4555 | `5556dc060fbff9b35ef7e421abc0f79bf63efdff15b4c8e08c8352239a5ddde8` |
| `artifacts/artifact-inventory.json` | 41365 | `c4630db25acc65ed4fa4075c81a5a95b00ecd403d683f47b55cf7c5704ab4c86` |
| `raw/trivy-images-run.log` | 20151 | `c62a1be914e1ca773a361a0fc9407ab6780a58d8553effa021ab365248143b2f` |
| `big-artifacts.sha256.txt` | 568 | `5ecdf3d52c4c98af4961a4e77cd0f1c42bc6f9404b4534d4454b2e722b5778b7` |

## 只读完整性证明（扫描前后一致）

| 文件 | sha256 |
|---|---|
| `backend/go.mod` (Windows) | `0091b9f57798adb2f39290557d67dfb9a17cc48138170f5f179fdba092ef3465` |
| `backend/go.mod` (VM) | `4e0f98fa4e9258cd07c1acc6f0ce5bd762cba5ba9343bcbd28b1f1a33644c36c` |
| `backend/go.sum` (两端) | `34147fb9944ea49b8470e4c0e0da85eb53ce60706975b2d5fe34c2011ee4b6b2` |
| `frontend/pnpm-lock.yaml` (两端) | `a11d5a0ec48ef455271427d048a066dc99a2b607f71beaa170188b22766a94b5` |

证据：`deps/windows-host/commands.log`、`deps/vm-deps-run.log`、`deps/official-registry-audit.log`。
