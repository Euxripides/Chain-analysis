# preserved-local-20260912T2030Z —— 本地（Windows 工作树）FAIL 期证据冻结副本

> 冻结时间：2026-09-12T20:30Z（UTC）/ 2026-09-13 03:30（UTC+7）
> 冻结人：执行 Agent（上下文交接前的最后一次本地冻结）
> 目的：在把部署机的新证据同步回工作树之前，先把工作树里**仍是 FAIL 期**的原始证据复制到本目录并逐一记录 SHA-256，
>       使「原始 FAIL 证据」在本地也有不可混淆的副本。**本目录只增不改，禁止覆盖或删除。**

## 目录内容与 SHA-256

见同目录 `SHA256-MANIFEST.txt`（每行：sha256 / 字节数 / 相对工作树的原始路径）。

| 文件 | 冻结时的结论 |
|---|---|
| `sprint7/security/negative-tests/result.json` | 工作树旧快照（FAIL），部署机已有 PASS 97/0/0 的新版本；两处不同步 |
| `sprint7/security/negative-tests/run.log` | 同上，含失败用例原文 |
| `sprint7/security/image-iac/SUMMARY.md` | 镜像/IaC 缺口记录（只在工作树存在） |
| `sprint7/security/dependency-scan/SUMMARY.md` | 依赖漏洞 P0=2 / P1=5 的原始记录 |
| `sprint7/release/release-gate.md` | 切流门禁原始记录 |
| `tests/sprint7/qa/summary.json` | **2026-09-12T18:52:26Z 的旧聚合快照**（sprint5 105/1、playwright 33/4/21，status FAIL） |
| `tests/sprint7/qa/summary.txt` | 同上，配套汇总行 |

## 必须如实记录的一件事：4 个本地文件已被本轮同步替换

本轮把部署机的 2026-09-13 复跑证据同步回工作树时，以下 4 个**同路径**文件在本地被新版覆盖
（旧版内容与部署机 `preserved-original-*` 中的副本**逐字节相同**，SHA-256 见对应目录的 `PRESERVATION-NOTE.txt`）：

| 本地路径 | 覆盖前（本地旧版） | 覆盖后（部署机新版） | 旧版权威副本仍在 |
|---|---|---|---|
| `tests/sprint7/qa/ui/result.json` | 33/4/21 FAIL | 37/0/21 PASS | 部署机 `tests/sprint7/qa/ui/preserved-original-20260912T1843Z/result.json` |
| `tests/sprint7/qa/ui/playwright.log` | 含 4 条 FAIL | 0 FAIL | 部署机 `…/preserved-original-20260912T1843Z/playwright.log`（sha256 `269193379a8af136a2c1a9d7dbb10df085f506b733a3d90f4ff29d41fddc98fe`） |
| `tests/sprint7/qa/sprint5/result.json` | 105/1/0 FAIL | 108/0/0 PASS | 部署机 `tests/sprint7/qa/sprint5/preserved-original-20260913T0148Z/result.json` |
| `tests/sprint7/qa/sprint5/run.log` | 含 `DATA-007 … got=no want=yes`（第 200 行） | 0 FAIL | 部署机 `…/preserved-original-20260913T0148Z/run.log` |

**因此：本轮「原始 FAIL 证据」没有丢失**——权威副本在部署机的 `preserved-original-*` 两处，且已同步进工作树的同名路径；
被替换掉的只是工作树里那 4 份**同内容的旧副本**。

## 后续操作纪律（本会话已犯过一次，下一会话必须遵守）

1. **禁止**先 read 解除覆盖保护、再 write 覆盖证据文件——那正好会把要保留的旧证据换掉。
   正确做法：先把旧文件复制到唯一的 `preserved-local-<真实UTC>/` 目录并算 SHA-256，再动原路径。
2. 原始 FAIL / PASS / abort 日志一律**只增不改**；聚合摘要（如 `qa/summary.json`）可以更新，但必须写明更新前后版本。
3. 证据文件跨机同步必须用**二进制字节传输**（scp / sftp_download / base64+decode），
   **不得**用会截断的文本通道（read 工具在约 47k 字符处静默截断；本会话已因此把 767 行脚本截成 650 行并损坏过一次）。
4. 传完必须比对两端 SHA-256。
