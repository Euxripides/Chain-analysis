# DOWNLOAD_SECURITY_AND_FRONTEND_FINALIZATION_REPORT — 安全收紧 + Ticket 前端切换 + IPv4 fallback 收尾

完成：2026-09-09 03:05 (+07) · dataserver (Ubuntu VM) · 依据《download_security_frontend_finalization.md》
结论：**GO**（外部 IPv6 实网验收由用户按 §29 自行执行；API/Worker 自启机制建议见 §28）

## 1. API 8080 原监听
`*:8080`（IPv4+IPv6 wildcard，公网 IPv6 可达）——`cmd/api/main.go`：`Addr: ":" + itoa(cfg.HTTPPort)`

## 2. API 8080 最终监听
`127.0.0.1:8080`（仅本机）✅

## 3. Worker 8081 原监听
`*:8081`（IPv4+IPv6 wildcard；该端口还暴露 /debug/pprof、/metrics）——`cmd/worker/main.go`：`metricsAddr := ":" + strconv.Itoa(...)`

## 4. Worker 8081 最终监听
`127.0.0.1:8081`（仅本机）✅

## 5. 修改的启动配置
Go 源码（无 host 配置开关，按文档 §6.1「应用默认 bind 配置」处理）：
- `backend/cmd/api/main.go`：Addr → `"127.0.0.1:" + itoa(cfg.HTTPPort)`
- `backend/cmd/worker/main.go`：metricsAddr → `"127.0.0.1:" + strconv.Itoa(...)`
- 备份：`cmd/api/main.go.bak-loopback-20260909-025942`、`cmd/worker/main.go.bak-loopback-20260909-025942`
- 重启：官方 `scripts/dev.sh`（go build + setsid 启动 api/worker/vite，readiness 通过）
- 附带修复：dev.sh 中 `vite --host`（v6+v4 wildcard）→ `--host 0.0.0.0`（恢复为 IPv4 LAN，避免新增公网 v6 暴露；备份 dev.sh.bak-20260909-vitehost）
- 依赖取证：ss 活动连接显示 8080 仅被本机 Vite(127.0.0.1) 与 nginx(127.0.0.1) 访问；8081 无任何入站消费者 → API_EXTERNAL_REQUIRED=no / WORKER_EXTERNAL_REQUIRED=no（有证据，非假设）

## 6. UFW / nftables 状态
UFW **inactive**（未启用——绑定收紧后 8080/8081 已不再公网可达，无需防火墙兜底；未擅自整体启用 UFW）。公网仅剩 `[::]:443`（download）与 cloudflared QUIC UDP（CF 会话，正常）。

## 7. 3000 最终监听
`0.0.0.0:3000`（Vite dev，**IPv4 LAN exposure** —— 按文档不强改，记录为既有暴露；本机经 nginx 8088/tunnel 访问不受影响）

## 8. 8088 最终监听
`127.0.0.1:8088`（nginx，主站链路原样）✅

## 9. 443 最终监听
`[::]:443`（nginx，download.aurore.online 直连通道）✅

## 10. 前端下载旧流程
`window.open('/api/v1/exports/{id}/download', '_blank')` 直链（BrowsePage、JobDetailPage doExport/doExportAll、DataExplorerPage 共 4 处），不经 Ticket。

## 11. 前端下载新流程
1) 导出 COMPLETED → `startExportDownload(exportId)`
2) `POST /api/v1/exports/{id}/download-ticket`（主站 origin，15 分钟票据）
3) `GET download.aurore.online/download-health` CORS 探测（1.2s AbortController 超时）
4) 可达 → `https://download.aurore.online/api/v1/download/{ticket}`（锚点点击触发，不 fetch 大文件、无弹窗拦截）
5) 不可达/无票据 → `window.location.origin/api/v1/exports/{id}/download`（Tunnel fallback），JobDetail 提示「已切换兼容下载」
- 新文件 `frontend/src/utils/download.ts`（DIRECT_DOWNLOAD_ORIGIN / IPV6_PROBE_TIMEOUT_MS=1200 / canUseDirectDownload / startExportDownload / triggerBrowserDownload）
- 修改：BrowsePage.tsx、JobDetailPage.tsx、DataExplorerPage.tsx（全应用再无直链 window.open 下载）
- 备份：各页 `*.bak-20260909-ticket`；项目非 git 仓库（无 .git），以 .bak 为回滚基线

## 12. Ticket 签发接口
`POST /api/v1/exports/{id}/download-ticket` → `{ticket_id, url:"/api/v1/download/{ticket}", expires_in_seconds:900}`（后端 v12.go handleExportTicket）

## 13. Direct URL
`https://download.aurore.online/api/v1/download/{ticket}`（DNS only AAAA；Nginx 仅放行 /api/v1/download/ 与 /api/v1/exports/{uuid}/download）

## 14. fallback URL
`https://aurore.online/api/v1/exports/{id}/download`（主站 Cloudflare Tunnel 原链路，200/206 实测）

## 15. 是否实现 download-health
**已实现**（Nginx 直返，不经后端）：`location = /download-health { add_header Access-Control-Allow-Origin "https://aurore.online" always; add_header Cache-Control "no-store" always; return 204; }`
实测：合法 Origin 204 + ACAO；非 aurore origin 浏览器侧拦截（ACAO 值不匹配）。其他路径仍 404。

## 16. IPv6 探测超时值
`IPV6_PROBE_TIMEOUT_MS = 1200`（1.2s，AbortController）

## 17. invalid ticket 测试
`GET download.aurore.online/api/v1/download/not-a-real-ticket` → **403** ✅

## 18. expired ticket 测试
签票后将 DB `expires_at` 置为过去 → **403** ✅（测试行已清理 DELETE）

## 19. Range direct
ticket 直连 `Range: bytes=0-1048575` → **206**，1,048,576 字节精确 ✅

## 20. Range fallback
`https://aurore.online/api/v1/exports/{id}/download` 同 Range → **206** ✅；两文件 **SHA-256 一致**（3cee9eab…）——双通道字节级相同

## 21. 主站回归
- `https://aurore.online/` 200（title Trace Desk）· /health/live、/health/ready 200 · /api/v1/me|tags|cases|download-jobs 全部 200 · 8088 全 200 ✅

## 22. SSE
`/api/v1/download-jobs/{id}/events` → HTTP 200, Content-Type: text/event-stream ✅（未用 WebSocket：全前端无 WebSocket 引用）

## 23. WebSocket
无（EventSource SSE 即实时通道，JobDetailPage:219 原样）

## 24. CSV/XLSX/ZIP
同一下载 handler 按 format 设置 Content-Type：ZIP(csv+zip object) HEAD 200 application/zip；**XLSX** 1.6MB HEAD 200 application/vnd.openxmlformats…（direct+fallback 双通道）；DB 中另有 json/jsonl/parquet COMPLETED 导出同路径可服务 ✅

## 25. 修改文件列表
- backend/cmd/api/main.go（bind）＋ .bak-loopback-20260909-025942
- backend/cmd/worker/main.go（bind）＋ .bak-loopback-20260909-025942
- scripts/dev.sh（vite --host 0.0.0.0）＋ .bak-20260909-vitehost
- frontend/src/utils/download.ts（新增）
- frontend/src/pages/BrowsePage.tsx / JobDetailPage.tsx / DataExplorerPage.tsx ＋ 各自 .bak-20260909-ticket
- /etc/nginx/sites-available/download.aurore.online.conf（增加 /download-health）
- 二进制：backend/bin/{api,worker}（重建）；运行进程由 scripts/dev.sh 重启（/tmp 日志）

## 26. git diff 摘要
项目无 git 仓库（root/frontend/backend 均无 .git）→ 无法提供 git diff；以 §25 的 .bak 文件为等价基线（逐文件备份于改动前）。

## 27. 回滚方案
- 前端：`cp .bak-20260909-ticket <文件>` 还原 3 个页面（新 util 删除即可；vite HMR 即时生效）
- bind：`cp main.go.bak-loopback-… main.go`（api/worker 各一）→ `bash scripts/dev.sh` 重建重启
- dev.sh：`cp dev.sh.bak-20260909-vitehost scripts/dev.sh`
- nginx：`sudo rm -f /etc/nginx/sites-enabled/download.aurore.online.conf && sudo nginx -t && sudo systemctl reload nginx`
- 每次操作前 `sudo nginx -t` 均 PASS（现状已 PASS）

## 28. 仍存在的风险
1. **API/Worker 无 systemd 单元**（is-enabled = not-found）：由 scripts/dev.sh（setsid + /tmp pid 文件）手动启动，**重启后不会自启**。cloudflared/nginx/DDNS/certbot 均已 enable。→ 建议后续为 api/worker/vite 建 systemd unit（本次未擅自引入，避免改变既有运维习惯）
2. 3000（Vite dev）仍 IPv4 LAN 暴露（记录项，见 §7）
3. 应用仍以 Vite dev 模式作为生产前端入口（现状如此；非本任务范围）
4. download 直连为 DNS only，源站公网 IPv6 暴露属方案必然（已有记录）
5. 外部 IPv6 实网验收待用户执行（§29 命令）

## 29. 最终 GO / NO-GO
**GO**
- 8080/8081 不再公网 wildcard（127.0.0.1）✅；主站 Tunnel 未动 ✅；download 仅 [::]:443 公网 ✅；Ticket invalid/expired 403 ✅；direct/fallback Range 206 且字节一致 ✅；前端 Ticket 优先+自动 fallback 完成、无大文件进内存、不缓存 ticket ✅；SSE/API/主站回归全绿 ✅
- 用户待办：外部 IPv6 实测（手机 5G/4G 或有 v6 网络）：`curl -6 -I https://download.aurore.online/` 与真实大文件下载

---

## 30. 追加：api / worker / vite 开机自启（2026-09-09 09:57 完成）

原 §28 风险 1（API/Worker/Vite 无自启）已解决，采用 **systemd 用户单元 + enable-linger**：

| 单元（~/.config/systemd/user/） | 进程 | 监听 | 日志 |
|---|---|---|---|
| blockchain-api.service | backend/bin/api（User=euripides, EnvironmentFile=deploy/.env, Restart=on-failure, StartLimitIntervalSec=0） | 127.0.0.1:8080 | append:/tmp/api.log |
| blockchain-worker.service | backend/bin/worker（同上） | 127.0.0.1:8081 | append:/tmp/worker.log |
| blockchain-vite.service | node v24.20.0 + vite --host 0.0.0.0（WorkingDirectory=frontend） | 0.0.0.0:3000 | append:/tmp/vite.log |

- 已执行：`loginctl enable-linger euripides`（Linger=yes → 开机免登录启动用户实例）；三个单元 `enable`（default.target.wants 软链）＋ active
- 依赖基础：docker compose 各容器 restart: unless-stopped（开机由 docker.service 拉起），api/worker After=docker.service/network-online.target，失败 5s 自动重启直至就绪
- **验证**：stop→start 全循环（模拟开机）后 listeners 与 health 全恢复；`bash scripts/dev.sh`（已改写为 systemctl --user enable/start/restart 流程，备份 dev.sh.bak-20260909-systemd）重建后单元全部 active；主站 200 / ready 200 / download-health 204
- 日常管理：`systemctl --user (status|restart|stop|start) blockchain-api blockchain-worker blockchain-vite`
- 说明：重启后 `systemctl --user` 免登录可用（linger）；如需彻底卸载自启：`systemctl --user disable --now blockchain-{api,worker,vite}` + `sudo loginctl disable-linger euripides`

**重启实测（2026-09-09 12:17 +07，sudo reboot 后自动恢复）**：
- api/worker/vite 用户单元开机自动拉起（进程 PID 1729/3371/3372，启动于引导早期）→ active
- nginx / cloudflared / docker 全部 active；compose 容器 healthy（postgres/clickhouse/nats/minio）
- DDNS timer 开机 30s 自动执行（日志 No change，AAAA 仍为 240e:356:e6:d600:20c:29ff:fe92:dd79）
- 监听：127.0.0.1:8080/8081、0.0.0.0:3000、127.0.0.1:8088、[::]:443 全部就位
- 验收：主站 https://aurore.online 200 / /health/ready 200；download.aurore.online /download-health 204
