# 复核说明（已更正）：`backend-scan-stability.txt` 的 `STABILITY_VERDICT=no` 根因是**扫错了文件**，不是 syft 语义问题

> 复核人：S7-SEC-01 主执行者（父代理）
> 初版时间：2026-09-12T17:05Z ｜ **更正时间：2026-09-12T17:10Z**
> **本文件初版包含一条错误的技术结论，已由我用对照实验证伪并在此更正。以下为更正后的内容。**

## 1. 先更正我自己的错误（重要）

初版我写道：「syft 对 Go 二进制写入的 `metadata.component.version` 不是文件 sha256，而是 Go build-info 派生摘要，本来就不会相等」。

**这条是错的。** 我用同一 syft 镜像做了三组对照实验（`docker.1ms.run/anchore/syft:latest`，1.51.1，`-o cyclonedx-json`），实测：

| 被扫目标 | 文件自身 sha256 | syft 写入的 `metadata.component.version` | 是否相等 |
|---|---|---|---|
| `/tmp/probe.txt`（23 字节纯文本） | `5f11e539676f…efe76` | `sha256:5f11e539676f…efe76` | **相等** |
| `<project>/bin/api`（陈旧构建） | `33440571c848…5ad` | `sha256:33440571c848…5ad` | **相等** |
| `<project>/backend/bin/api`（交付物） | `bae72e0a5063…142` | `sha256:bae72e0a5063…142` | **相等** |

**结论：syft 的该字段就是被扫文件的真实 sha256。** 我此前的推断不成立，特此更正。

## 2. 真正的根因：扫描目标路径解析到了另一个文件

任务书给的原始命令形式是：

```bash
sudo -n docker run --rm -v <project>:/w -w /w anchore/syft:latest bin/api -o cyclonedx-json=...
```

挂载点是 `<project>`，工作目录是 `/w`，因此相对路径 `bin/api` 解析为 **`/w/bin/api` = `<project>/bin/api`**，
而交付物在 **`<project>/backend/bin/api`**。两个文件都真实存在：

| 路径 | 大小 | mtime | sha256 |
|---|---|---|---|
| `<project>/bin/api` | 50,347,852 B | 2026-09-08 22:37 | `33440571c848…5ad`（**陈旧构建，非交付物**） |
| `<project>/backend/bin/api` | 55,518,192 B | 2026-09-13 00:00 | `bae72e0a5063…142`（**交付物**） |

第一轮 syft 记录的 `33440571…` 正是**陈旧文件的真实哈希**，却被拿去与交付物的哈希比对 →
于是脚本判定「目标不稳定」并重试 4 次。**这是「拿 A 文件的哈希核对 B 文件的扫描结果」，属测量错误，
不是工具语义错误，也不是 syft 的缺陷。**

修正后（目标改为 `backend/bin/api`）重新生成，`TARGET_VERIFICATION=PASS`。

## 3. 确实发生过的并发重建（这一条是真的）

在此期间另一个进程在做部署循环，`backend/bin/api` 被多次重建：

```text
23:28:04 local  753ba724…  (55,370,073 B)
23:54:22 local  6106d8e4…  (55,370,073 B)   api 进程重启 pid 2799693
00:00:03 local  bae72e0a…  (55,518,192 B)   ← 最终 backend SBOM 描述的就是这一版
00:02:57 local  23a8dd77…  (随后又被替换)
```

因此**消费方在使用 SBOM 前必须重新比对**：`sha256sum backend/bin/api` 是否等于 `backend.cdx.json` 中的
`metadata.component.version`（去掉 `sha256:` 前缀）。不等则说明 SBOM 与现役构建已脱钩，需重新生成。

## 4. 复核结论（更正后）

| 项 | 判定 |
|---|---|
| syft 的 `metadata.component.version` 是否等于被扫文件 sha256 | **是**（三组对照实测） |
| 首轮「目标不稳定」的根因 | **扫描目标路径解析到陈旧文件 `<project>/bin/api`** |
| 是否有并发重建 | **有**（4 次构建），但**不是**首轮判定失败的原因 |
| 最终落盘 SBOM 对应的构建 | `bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142` |
| 四份产物可解析性 | **通过**（CycloneDX 1.7：backend 56 / frontend 184 组件；SPDX-2.3：backend 56 / frontend 182 packages） |

## 5. 由此得到的流程教训

1. 容器内做文件扫描时，**挂载点与目标路径必须显式对齐**（用 `-v <project>:/w` 就应写 `backend/bin/api`，
   或直接挂 `-v <project>/backend:/w` 再写 `bin/api`）；生成脚本应对目标做存在性/大小/mtime 前置断言。
2. SBOM/扫描类证据**必须同时记录构建指纹**（`build_sha`）与扫描前后哈希，否则证据会与被测构建脱钩。
3. 校验 syft 产物时，应直接比对 `metadata.component.version` 与被扫文件 sha256，而不是推断其语义。
