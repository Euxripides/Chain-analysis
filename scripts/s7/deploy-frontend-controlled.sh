#!/usr/bin/env bash
# deploy-frontend-controlled.sh — 前端受控部署（备份 -> staged 构建 -> 原子替换 -> 重启 -> 服务端实测）。
#
# 为什么需要它（2026-09-16 的真实教训）：
#   `npm run build:user` / `build:admin` 会按 vite.config 的 emptyOutDir **就地重写** apps/*/dist，
#   而运行中的 vite preview 直接从 dist 读取 —— 于是"构建即部署"，且上一版产物被清空、
#   部署失败时没有任何回滚材料。本脚本把顺序固定下来：
#     1) 先记录 pre-state（磁盘 index sha / 服务端实际服务的 sha）；
#     2) 用 --outDir 构建到 dist.staged-<ts>，线上目录全程不动；
#     3) 备份 dist -> dist.bak-<ts>（回滚材料，永不删除）；
#     4) 原子替换：mv dist dist.prev-<ts> && mv staged dist；
#     5) 重启服务 + 有界就绪等待 + 实测（index sha 一致、入口 chunk 可访问）。
#
# 用法：
#   APP=user  bash scripts/s7/deploy-frontend-controlled.sh            # 自建并部署用户台
#   APP=admin bash scripts/s7/deploy-frontend-controlled.sh            # 自建并部署管理台
#   APP=user STAGED=/abs/dist bash scripts/s7/deploy-frontend-controlled.sh   # 部署已有产物
set -uo pipefail
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
R="${R:-/home/euripides/projects/blockchain-platform}"
APP="${APP:-}"
case "$APP" in
  user)  D="$R/frontend/apps/user-console";  SVC="blockchain-user-console";  URL="https://127.0.0.1:8448/app-v2/";  BASE="/app-v2/" ;;
  admin) D="$R/frontend/apps/admin-console"; SVC="blockchain-admin-console"; URL="https://127.0.0.1:8448/admin/";  BASE="/admin/" ;;
  *) echo "用法：APP=user|admin bash $0"; exit 2 ;;
esac
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/frontend-deploy-$APP-$TS}"
STAGED="${STAGED:-}"
mkdir -p "$EV"; chmod 700 "$EV"
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS $1 :: $2" | tee -a "$EV/deploy.log"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1 :: $2" | tee -a "$EV/deploy.log"; }
echo "app=$APP service=$SVC evidence=$EV" | tee "$EV/deploy.log"
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee -a "$EV/deploy.log"

# --- pre-state ---
OLD_DISK_SHA="$(sha256sum "$D/dist/index.html" 2>/dev/null | cut -d' ' -f1)"
OLD_SERVED_SHA="$(curl -s -k "$URL" | sha256sum | cut -d' ' -f1)"
printf 'pre_disk_index_sha=%s\npre_served_sha=%s\n' "$OLD_DISK_SHA" "$OLD_SERVED_SHA" > "$EV/version-binding-pre.txt"
[ -n "$OLD_DISK_SHA" ] && ok P1 "部署前磁盘 index.html sha=$OLD_DISK_SHA" || bad P1 "读不到 $D/dist/index.html"
[ "$OLD_DISK_SHA" = "$OLD_SERVED_SHA" ] && ok P2 "部署前服务端与磁盘一致" || bad P2 "served=$OLD_SERVED_SHA disk=$OLD_DISK_SHA"

# --- 构建到 staged（线上目录不动） ---
if [ -z "$STAGED" ]; then
  STAGED="$D/dist.staged-$TS"
  rm -rf "$STAGED"
  ( cd "$R/frontend" && PATH="$HOME/.nvm/versions/node/v24.20.0/bin:$PATH" \
      npx vite build --config "apps/$APP-console/vite.config.ts" --outDir "$STAGED" --emptyOutDir ) \
      > "$EV/build.log" 2>&1
  BUILD_RC=$?
  [ "$BUILD_RC" -eq 0 ] && ok P3 "staged 构建成功（--outDir 隔离，线上目录未动）" || bad P3 "构建失败 rc=$BUILD_RC（见 $EV/build.log）"
  [ "$BUILD_RC" -eq 0 ] || { echo "=== summary === PASS=$PASS FAIL=$FAIL"; echo "DEPLOY_FAIL=$FAIL"; exit 1; }
else
  ok P3 "使用已有产物 STAGED=$STAGED"
fi
[ -f "$STAGED/index.html" ] && ok P4 "staged 含 index.html" || { bad P4 "staged 缺少 index.html"; echo "DEPLOY_FAIL=1"; exit 1; }
NEW_INDEX_SHA="$(sha256sum "$STAGED/index.html" | cut -d' ' -f1)"
ENTRY_CHUNK="$(grep -oE 'assets/index-[A-Za-z0-9_-]+\.js' "$STAGED/index.html" | head -1 || true)"
[ -n "$ENTRY_CHUNK" ] && ok P5 "发现入口 chunk $ENTRY_CHUNK" || { bad P5 "无法从 index.html 发现入口 chunk（禁止用空名继续）"; echo "DEPLOY_FAIL=1"; exit 1; }

# --- 备份 + 原子替换 ---
cp -a "$D/dist" "$D/dist.bak-$TS" && ok P6 "旧 dist 已备份为 dist.bak-$TS（回滚材料）" || bad P6 "备份失败"
mv "$D/dist" "$D/dist.prev-$TS" && ok P7 "旧 dist 已保留为 dist.prev-$TS" || bad P7 "移动旧 dist 失败"
cp -a "$STAGED" "$D/dist" && ok P8 "staged 已就位为 dist" || bad P8 "就位失败"
[ "$(sha256sum "$D/dist/index.html" | cut -d' ' -f1)" = "$NEW_INDEX_SHA" ] && ok P9 "新 dist index.html sha=$NEW_INDEX_SHA" || bad P9 "新 dist sha 与 staged 不一致"

# --- 重启 + 有界就绪 ---
systemctl --user restart "$SVC"; SVC_RC=$?
[ "$SVC_RC" -eq 0 ] && ok P10 "$SVC restarted" || bad P10 "restart rc=$SVC_RC"
READY=0
for i in $(seq 1 30); do
  code=$(curl -s -k -o /dev/null -w '%{http_code}' "$URL")
  if [ "$code" = "200" ]; then READY=1; break; fi
  sleep 1
done
[ "$READY" -eq 1 ] && ok P11 "$BASE 有界就绪（第 $i 次探测）" || bad P11 "$BASE 就绪超时"
systemctl --user is-active --quiet "$SVC" && ok P12 "服务 active" || bad P12 "服务非 active"

# --- 服务端实测 ---
NEW_SERVED_SHA="$(curl -s -k "$URL" | sha256sum | cut -d' ' -f1)"
[ "$NEW_SERVED_SHA" = "$NEW_INDEX_SHA" ] && ok P13 "服务端服务的 index.html 与新产物一致（$NEW_SERVED_SHA）" || bad P13 "served=$NEW_SERVED_SHA disk=$NEW_INDEX_SHA"
CHUNK_CODE="$(curl -s -k -o "$EV/served-entry-chunk.js" -w '%{http_code}' "https://127.0.0.1:8448$BASE$ENTRY_CHUNK")"
[ "$CHUNK_CODE" = "200" ] && ok P14 "入口 chunk 可访问（$ENTRY_CHUNK）" || bad P14 "chunk $ENTRY_CHUNK http=$CHUNK_CODE"
printf 'new_index_sha=%s\nentry_chunk=%s\n' "$NEW_INDEX_SHA" "$ENTRY_CHUNK" > "$EV/version-binding-post.txt"

echo "=== summary === PASS=$PASS FAIL=$FAIL"
echo "rollback: cp -a $D/dist.bak-$TS $D/dist && systemctl --user restart $SVC"
echo "DEPLOY_FAIL=$FAIL"
exit 0
