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
