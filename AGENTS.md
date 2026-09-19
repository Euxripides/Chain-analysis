# Repository instructions

开发本项目时必须遵守 [docs/code-standards.md](docs/code-standards.md)。

前端特别约束：所有警告、说明、成功、错误、风险和确认类提示只能使用弹窗呈现；禁止将提示横幅、提示卡、警告条或说明段落直接嵌入页面布局。页面正文仅保留业务数据、字段标签、导航和操作控件。

---

## 服务器侧开发（dataserver）

本节描述**在这台部署机上开发**的环境与流程；在其他机器上开发可忽略。

### 环境

- 仓库：`/home/euripides/projects/blockchain-platform`，分支 `main` 跟踪 `origin/main`
- Node：`~/.nvm/versions/node/v24.20.0/bin` —— **不在默认 PATH**，命令前先
  `export PATH=$HOME/.nvm/versions/node/v24.20.0/bin:$PATH`
- 前端工作目录：`frontend/`（pnpm workspace）
- git 走 SSH：`~/.ssh/id_ed25519_chain`（deploy key，含写权限），`~/.ssh/config` 与 `known_hosts` 已配好
- `core.fileMode=false`（消除 Windows/Linux 权限位差异）

### 改完代码怎么上线（一条命令）

```bash
~/deploy-frontend.sh                # 拉取 + 部署用户端与管理台
~/deploy-frontend.sh user           # 只部署用户端
~/deploy-frontend.sh both --verify  # 部署后再跑完整浏览器验收（约 3 分钟）
```

它内部调用 `scripts/s7/deploy-frontend-controlled.sh`：staged 构建 → 备份 `dist.bak-*`
→ 原子替换 → 重启 → 自检。**线上目录全程不会被半成品覆盖**，回滚材料自动保留。

- 服务（systemd user）：`blockchain-user-console`(:3001)、`blockchain-admin-console`(:3002)、`blockchain-api`
  重启：`systemctl --user restart <service>`
- nginx：`/etc/nginx/sites-enabled/blockchain-platform.conf`，`/app-v2/`→3001、`/admin/`→3002
  仅服务器本机监听 `https://127.0.0.1:8448`

### 提交前必须过的门禁

```bash
cd frontend
pnpm run typecheck                          # 两个 app + 全部 packages
node scripts/uc-contract-freeze-check.mjs   # 路由 / 权限 / API 字面量 / 案件 Tab 权限契约
E2E_BROWSER_CHANNEL=chromium node scripts/uc-v4-render-check.mjs   # 真实浏览器验收
```

注意：本机是 Linux，Playwright 浏览器没有 msedge，必须显式传 `E2E_BROWSER_CHANNEL=chromium`。
完整门禁清单见 `docs/code-standards.md`（仓库根）与 `frontend/scripts/*.mjs`。

### 本机特有的坑

- **换行符**：Linux 产出 LF，仓库存 LF，一致。在 Windows 上改同一文件会带进 CRLF，
  `git status` 会显示整文件改动（`git diff --ignore-all-space` 可确认是纯 EOL）。**统一在本机改可避免。**
- **重新生成的验收产物**：`frontend/acceptance-evidence/v4-render-check/**` 已设为
  `git update-index --skip-worktree`，本地重新生成不再污染 `git status`。
  真要提交新截图时先 `git update-index --no-skip-worktree <path>`。
- **工作区存在历史本地改动**（`.work/`、`deploy/nginx-blockchain-platform.conf`、
  `docs/implementation/*` 等）。`git pull` 若被拒，多半是它们挡住了快进合并。

### 权限相关的既有约定

案件详情 7 个子 Tab 的权限唯一定义在 `frontend/apps/user-console/src/app/case-tabs.ts`，
由 `uc-contract-freeze-check.mjs` 冻结（含 AND/OR 组合方式与路由守卫接入）。
证据 Tab 的 `case:read + evidence:list + investigation:list` 是**临时兼容规则**，
见 `docs/adr/ADR-022-case-evidence-tab-permission.md` —— 未做产品决策前不要改动它。

## DSH Web（局域网访问，常驻服务）

**拓扑**：`nginx:8450 (TLS, 自签)` → `127.0.0.1:43130 (dsh web, 仅 loopback)`。
局域网不直接暴露 dsh：绑定退回 loopback 后 `/api` 围栏对局域网 authority 恢复 403，
局域网流量只走插件门控的 `/remote` 通道（启动日志里的 `CRITICAL — /api fence is OPEN` 因此归零）。

- 服务：`dsh-web.service`（已 enable，开机自启）——`systemctl status|restart dsh-web`，日志 `journalctl -u dsh-web`。
- 绑定 `127.0.0.1:43130` 由 `~/.dsh/profiles/web/cordis.patch.yml` 的受管 lan-bind 块固定。
  **不要**用 `dsh web --host 0.0.0.0`（CLI 明确拒绝）。
- 反代拓扑靠 systemd 单元里的环境变量驱动（profile 覆盖不到插件配置，只能用 env）：
  `DSH_REMOTE_PUBLIC_BASE_URL=https://192.168.31.152:8450`、`DSH_REMOTE_TRUSTED_HOSTS=192.168.31.152:8450`；
  配对链接据此自动生成为 https。
- 证书：自签，`/etc/ssl/dsh-web/{cert,key}.pem`，SAN 含 `IP:192.168.31.152` 与 `DNS:dataserver`，有效期至 2028-12-22；
  浏览器首次访问需手动信任一次。nginx 站点：`/etc/nginx/sites-available/dsh-web.conf`。

**用户入口**

- 一次配对（长期有效）：`bash ~/dsh-lan-link.sh` 铸链接 → 浏览器打开 → 之后收藏 `https://192.168.31.152:8450/`，
  刷新、书签、新标签页均正常（已实测连续刷新 3 次 200）。配对 cookie `dsh_pair` 有效期 1 年，
  设备库 `~/.dsh/remote-web-ui-devices.json`；一次吊销全部设备：loopback `POST /api/pair/stop`。
- 免配对临时用：`bash ~/dsh-lan-url.sh` 打印带 launch token 的 https URL，打开一次即兑换 `dsh-auth` cookie，
  根路径即可正常使用（cookie 30 天，但随每次 dsh-web 重启失效）。

**为什么 nginx 要把带配对 cookie 的 `/` 内部重写为 `/pair-app`**

- 根路径 `/` 归 harness 管，要求 `dsh-auth` cookie；配对 cookie 只门控插件的 `/remote` 通道，管不到 `/`。
  而客户端路由会把地址栏改写成 `/`，于是在 `/` 上一刷新就 401（`dsh web authentication required`）。
- 插件的官方解法是 `/pair-app.sw.js` service worker 接管 `/`，但 **SW 脚本抓取不认自签证书的例外**
  （`SecurityError: An SSL certificate error occurred when fetching the script`），自签下无法注册。
- 故在 nginx 里做等价转发；无配对 cookie 时不改动，保持原语义。将来若换成受信任证书，
  可删掉这段让 SW 原生接管。
- 安全性已核：`/pair-app` 在无 cookie 或伪造 cookie 下只返回 785 字节配对提示页，**不含** `__DSH_BOOT__`。

**插件与陷阱**

- 插件管理：`dsh plugin --profile web <pnpm 参数>`。原生构建白名单写在
  `~/.dsh/profiles/web/pnpm-workspace.yaml` 的 `allowBuilds`（pnpm 11 已不读 package.json 的 `pnpm.onlyBuiltDependencies`）。
- 已按需精简：卸载 `dsh-chat-import`；关闭聚合包自带的皮肤中心（`cordis.patch.yml` 里
  `- id: web-ui-skin-center` + `disabled: true`，聚合包不能整体卸载）。
- **版本陷阱**：npm 版 `@deepseek-ai/dsh-settings`（实测到 0.1.6-alpha.2）都不导出 `settingsNamespace`，
  那是 Desktop 私有构建才有的 API；故 `dsh-better-sidebar` 必须 ≥ 0.18.0（当前 0.19.1）。
- 聚合包 `@linxin666/dsh-web-all` 的 family 行 config 只转发 `plugin` 键，插件自身配置项（如 `tokenTtlMs`）
  无法从 profile 覆盖——已实测并回退，勿重复尝试；改用该插件的环境变量入口。配对令牌默认 10 分钟有效、一次性。
