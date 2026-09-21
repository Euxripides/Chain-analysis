# Nginx + Cloudflare 透明代理部署报告

- 实施时间：2026-09-07 16:51–17:21（Asia/Bangkok，目标主机时间）
- 目标：`dataserver` / `192.168.31.152`
- 项目：`/home/euripides/projects/blockchain-platform`
- 架构：Cloudflare → cloudflared → `127.0.0.1:8088` Nginx → `127.0.0.1:3000`
- 实施结论：**本机部署完成，临时 Cloudflare 回归通过；正式公网 hostname 验收 NO-GO（hostname/DNS 管理信息缺失）。**

## 1. 已实施内容

1. 部署前生成 `NGINX_CLOUDFLARE_PRECHECK.md`。
2. 建立 root-only 备份：`/home/euripides/backups/nginx-cloudflare-20260907-165232`，权限 `0700`，包含 cloudflared token/config、原 systemd unit、端口和状态快照。
3. 安装 Ubuntu Nginx `1.24.0-2ubuntu7.17`。
4. 新增 `/etc/nginx/sites-available/blockchain-platform.conf`，仅监听 `127.0.0.1:8088`，单一 `location /` 转发至 `127.0.0.1:3000`。
5. 移除安装包默认公网 site；最终无 `:80` / `:443` / `0.0.0.0:8088` 监听，未修改 UFW/nftables。
6. 保留 Upgrade/Connection、CF、Real-IP、Forwarded、100 MiB 请求体与 300 秒超时设置。
7. 将上游 `Host` 固定为 `localhost`，并把真实公网 hostname 保留在 `X-Forwarded-Host` 和 Nginx access log，避免未改动的 Vite 拒绝 Cloudflare hostname。
8. 将 cloudflared systemd Origin 固定为 `http://127.0.0.1:8088`，增加对 nginx 的启动依赖。
9. 终止附着在 root shell 的重复 connector；当前仅保留一个 systemd 管理的 named Tunnel connector。
10. 临时 TryCloudflare hostname 测试完成后已终止，未留临时公网入口。

未修改 Downloader、TRON/EVM Provider、数据库 Schema、Frontend API Base URL、应用路由或 `:3000` bind。

## 2. 配置与运行状态

| 项目 | 最终状态 |
|---|---|
| `nginx -t` | PASS |
| nginx | enabled / active |
| cloudflared | enabled / active，单实例 PID 1221907 |
| named Tunnel | `bdaee7b6-c649-46e9-9e07-7e2b4e32e773`，4 个 HA edge connections |
| named Tunnel Origin | CLI local origin `http://127.0.0.1:8088` |
| Nginx bind | 仅 `127.0.0.1:8088` |
| 原应用 bind | 保持 `0.0.0.0:3000`，PID 10364 未重启 |
| API / Worker | PID 1190149 / 1190150，未因本部署重启 |
| Nginx error log | 空 |
| 防火墙 | 未增加任何规则 |
| 生产后台控制 | 保持 `ISOLATED`、`background_paused=true`，未自动恢复 |

文件 SHA-256：

- Nginx config：`e25bcbb82ddd6a94738a41ed541b48c3cbe3c5ce21ce54ab43512cdf3633bfa2`
- cloudflared unit：`46e883f58d3bd9f6ea093477be2cd4aacd27dc5f87e3e0834ace98650ee5023e`
- Precheck：`506f1c34e059f2a0b4ee4af5cfaa567f4132578d2dac79c10fc3796bbb03a0d0`

## 3. 本机透明代理验收

| 场景 | 证据 | 结果 |
|---|---|---|
| 首页等价 | `:3000` 与 `:8088` 均 200、710 bytes、SHA-256 `7606eacc...e1a1b6` | PASS |
| `/api/v1/me` | 两侧均 200、157 bytes、相同 SHA-256 | PASS |
| 身份头 | `X-User-Id/Email/Org/Role` 两侧回显一致 | PASS |
| Job detail | 去除每次生成的 `progress.ts` 后稳定 JSON 完全一致 | PASS |
| SSE | 两侧 200、`text/event-stream`，3 秒内均收到 609 bytes 初始事件 | PASS |
| CSV Range | 两侧 206、1024 bytes、文件头 `chain,tx` | PASS |
| XLSX Range | 两侧 206、1024 bytes、ZIP/XLSX magic `504b0304...` | PASS |
| 并发 | 100 请求、并发 20，100/100 为 HTTP 200 | PASS |
| 任意公网 Host | 修复后模拟公网 Host 返回 200，正文哈希与源站一致 | PASS |

`/health` 在源站和代理均为相同 404；真实健康端点 `/health/live` 与 `/health/ready` 均为 200，ready 检查 PostgreSQL/ClickHouse/MinIO/NATS 全部为 `ok`。

## 4. 临时 Cloudflare hostname 回归

使用限时 TryCloudflare hostname 验证 HTTPS → cloudflared → Nginx → Vite/API，全程测试结束即关闭。

| 场景 | 结果 |
|---|---|
| HTTPS 首页 | 200，710 bytes，正文 SHA-256 与本机源站一致 |
| Desktop | 下载、任务列表、任务详情、Explorer、TRON/BSC Investigation、系统页全部 200 |
| Mobile | 同上全部 200；除既有任务列表横向溢出外无新增布局差异 |
| Auth / API | Header Auth 与真实 API PASS |
| TRON / BSC / ETH | TRON/BSC Investigation 页面 PASS；ETH 地址目录 API 200（当前为空） |
| CSV Import | 2 个 BSC 地址、2 个 chain mapping、案件/标签字段均成功解析 |
| CSV / XLSX / ZIP | CSV/XLSX Range PASS；ZIP 206、1024 bytes、magic `504b0304...` |
| SSE | HTTPS 返回 200、`text/event-stream`，并观察到响应体字节；本机代理已确认完整 `data:` 初始事件 |
| WebSocket | Vite HMR Upgrade 经 Cloudflare/Nginx 返回 101 |
| 浏览器错误 | CORS 0、Mixed Content 0、Page/console error 0、502/504 0 |

浏览器证据：

- `frontend/test-results-nginx-cloudflare-final/desktop-download-tron.png`
- `frontend/test-results-nginx-cloudflare-final/mobile-download-tron.png`
- `frontend/test-results-nginx-cloudflare-final/mobile-navigation.png`

## 5. 低成本真实业务 Smoke

| 链/功能 | ID | 结果 |
|---|---|---|
| TRON Job | `8f005b7d-f1cc-4a5f-b245-d7b9780bfbb8` | CREATED → QUEUED → COMPLETED；1 shard、1 row、无 error |
| BSC Job | `76cadbf4-fb32-4cc7-a3e5-75de89780742` | coverage=1，CREATED → COMPLETED；无 Provider 下载、无 error |
| ZIP Export | `4b289ec1-2c46-4597-b2ed-f25830224432` | COMPLETED；6 rows、1706 bytes；SHA-256 `272dc2502c3b1ceba88e440cf16246dfa0ac5343e7bec6542b40cb49d4c6cb9b` |
| CSV Export | `1646feff-e416-4f0d-a6f7-99ba756e1463` | Range 206，内容与源站一致 |
| XLSX Export | `533ed04d-65a6-4d9b-bcae-73e36a308b28` | Range 206，文件头正确 |

## 6. 已知限制与 GO/NO-GO

### PASS

- Nginx 安装、配置、bind、日志、systemd 和回滚准备全部通过。
- 本机透明代理、真实 API、Auth、SSE、WebSocket、TRON/BSC、Explorer/Investigation、Import/Export、Desktop/Mobile 代理回归通过。
- 临时 Cloudflare hostname 完整链路通过。
- 部署后未发现 413/502/503/504；测试日志中的少量 499 均发生在 Playwright 页面跳转主动取消尚未完成的查询时，源站与代理行为一致。

### 未满足的正式发布门禁

1. 服务器上没有正式 hostname、DNS zone 或 Cloudflare 管理 API token；connector token 无法调用 Cloudflare management API（返回 Cloudflare 9106 Authentication failed）。
2. named Tunnel 启动后的 metrics 为 4 个 HA connection、0 request、0 request error，说明当前没有可用于验证的正式 hostname 流量。
3. 因此不能给出“正式公网 hostname Smoke PASS”的证据，也不能确认 DNS 已发布到该 Tunnel。
4. 应用没有独立 Graph 路由；本次只验证了现有 Explorer/Investigation 关系视图，不能伪造 Graph PASS。
5. 移动端 `/jobs` 横向溢出在源站 `:3000` 与代理 `:8088` 均存在，属于既有前端问题；按本次禁止改应用的范围未修复。

最终判定：**本机/临时 Cloudflare 部署 GO；正式公网验收 NO-GO。** named Tunnel 的本地 Origin 已安全切至 Nginx，但要关闭 NO-GO，仍需提供正式 hostname（以及必要时 Cloudflare API token），随后执行正式 URL smoke。

## 7. 回滚

如 Nginx 路径出现问题，可将 `/etc/systemd/system/cloudflared.service` 的 `ExecStart` 恢复为备份中的原 unit，并执行：

```bash
sudo systemctl daemon-reload
sudo systemctl restart cloudflared
```

完整备份位于 `/home/euripides/backups/nginx-cloudflare-20260907-165232`，仅 root 可读。回滚不需要修改数据库、Provider 或应用代码。
