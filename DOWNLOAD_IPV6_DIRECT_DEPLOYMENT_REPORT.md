# DOWNLOAD_IPV6_DIRECT_DEPLOYMENT_REPORT — download.aurore.online 直连 IPv6 下载通道

部署完成：2026-09-09 02:4x (+07) · 主机 dataserver (Ubuntu VM, 192.168.31.152) · 依据《download_aurore_ipv6_direct_deployment.md》
状态：**GO（条件性）** —— 本机全链路验证通过；外部 IPv6 入站最终验证与两项安全建议见 §11 / §18。

## 1. 实际下载 API 路径

后端（Go, ./bin/api, 127.0.0.1:8080 上游经 Vite dev 3000 代理）提供两类下载 URL（均支持 HTTP Range/206、Accept-Ranges、ETag）：

| 路径 | 说明 |
|---|---|
| `GET/HEAD /api/v1/exports/{id}/download` | 直链下载（前端 JobDetailPage/BrowsePage 当前使用 `window.open`） |
| `GET/HEAD /api/v1/download/{ticket}` | 15 分钟短期下载票据（`POST /api/v1/exports/{id}/download-ticket` 签发，跨源安全，主站→download 子域推荐形态） |

下载内容由后端从 **MinIO（127.0.0.1:9001, 内网/loopback 绑定）** 流式转发，1MB 缓冲，非本地磁盘文件。

## 2. 实际下载文件位置

- MinIO object：`exports/<export_uuid>.zip`（`object_ref` 列）
- 实测文件：`c74ab23e-0185-4b2c-9305-a491f4697b2c` → **148,902,867 字节（≈142 MB）** zip，SHA256 etag `95312ab4…`
- `/data/blockchain-platform/exports`（旧目录）为空——不是现行下载源，勿误配。

## 3. 当前 ens33 IPv6

```
inet6 240e:356:e6:d600::382/128                  scope global (dynamic, noprefixroute)
inet6 240e:356:e6:d600:20c:29ff:fe92:dd79/64     scope global (SLAAC, mngtmpaddr)   ← 选中
```

## 4. DDNS 选择了哪个 IPv6

**240e:356:e6:d600:20c:29ff:fe92:dd79**（/64 SLAAC、非 temporary、IID 为 MAC 派生 EUI-64——前缀变化后 IID 稳定，DDNS 只改前缀部分）。

## 5. Cloudflare AAAA 记录

`download.aurore.online` AAAA → `240e:356:e6:d600:20c:29ff:fe92:dd79`（record id 75ac5233…；2026-09-08T19:27:55Z 创建，本次 PUT 幂等校验通过）

## 6. Proxy status

**DNS only**（`proxied:false`，ttl auto=1）✅ —— 按要求不设橙云。

## 7. DDNS service / timer 状态

- `/usr/local/sbin/cloudflare-ddns-download.sh`（root:root 700）——按 ens33 global IPv6 自动选择 /64 非 temporary 地址并 PUT AAAA（无变化时跳过）
- `/etc/systemd/system/cloudflare-ddns-download.service`（oneshot, root）
- `/etc/systemd/system/cloudflare-ddns-download.timer`：OnBootSec=30s / OnUnitActiveSec=2min / Persistent
- `systemctl is-enabled cloudflare-ddns-download.timer` = **enabled**；timer active；最近一次运行输出 `No change: … → 240e:356:e6:d600:20c:29ff:fe92:dd79`
- token：`/etc/cloudflare-ddns/token`（root:root 600，最小权限 Zone.DNS Edit + Zone Read，仅 aurore.online）

## 8. Nginx 新增配置

`/etc/nginx/sites-available/download.aurore.online.conf`（已软链到 sites-enabled；修改前备份 `/home/euripides/backups/nginx-before-download-20260909-024349`）

要点：
- `listen [::]:443 ssl http2 ipv6only=on;` server_name download.aurore.online
- LE 证书（fullchain/privkey）
- 仅两个 location 转发到 `http://127.0.0.1:3000`（vite → 8080 API）：
  - `location ^~ /api/v1/download/`（ticket）
  - `location ~ ^/api/v1/exports/[0-9a-fA-F-]+/download$`（直链, GET/HEAD）
- `proxy_buffering off; proxy_request_buffering off; proxy_read/send_timeout 3600s; send_timeout 3600s`
- Host=localhost + X-Forwarded-*（与主站 conf 一致）；其余所有路径 `return 404`（实测 /api/v1/tags、/health/live 均 404 ✅）

## 9. 443 listener

```
LISTEN [::]:443 (nginx)      ← 新增
LISTEN 127.0.0.1:8088 (nginx) ← 主站原样
```

## 10. SSL 证书

- Let's Encrypt **DNS-01**（certbot 2.9.0 + python3-certbot-dns-cloudflare）
- 凭据：`/root/.secrets/certbot/cloudflare.ini`（root 600）
- 证书：`/etc/letsencrypt/live/download.aurore.online/`，notAfter **2026-12-07**；`certbot.timer` enabled 自动续期
- `curl -6 -skI https://download.aurore.online/` → HTTP/2 404（TLS 由浏览器可信任 LE 证书签发 ✅；根路径 404 为设计）

## 11. 外部 IPv6 测试结果

**未完成外部网络验证**（§22）。VM 侧 curl 命中本机 global IPv6 属 on-link（同 /64），证明 TLS/nginx/DNS 正确，但不代表运营商/路由器入站放行。请在外部 IPv6 网络执行：

```bash
curl -6 -I https://download.aurore.online/
# 以及真实大文件
curl -6 -L -o /dev/null 'https://download.aurore.online/api/v1/exports/c74ab23e-0185-4b2c-9305-a491f4697b2c/download'
```
若超时：检查光猫/路由器 IPv6 入站防火墙仅放行 TCP 443 → VM（VM 的 SLAAC 地址 240e:356:e6:d600:20c:29ff:fe92:dd79）。

## 12. Range / 206 测试

| 测试 | 结果 |
|---|---|
| 直连 v6 直链 `Range: bytes=0-1048575` | **HTTP/2 206**, content-range `0-1048575/148902867`, 落盘 1048576 字节 ✅ |
| 直连 v6 ticket `Range: bytes=0-524287` | **206**, content-range 正确 ✅ |
| 非法 ticket（直连） | 403（后端校验）✅ |

## 13. Tunnel 下载速度（fallback 保留验证）

`https://aurore.online/api/v1/exports/c74ab23e…/download`（经 Cloudflare Tunnel 全链路）
- 完整 142 MB：**3,179,615 B/s ≈ 3.2 MB/s**（46.8 s）
- ticket 走 tunnel：200，60s 内实收 79.8 MB（持续流式）✅

## 14. Direct IPv6 下载速度

`https://download.aurore.online/api/v1/exports/c74ab23e…/download`
- 完整 142 MB：**180,885,174 B/s ≈ 180.9 MB/s**（0.82 s）

## 15. 性能提升比例

≈ **57 倍**（180.9 vs 3.2 MB/s）。
口径说明：直连测试为 VM 同 /64 on-link（未出 WAN），tunnel 腿为真实互联网往返。真实外网直连速度取决于运营商链路上行与路由器入站；即便打折，绕过 cloudflared 后大文件吞吐显著优于 3 MB/s 的 tunnel 上限。

## 16. IPv4-only fallback 是否保留

**保留且验证可用**：`aurore.online` 主站 Tunnel 未做任何改动（200），原下载直链与 ticket 路径均 200/206。前端策略（IPv6 优先 download、失败回退 aurore.online）为后续前端改动，本次未触碰前端代码。

## 17. CORS / Cookie / Auth 检查结果

- 后端鉴权中间件为 V1 透传（X-User-* 可选，默认 admin@local）——下载本身无登录强制；**票据（15 min TTL）是下载的时效鉴权**
- 下载不依赖 Cookie → 跨 origin（aurore.online ↔ download.aurore.online）无 Cookie/SameSite 问题
- 后端已返回 `Access-Control-Allow-Origin: *` + Allow-Methods/Headers（GET 实测）→ 前端 fetch/下载均可跨源
- 建议：主站前端后续把下载 URL 指向 `https://download.aurore.online/api/v1/download/{ticket}`（票据先在主站 POST 签发），保持鉴权不被绕过

## 18. 安全暴露检查（发现项）

**既有暴露（非本次引入，但违反 §29「仅 443 公网开放」验收口径）**：
1. Go API `./bin/api` 监听 **`*:8080`**、worker `./bin/worker` 监听 **`*:8081`**（IPv6+IPv4 wildcard）→ 公网 IPv6 可直接访问 API/worker 端口（无鉴权、含 admin 路由）
2. Vite dev server `0.0.0.0:3000`（仅 v4，局域网可达）
3. **UFW inactive**（无主机防火墙）

本次新增暴露：仅 `[::]:443`（必需）。**建议（未擅自改动）**：① 将 api/worker 绑定改为 127.0.0.1（或确认其只被本机调用后调整启动配置）；② 启用 ufw 仅放行 22/443（v6 与 v4）。改动前请确认 worker→api 通信模式，避免破坏链路。另注意：DNS only 已按方案公开源站 IPv6，属预期。

## 19. 修改文件列表

VM：
- `/etc/cloudflare-ddns/token`（新建 600 root）
- `/usr/local/sbin/cloudflare-ddns-download.sh`（新建 700 root）
- `/etc/systemd/system/cloudflare-ddns-download.service`、`.timer`（新建）+ timers.target.wants 软链
- `/etc/nginx/sites-available/download.aurore.online.conf`（新建）+ sites-enabled 软链
- `/root/.secrets/certbot/cloudflare.ini`（新建 600 root）
- `/etc/letsencrypt/`（certbot 生成）
- 备份：`/home/euripides/backups/nginx-before-download-20260909-024349`

Cloudflare：AAAA download.aurore.online（DNS only）——部署前已存在（2026-09-08T19:27:55Z），本次校验 + 幂等 PUT 确认内容/代理状态。

未改动：aurore.online / www.aurore.online Tunnel CNAME、ETL Tunnel、MX/SPF/DKIM/_dnsauth、主站 nginx conf、127.0.0.1:8088、应用代码、前端代码。

## 20. 回滚步骤

```bash
# 1) 禁用下载 Nginx
sudo rm -f /etc/nginx/sites-enabled/download.aurore.online.conf
sudo nginx -t && sudo systemctl reload nginx
# 2) 禁用 DDNS
sudo systemctl disable --now cloudflare-ddns-download.timer
# 3) Cloudflare 删除 download.aurore.online AAAA（保留其他所有记录）
# 4)（可选）删除证书与 nginx conf
sudo rm -rf /etc/letsencrypt/live/download.aurore.online /etc/nginx/sites-available/download.aurore.online.conf
```

## 21. 最终 GO / NO-GO

**GO（条件性）**：
- 本机验收全部通过：DNS ✅ / TLS ✅ / 443 ✅ / 200 ✅ / 206+Range ✅ / ticket 403 校验 ✅ / 主站 Tunnel fallback ✅ / DDNS 自动维护 ✅ / 主站与邮件 DNS 未动 ✅
- 前提条件：① 完成 §11 外部 IPv6 入站验收（若不通需放行路由器 TCP 443→VM）；② 建议尽快处理 §18 既有暴露（8080/8081 绑定 + UFW）后即完全达标。
