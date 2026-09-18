# activity 端点 8–10 s 修复（2026-09-17，实测对照）

> 目标（用户底线 #7）：把明显慢的请求处理到可接受程度。
> 本文只写**实测数字**：同一台机器、同一个测试账号、同一地址、串行请求，旧=冻结版（8080），新=修复版（18099）。

## 1. 根因（逐行定位）

`GET /api/v2/explorer/addresses/{chain}/{address}/activity` 的耗时里，**ClickHouse 只占 0.14–0.32 s**：

- `internal/explorer/service.go:534 resolveAddressTypes` 把一页 200 行里出现过的 from/to/address/counterparty 全部作为候选地址，
  交给 `internal/tron/client.go:1004 ResolveAddressTypes`；
- 该函数**逐个串行**调用 `AccountState`（TRON：每个地址一次 Provider HTTP），最多 200 次串行外呼；
- 调用方只给 **8 s** 预算（`service.go:564`），必然超时；而超时/出错时 `service.go:567-569` **直接 return、一条缓存都不写**，
  于是**每一个请求都重新烧满 8 s** —— 这解释了为什么它稳定复现 8.2–10.2 s。

## 2. 修复内容（两个文件）

| 文件 | 改动 |
|---|---|
| `backend/internal/tron/client.go` | `ResolveAddressTypes` 由"逐个串行 + 一个失败全批放弃"改为**有限并发（8）+ 单地址容错 + 返回已解析的部分结果** |
| `backend/internal/explorer/service.go` | ① 解析预算 **8s → 500ms**（墙钟到点即返回已有结果）；② **每请求最多解析 16 个**地址（排序后截断，确定性），其余留给后续请求让缓存逐轮填充；③ 成功项写 **24h** 缓存，**已尝试但未解析**的写 **2 分钟**负缓存（显示语义与原先"超时后缺失⇒按非合约呈现"一致），从而不再每个请求重烧 |

语义保持：返回仍是 canonical 地址 → 是否合约；未解析地址的表现与修复前超时一致。

## 3. 实测对照（同一地址 `TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj`，`limit=200`，串行 6 次）

| 次数 | 冻结版（8080） | 修复版（18099） |
|---|---:|---:|
| #1 | 8.870 s | 1.531 s |
| #2 | 8.215 s | 1.086 s |
| #3 | 8.558 s | 0.874 s |
| #4 | 8.721 s | 0.548 s |
| #5 | 8.255 s | 0.546 s |
| #6 | 8.462 s | **0.383 s** |

- 稳态 **0.38–0.55 s**（比修复前快约 **20 倍**），首次冷请求 1.53 s（后续请求把剩余地址逐步解析并缓存）。
- 全部返回 **HTTP 200**，响应体结构不变。

## 4. 复算方式

```bash
# 构建到独立文件（不覆盖 bin/api）
cd backend && /usr/local/go/bin/go build -o bin/api.activity ./cmd/api
# 用 18099 起一份，配同一套 env（deploy/.env + /tmp/s6-e2e-env.sh + /tmp/dual-system-fixture.env）
API_HTTP_PORT=18099 ./bin/api.activity &
# 用夹具 investigator 登录取 Cookie（必须带 workspace_id），再对照 curl 8080 / 18099
```

（脚本与命令留存在部署机 `/tmp/activity-test.sh`；本文件的数字即该脚本输出。）

> 说明：本文件记录的是 `bin/api.activity`（只含本次 activity 修复）上的对照测量；
> 与另一路（后台 count FINAL 有界化、flow-profile 聚合）合并后的最终数字，见 perf 套件复跑结果与子任务交付包。
