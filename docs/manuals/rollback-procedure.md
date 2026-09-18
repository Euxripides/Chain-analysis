# 回滚操作手册（双系统平台）

> 适用环境：部署机 `dataserver`（Linux/amd64），项目根 `/home/euripides/projects/blockchain-platform`，
> 服务由 **systemd --user** 单元管理（`blockchain-api` / `blockchain-worker` / `blockchain-user-console` / `blockchain-admin-console`）。
> 本文所有结论都来自 2026-09-16 的真机实测：可启动性测试与两轮回滚演练（证据见 `acceptance-evidence/dual-system-v1/sprint7/release/`）。

## 1. 可回滚产物（已归档 + 已实测可启动）

归档目录：`~/deploy-backups/rollback-20260916T1735Z/`（`SHA256SUMS.txt` 为清单）

| 角色 | 文件 | sha256 | 可启动性实测（2026-09-16） |
|---|---|---|---|
| **上一版 API** | `api.previous`（= `api.pausefix-20260915T092044Z`，2026-09-15 16:20） | `cbb8d01b8067b6b2a1a6acc6272b68b7ba8e5808427cab6911a8690d528a66d7` | ✅ 指向**一次性恢复库**启动后 `/health/live` 返回 200 |
| **上一版 Worker** | `worker.previous`（同批次） | `4379522a7eeaf92871e357227cb24378413baf71cd64958614fafc929b6fcd45` | ✅ 启动到 `worker started`（`WORKER_METRICS_PORT=18098` 避让现网端口） |
| 当前冻结 API | `api.current-frozen` | `7831bff0bc6127963840aba8f505b0c337243e2c69a871f2b4010e4dbec19e32` | ✅ 现网运行中 |
| 当前冻结 Worker | `worker.current-frozen` | `0d49fb75105eb5ba81a0b062fa557fc09893d2a43555989e6128451a2c1f2c78` | ✅ 现网运行中 |
| 当前冻结 migrate | `migrate.current-frozen` | `d48e58fec83bc2af83ed322cf3f47da2f84ed4fe7f3873c1887c5481d06bf062` | — |

### ⚠️ 不可用于回滚的产物（实测结论）

`~/deploy-backups/tron-address-catalog-20260909/{api,worker}`（2026-09-09）：实测**启动即失败**
```
bootstrap failed: create schema_migrations: ERROR: permission denied for schema public (SQLSTATE 42501)
```
原因：该版本的启动路径要创建 `schema_migrations` 表（需要 DDL），而运行时角色已按迁移 `0054` 降权为 `blockchain_app`（无 DDL 权限）。
**结论：跨"权限加固边界"只替换二进制不可行**；回滚产物必须与当前权限/迁移时代兼容（或配套临时授权方案）。

## 2. 后端回滚步骤（实测 RTO ≈ 数秒~1 分钟）

```bash
cd /home/euripides/projects/blockchain-platform
D=~/deploy-backups/rollback-20260916T1735Z

# 1) 记录回滚前状态（留证据，便于回滚失败时判断现场）
sha256sum backend/bin/api backend/bin/worker | tee /tmp/rollback-before.txt
curl -s -o /dev/null -w "ready=%{http_code}\n" http://127.0.0.1:8080/health/ready

# 2) 停服务（中断从这里开始）
systemctl --user stop blockchain-api blockchain-worker

# 3) 装入上一版二进制
cp -f "$D/api.previous"    backend/bin/api
cp -f "$D/worker.previous" backend/bin/worker
chmod +x backend/bin/api backend/bin/worker
sha256sum backend/bin/api backend/bin/worker   # 应与 $D/SHA256SUMS.txt 中 api.previous/worker.previous 一致

# 4) 启动并等待健康（旧版本启动较慢，实测 20~40 s 才 listen；不要用 5 s 就判失败）
systemctl --user start blockchain-api blockchain-worker
for i in $(seq 1 60); do
  code=$(curl -s -o /dev/null -m 2 -w '%{http_code}' http://127.0.0.1:8080/health/live || echo 000)
  [ "$code" = "200" ] && { echo "health/live OK after ${i}s"; break; }
  sleep 1
done
curl -s -o /dev/null -w "ready=%{http_code}\n" http://127.0.0.1:8080/health/ready
```

## 3. 前端回滚步骤（实测 RTO ≈ 1 s）

```bash
cd /home/euripides/projects/blockchain-platform/frontend/apps
# 上一版产物位于同目录的 dist.prev-* / dist.bak-*；先确认要与哪一版对齐
ls -dt user-console/dist.prev-* admin-console/dist.prev-* | head -4

systemctl --user stop blockchain-user-console blockchain-admin-console
mv user-console/dist  /tmp/uc.dist.frozen     # 现场保留，便于回滚回去
cp -a user-console/dist.prev-<TS>  user-console/dist
mv admin-console/dist /tmp/ac.dist.frozen
cp -a admin-console/dist.prev-<TS> admin-console/dist
systemctl --user start blockchain-user-console blockchain-admin-console

# 校验：服务端**实际下发**的入口哈希必须等于上一版磁盘产物
curl -s http://127.0.0.1:3001/app-v2/ | sha256sum
sha256sum user-console/dist/index.html
```

## 4. 回滚后再前进（把系统恢复回冻结版本）

```bash
D=~/deploy-backups/rollback-20260916T1735Z
systemctl --user stop blockchain-api blockchain-worker
cp -f "$D/api.current-frozen"    backend/bin/api
cp -f "$D/worker.current-frozen" backend/bin/worker
chmod +x backend/bin/api backend/bin/worker
systemctl --user start blockchain-api blockchain-worker
# 校验哈希必须逐条等于冻结值（api 7831bff0… / worker 0d49fb75…），/health/ready 必须 200
```

## 5. 一键演练（推荐在维护窗口执行，先演练再真回滚）

```bash
bash scripts/release/rollback-drill.sh
```
脚本会：记录基线 → 回滚后端 → 回滚前端 → **回滚后再前进** → 逐条校验哈希与健康，并写出
`acceptance-evidence/dual-system-v1/sprint7/release/rollback-drill-<TS>.{md,json}`；
带 `trap`：任何一步异常退出都会先把冻结版本装回去，不会把系统留在半回滚状态。

## 6. 已知限制

1. **数据库迁移不回退**：回滚只回退二进制/前端，迁移头（当前 `0065`）保持不变；因此上一版必须能容忍当前 schema。
2. **权限模型变化会阻断回滚**：如 `0054` 降权之后，更早的、启动时需要 DDL 的版本无法启动（见 §1 ⚠️）。
3. **回滚窗口内 API/Worker 中断**：实测前滚 RTO 23.30 s、前端回滚 1.03 s / 0.02 s；后端回滚取决于版本，务必按 §5 先演练。
4. **演练目标与归档产物不自动清理**：便于复核，需要人工确认后删除。
