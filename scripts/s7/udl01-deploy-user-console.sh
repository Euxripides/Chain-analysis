#!/usr/bin/env bash
# udl01-deploy-user-console.sh — U-DL-01 前端 bundle 受控部署（staged build -> 备份 -> 原子替换 -> 服务端实测）
# 新文件（不 hot-edit 任何已运行脚本）。红线：不删旧 dist、不删任何证据；每步 rc 显式 gate。
set -uo pipefail
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
R="${R:-/home/euripides/projects/blockchain-platform}"
D="$R/frontend/apps/user-console"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/udl01-worker-$TS}"
STAGED="${STAGED:-}"
if [ -z "$STAGED" ] || [ ! -d "$STAGED" ]; then echo "STAGED_MISSING '$STAGED'"; exit 3; fi
if [ ! -f "$STAGED/index.html" ]; then echo "STAGED_NO_INDEX"; exit 3; fi
mkdir -p "$EV"; chmod 700 "$EV"
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS $1 :: $2" >> "$EV/deploy.log"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1 :: $2" >> "$EV/deploy.log"; }
echo "evidence=$EV" | tee "$EV/deploy.log"
echo "staged=$STAGED" | tee -a "$EV/deploy.log"
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee -a "$EV/deploy.log"

# --- P1 pre-state（旧 bundle 保留为备份；记录服务端实际服务的 sha） ---
OLD_INDEX_SHA="$(sha256sum "$D/dist/index.html" | cut -d' ' -f1)"
OLD_SERVED_SHA="$(curl -s -k https://127.0.0.1:8448/app-v2/ | sha256sum | cut -d' ' -f1)"
OLD_DOWNLOAD_CHUNK="$(grep -oE 'DownloadNewPage-[A-Za-z0-9_-]+\.js' "$D/dist/index.html" | head -1 || true)"
printf 'pre_index_sha=%s\npre_served_sha=%s\npre_download_chunk=%s\n' "$OLD_INDEX_SHA" "$OLD_SERVED_SHA" "$OLD_DOWNLOAD_CHUNK" > "$EV/version-binding-pre.txt"
[ -n "$OLD_INDEX_SHA" ] && ok P1 "旧 dist index.html sha=$OLD_INDEX_SHA" || bad P1 "读不到旧 index.html"
[ "$OLD_INDEX_SHA" = "$OLD_SERVED_SHA" ] && ok P2 "服务端服务的 index.html 与磁盘一致（部署前基线）" || bad P2 "served=$OLD_SERVED_SHA disk=$OLD_INDEX_SHA"

NEW_INDEX_SHA="$(sha256sum "$STAGED/index.html" | cut -d' ' -f1)"
WORKER_CHUNK="$(cd "$STAGED/assets" && ls import-parse.worker-*.js 2>/dev/null | head -1)"
[ -n "$WORKER_CHUNK" ] && ok P3 "staged 产物含独立 Worker chunk $WORKER_CHUNK" || bad P3 "staged 产物缺少 import-parse.worker-*.js"
grep -q "import-parse.worker" "$STAGED/assets/$(ls "$STAGED/assets" | grep -E '^DownloadNewPage-.*\.js$' | head -1)" && ok P4 "DownloadNewPage chunk 引用了 Worker" || bad P4 "chunk 里找不到 Worker 引用"

# --- P2 备份 + 原子替换（旧目录一律保留，不删除） ---
cp -a "$D/dist" "$D/dist.bak-udl01-$TS"; B1=$?
mv "$D/dist" "$D/dist.prev-udl01-$TS"; B2=$?
cp -a "$STAGED" "$D/dist"; B3=$?
[ "$B1" -eq 0 ] && ok P5 "旧 dist 已备份为 dist.bak-udl01-$TS" || bad P5 "备份失败 rc=$B1"
[ "$B2" -eq 0 ] && ok P6 "旧 dist 已移动保留为 dist.prev-udl01-$TS" || bad P6 "移动失败 rc=$B2"
[ "$B3" -eq 0 ] && ok P7 "staged 已就位为 dist" || bad P7 "就位失败 rc=$B3"
[ "$(sha256sum "$D/dist/index.html" | cut -d' ' -f1)" = "$NEW_INDEX_SHA" ] && ok P8 "新 dist index.html sha=$NEW_INDEX_SHA" || bad P8 "新 dist index.html sha 不符"

# --- P3 重启服务 + 有界就绪等待 ---
systemctl --user restart blockchain-user-console; SVC=$?
[ "$SVC" -eq 0 ] && ok P9 "blockchain-user-console restarted" || bad P9 "restart rc=$SVC"
READY=0
for i in $(seq 1 30); do
  if curl -s -k -o /dev/null -w '%{http_code}' https://127.0.0.1:8448/app-v2/ | grep -q 200; then READY=1; break; fi
  sleep 1
done
[ "$READY" -eq 1 ] && ok P10 "app-v2 有界就绪（$i 次尝试）" || bad P10 "app-v2 就绪超时"
systemctl --user is-active --quiet blockchain-user-console && ok P11 "服务 active" || bad P11 "服务非 active"

# --- P4 服务端实测：sha 一致 + 新 chunk + worker chunk + CSP ---
NEW_SERVED_SHA="$(curl -s -k https://127.0.0.1:8448/app-v2/ | sha256sum | cut -d' ' -f1)"
[ "$NEW_SERVED_SHA" = "$NEW_INDEX_SHA" ] && ok P12 "served index.html == 新 dist sha" || bad P12 "served=$NEW_SERVED_SHA new=$NEW_INDEX_SHA"
NEW_DL_CHUNK="$(grep -oE 'DownloadNewPage-[A-Za-z0-9_-]+\.js' "$D/dist/index.html" | head -1)"
C1="$(curl -s -k -o "$EV/served-download-chunk.js" -w '%{http_code}' "https://127.0.0.1:8448/app-v2/assets/$NEW_DL_CHUNK")"
C2="$(curl -s -k -o "$EV/served-worker-chunk.js" -w '%{http_code}' "https://127.0.0.1:8448/app-v2/assets/$WORKER_CHUNK")"
[ "$C1" = "200" ] && ok P13 "新 DownloadNewPage chunk 200 ($NEW_DL_CHUNK)" || bad P13 "chunk http=$C1"
[ "$C2" = "200" ] && ok P14 "Worker chunk 200 ($WORKER_CHUNK)" || bad P14 "worker chunk http=$C2"
[ "$(sha256sum "$EV/served-download-chunk.js" | cut -d' ' -f1)" = "$(sha256sum "$D/dist/assets/$NEW_DL_CHUNK" | cut -d' ' -f1)" ] && ok P15 "服务端 chunk 字节与磁盘一致" || bad P15 "chunk 字节不一致"
[ -f "$D/dist.prev-udl01-$TS/index.html" ] && [ "$(sha256sum "$D/dist.prev-udl01-$TS/index.html" | cut -d' ' -f1)" = "$OLD_INDEX_SHA" ] && ok P16 "旧 dist 原样保留（可回滚）" || bad P16 "旧 dist 校验失败"
CSP="$(curl -s -k -D - -o /dev/null https://127.0.0.1:8448/app-v2/ | grep -ci "content-security-policy")"
[ "$CSP" -ge 1 ] && ok P17 "CSP 头在位" || bad P17 "缺少 CSP 头"

printf 'post_index_sha=%s\npost_served_sha=%s\npost_download_chunk=%s\nworker_chunk=%s\nbackup_dir=dist.bak-udl01-%s\nprev_dir=dist.prev-udl01-%s\n' \
  "$NEW_INDEX_SHA" "$NEW_SERVED_SHA" "$NEW_DL_CHUNK" "$WORKER_CHUNK" "$TS" "$TS" > "$EV/version-binding-post.txt"
echo "PASS=$PASS FAIL=$FAIL" | tee -a "$EV/deploy.log"
if [ "$FAIL" -ne 0 ]; then echo "UDL01_DEPLOY_VERDICT=FAIL"; exit 1; fi
echo "UDL01_DEPLOY_VERDICT=PASS"
