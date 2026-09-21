# Nginx + Cloudflare 透明代理部署预检

- 执行时间：2026-09-07 16:51（Asia/Bangkok，目标主机时间）
- 目标主机：`dataserver` / `192.168.31.152`
- 项目目录：`/home/euripides/projects/blockchain-platform`
- 结论：**允许实施本机 Nginx 阶段；禁止直接切换未知正式 hostname。**

## 预检结果

| 检查项 | 现场结果 | 判定 |
|---|---|---|
| `:3000` bind | `0.0.0.0:3000`，PID 10364 | PASS，可由 `127.0.0.1` 访问；本次不改 bind |
| `:3000` health | `GET /` = 200，HTML 710 bytes；`GET /api/v1/me` = 200 | PASS |
| 当前 Cloudflare hostname | 服务器配置、环境变量、shell history 和项目文件均未发现 hostname | **UNKNOWN：不得盲切正式域名** |
| 当前 Tunnel origin | 两个 connector 的 orchestration config version 均为 0；请求分别为 143/793，503 分别为 142/792 | **与文档“当前 origin=:3000”的假设不一致** |
| cloudflared 管理方式 | remotely-managed token tunnel；Tunnel ID `bdaee7b6-c649-46e9-9e07-7e2b4e32e773` | 已确认 |
| cloudflared 运行状态 | systemd connector 1 个，另有附着在 root shell 的手工 connector 1 个；均属于同一 Tunnel | 风险：重复且手工进程不受监管 |
| WebSocket | 代码与路由未发现实际 WebSocket endpoint | N/A；配置仍保留 Upgrade/Connection 透传 |
| SSE | `GET /api/v1/download-jobs/{id}/events`；实测立即返回事件；应用设置 `X-Accel-Buffering: no` | PASS；需要代理后复测流式到达 |
| 上传 | `POST /api/v1/admin/contracts/{chain}/{address}/abi`，JSON 请求体 | 代理后验证小请求；无 multipart 大文件入口证据 |
| 导出 | `/api/v1/exports`、`/download`、`/download-ticket`；支持流式与 Range | 代理后验证元数据、Range 与文件头 |
| Auth / Cookie | V1 为 `X-User-*` 头注入；无登录会话或 Set-Cookie 机制；默认 owner | 代理需保持请求头；Cookie 无适用场景 |
| 防火墙 | UFW inactive；nftables 仅有 Docker 规则 | 不新增规则 |
| 当前公网/全接口监听 | `22`、`631`、`3000`、`8080`、`8081`；其余依赖为 loopback | 记录现状；本次只新增 loopback `8088` |
| `8088` 占用 | 未监听 | PASS |
| systemd | cloudflared enabled/active；nginx 未安装 | 可实施安装 |
| 资源 | RAM 23 GiB、可用约 21 GiB；根盘可用 87 GiB | PASS |

## 基线响应

- `/`：HTTP 200，`Content-Type: text/html`，710 bytes。
- `/api/v1/me`：HTTP 200，默认用户 `admin@local`、角色 `owner`。
- SSE：HTTP 200，`Content-Type: text/event-stream`，3 秒内收到 609 bytes 初始快照，curl 超时是预期的长连接行为。
- Cloudflare connector metrics：`request_errors=0` 但全部业务请求为 HTTP 503；因此不能把 connector 已连接边缘节点当作业务可用。

## 实施门禁

1. 先做 root-only 备份，再安装 Nginx。
2. Nginx 仅监听 `127.0.0.1:8088`，单一 `location /` 透明转发到 `127.0.0.1:3000`。
3. 本机源站/代理比较、SSE、API、导出、UI 回归通过后，才测试 Cloudflare 临时 hostname。
4. 未确认正式 hostname、未完成临时 hostname 回归前，不执行正式 hostname 切换。
5. 不修改数据库、Provider、链上采集逻辑、应用路由和 `:3000` bind；不恢复当前隔离中的生产批处理。
