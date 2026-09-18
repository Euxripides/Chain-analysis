# 运维变更：SSH 加固与 DDNS 令牌修复（2026-09-18）

涉及主机：生产机 `192.168.31.152`（dataserver，系统级 user 单元部署 ChainSight）。
触发原因：排查"端口暴露在公网"时确认 22 端口对全网监听且仅密码认证。

## 变更 1：sshd 认证加固 —— 已完成并验证

文件：`/etc/ssh/sshd_config.d/10-auth-hardening.conf`

```
PubkeyAuthentication yes
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
MaxAuthTries 3

Match Address 192.168.31.0/24,127.0.0.1,::1
    PasswordAuthentication yes
```

策略：非局域网来源只允许密钥；局域网与本机保留密码登录，作为密钥丢失时的退路（避免自锁）。

### 关键坑
`/etc/ssh/sshd_config.d/50-cloud-init.conf`（cloud-init 生成）内含 `PasswordAuthentication yes`。
sshd 对同名指令取**先解析到的值**，且 `Include /etc/ssh/sshd_config.d/*.conf` 按文件名排序，
因此本文件必须以 `10-` 前缀排在 `50-` 之前。首版使用 `60-` 完全不生效（`sshd -T -C` 显示仍为 yes）。

### 验证证据
| 检查 | 结果 |
| --- | --- |
| `sshd -t` 语法 | OK |
| `sshd -T -C addr=240e:357:1718:8f00:…`（公网 IPv6） | `passwordauthentication no` |
| `sshd -T -C addr=8.8.8.8`（公网 IPv4） | `passwordauthentication no` |
| `sshd -T -C addr=192.168.31.41`（局域网） | `passwordauthentication yes` |
| `sshd -T -C addr=127.0.0.1`（回环） | `passwordauthentication yes` |
| Windows 客户端 `ssh dataserver`（公钥） | `KEY_LOGIN_STILL_OK` |
| 新建会话走密码登录（局域网） | `LAN_PASSWORD_SESSION_OK` |
| reload 后服务状态 | `active` |

重启后依然生效：配置位于 `sshd_config.d`，且 `ssh.socket` 为 enabled+active（socket 激活）。

### 回滚
```bash
sudo rm /etc/ssh/sshd_config.d/10-auth-hardening.conf && sudo systemctl reload ssh
```

## 变更 2：DDNS 令牌更换 —— 部分完成，阻塞在权限

### 现象
`cloudflare-ddns-download.timer` 每 2 分钟执行一次，`journalctl -u cloudflare-ddns-download.service` 每次均为：

```
Detected IPv6: 240e:356:68:7300:20c:29ff:fe92:dd79
curl: (22) The requested URL returned error: 403
```

### 影响
`download.aurore.online` 的 AAAA 记录停在 `240e:357:1718:8f00:20c:29ff:fe92:dd79`，
该地址**已不在本机**（本机当前 41 个未 deprecated 的公网 IPv6 中无此地址）。
nginx 在 `[::]:443` 正常监听，但域名解析到死地址 → 下载通道从公网不可达；从外网经该域名 SSH 同样不可达。

### 排查结论
1. 旧令牌已失效：对 `/zones?name=aurore.online` 的查询即返回 403。
2. `/user/tokens/verify` 报 `Invalid API Token` **不能作为判据**：该接口只认用户级令牌，
   账号级 `cfat_` 令牌在此必然失败。
3. 换入新令牌后：区域查询、DNS 记录查询全部成功（读权限正常），
   但脚本执行到更新记录的 PUT 时仍返回 403 → 令牌缺少 `Zone:DNS:Edit`。

### 处理结果（已完成）
- 第二次更换为具备 `Zone:DNS:Edit` 的令牌，装入 `/etc/cloudflare-ddns/token`（root:root 600）；
  此前两个令牌备份于 `/home/euripides/backups/cf-token.old-20260918`。
- 手工执行输出：
  `download.aurore.online updated 240e:357:1718:8f00:… → 240e:356:68:7300:…`，
  记录内容与本机当前首选 IPv6 一致，`proxied=false`；再次执行输出 `No change:` 并正常退出（幂等）。
- 解析验证：`getent ahostsv6 download.aurore.online` → `240e:356:68:7300:20c:29ff:fe92:dd79`。
- 定时器每 2 分钟自动执行，后续跟随前缀轮换自动更新。
- 客户端：`~/.ssh/config` 增加 `dataserver-wan`（HostName `download.aurore.online`）。

### 遗留：入站 IPv6 可达性未经外部验证
下载通道访问日志（含归档 `.1` / `.2.gz`）内的历史请求，来源全部是本机自身地址
（`…20c:29ff:fe92:dd79`，即本机 MAC 派生的接口标识）或同一 `/64` 内的家用设备，
**没有任何来自外部网络的请求记录**；加之 DNS 记录此前长期指向已消失地址，
因此无法从本机内部证明"公网能否连进来"——本机回连自己的公网地址会被内核走本地环回，
不构成外部验证。需在家庭网络之外实测：
`https://download.aurore.online/download-health`（浏览器）或 `ssh dataserver-wan`。
若超时，则说明运营商封锁入站 IPv6，应改用既有 Cloudflare Tunnel（token 托管模式）承载 SSH，
此时 22 端口可完全不对公网开放。

## 变更 3：IPv6 改为 Passthrough，入站打通 —— 已完成并验证（2026-09-19）

### 结论
小米 RD08（固件 1.1.96，管理页 http://192.168.31.1）在 **native** 模式下，
WAN→LAN 转发默认 DROP，且固件**不提供**任何 IPv6 防火墙 / 端口映射开关。
实测：`/api/xqsystem/{firewall_v6,v6_firewall,ipv6_firewall,ipv6_portforward,portforward_v6,ipv6_switch}`
全部 404；可用的只有 IPv4 的 `/portforward`、`/dmz`、`/upnp`；`/web/setting` 中"防火墙"字样出现 0 次。
且本线路 WAN 为 PPPoE + 网关 `100.101.0.1`（CGNAT），没有公网 IPv4，IPv4 端口映射无意义。

将 IPv6 模式由 native 改为 **Passthrough（透传）** 后，入站即通。

### 验证证据（2026-09-19 00:21）
手机蜂窝网（源 `240e:452:df0f:265c:7c40:72ff:fe68:7fc0`）访问
`http://[240e:350:b:e2b:20c:29ff:fe92:dd79]:9999/`（该端口无监听）：

```
00:21:01.720885 IP6 240e:452:df0f:265c:7c40:72ff:fe68:7fc0.37556 >
                240e:350:b:e2b:20c:29ff:fe92:dd79.9999: Flags [S]
```

在本机 ens33 上抓到该 SYN —— 包已抵达。手机端显示的"拒绝连接"即本机内核对该关闭端口回的 RST；
**能收到 RST 恰恰是"包进得来"的判据**（被墙时只会超时，不会拒绝）。
随后用纯 HTTP 测试页（`python3 -m http.server 9998 --bind ::`）从蜂窝网成功打开，
排除 DNS 与证书干扰后确认链路可用。

### 透传模式的地址行为
切换后本机出现 ISP 侧 `240e:350:b::/48` 内的多个 /64（如 `240e:350:b:e2b::/64`、`240e:350:b:151a::/64`），
内核首选源地址变为 `240e:350:b:e2b:20c:29ff:fe92:dd79`（`valid_lft forever`，非 temporary）。
**不要改回 Auto**：Auto 可能自行选回 native，入站会再次中断。

### 打开入站后的暴露面（复核）
| 端口 | 服务 | 公网可达性 |
| --- | --- | --- |
| 22 | sshd | 可达，但**非局域网来源仅密钥**（`passwordauthentication no`，已用蜂窝地址实测） |
| 443 | nginx（下载通道） | 可达，设计如此；证书仅覆盖 `download.aurore.online`（无通配符，按 IP 访问必然告警） |
| 631 | cupsd | 监听 0.0.0.0/:: 但被 nftables 对非回环来源 DROP |
| 8080 / 8081 | — | 同上 DROP（`blockchain-port-guard.service`） |
| 其余 | postgres/redis/minio/前端/隧道源站 | 仅监听 127.0.0.1 |

## 变更 4：DDNS 选地址逻辑修复（2026-09-19）
原逻辑取 "`ip -6 addr show` 输出中第一个非 temporary/deprecated 的 /64"，
但该输出顺序不代表地址有效性，**曾两次把记录写成机器上已不存在的地址**
（`240e:357:1718:8f00:…`），导致下载通道不可达。
现改为优先采用内核为外网目的地选择的源地址：

```bash
ip -6 route get 2606:4700::1111 | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}'
```

原逻辑保留为回退。备份：`/home/euripides/backups/cloudflare-ddns-download.sh.orig-20260919`。

## 变更 5：下载通道改走 8443（运营商封锁入站 80/443）—— 已完成（2026-09-19）

### 根因
本线路为家宽 PPPoE（网关 `100.101.0.1`，CGNAT，无公网 IPv4）。IPv6 入站虽已由 Passthrough 打通，
但**运营商对入站 TCP 80/443 做丢弃**。同一地址、同一时段，两种互相独立的外部网络结论一致：

| 端口 | 外部访问结果 | 判据 |
| --- | --- | --- |
| 9999（无监听） | `net::ERR_CONNECTION_REFUSED` | 包**到达**，内核回 RST ✅ |
| 9998（临时 HTTP 服务） | 连接成功、页面正常 | 通 ✅ |
| 8443（nginx TLS） | 连接成功（取到 nginx 404 页） | 通 ✅ |
| 80 / 443 | 15 秒超时 | **被上游丢弃** ❌ |

这解释了下载通道自建成以来的日志里只有自测与同网段设备记录——**它从未被公网成功访问过**。

### 处理
1. nginx 下载 vhost 增加 `listen [::]:8443 ssl http2 ipv6only=on;`（保留 443 监听）。
   证书按 SNI 签发、与端口无关，**无需重签**。
   备份：`/home/euripides/backups/download-vhost.before-8443-20260919`
2. 前端接线——此前 `frontend/src/utils/download.ts` 的"直连优先 + 回退"逻辑是**死代码，无人调用**：
   - 新增共享模块 `frontend/packages/api-client/download.ts`：
     `DIRECT_DOWNLOAD_ORIGIN`（含端口）、`canUseDirectDownload()` 探测、`directTicketUrl()`。
   - `apps/user-console/src/pages/{ExportsPage,JobDetailPage}.tsx`：签发票据后先探测直连通道，
     可达则跳 `https://download.aurore.online:8443/api/v1/download/{ticket}`，
     否则回退主站隧道 URL —— **IPv4-only 客户端不受影响**。
   - 依据：导出票据与 v1 消费端点共用同一存储（`backend/internal/store/meta_v2.go` 的
     `CreateExportTicket`/`GetExportTicket`），故控制台签发的 v2 票据可在直连通道消费。
   - **后端代码未改动。**

### 验证
- `tsc -b`、`build:user`、`build:admin` 均 exit=0；两个控制台服务已重启（active）。
- 线上产物 `https://aurore.online/app-v2/assets/download-CsusrST3.js` → 200 且内含 `download.aurore.online:8443`。
- CORS 探测：`GET https://download.aurore.online:8443/download-health`（Origin: `https://aurore.online`）
  → `HTTP/2 204` + `access-control-allow-origin: https://aurore.online`。
- 外部可达：境外抓取服务访问 `https://download.aurore.online:8443/` 取到 nginx 404 页。

### 注意
- 隧道回退路径保留：`https://aurore.online/api/v1/exports/{id}/download`（受家宽上行带宽限制）。
- 443 监听保留但公网不可达（LAN 内仍可用）；若将来运营商解封，去掉端口即可。

## 其他
用户同时提供了 R2 的 S3 访问密钥（与 DDNS 无关，DDNS 只需要 API 令牌）：**未保存到任何文件**，
建议在 Cloudflare 删除该 R2 令牌。
