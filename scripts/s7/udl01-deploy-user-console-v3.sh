#!/usr/bin/env bash
# udl01-deploy-user-console-v3.sh — U-DL-01 前端 bundle 受控部署。
#   PHASE1 precheck（只读，失败立即退出且绝不触碰现网）
#   PHASE2 复制到同父目录临时路径 + 逐字节 manifest 比对
#   PHASE3 可恢复 rename 序列切换（备份 -> dist 移到 prev -> 临时路径就位为 dist）
#   PHASE4 重启/就绪/服务端字节门；任一失败 -> 用刚冻结的 prev 自动恢复并验证旧 served SHA
#
# 为什么有 v3（真实缺陷记录；v1/v2 一律原样保留未改，禁止再用 v2 部署）：
#   v1（PASS=16/FAIL=1）：从 dist/index.html grep DownloadNewPage-*.js；Vite 入口 chunk 是 index-*.js，
#     该 grep 恒为空 -> 用空名 curl /assets/ 得到 200（其实是 index.html）-> P15 字节不一致。
#   v2（sha 5667f6bc650f4e43fccaa2494d53f43044882dbe612e679a3cca98ebfe8b7b58）：修了 chunk 发现，
#     但 precheck 的 bad 只累加 FAIL 仍继续 cp/mv/restart；cp/mv 任一步失败也照常 restart，
#     可能留下半部署/空 dist 且无自动恢复 —— 根 2026-09-13 独立审查判定禁止使用。
#
# 校准：全部外部依赖都可用环境变量替换，负向校准在隔离临时目录 + 可控服务替身上进行，
# 绝不向真实用户服务注入故障。
#   DEPLOY_APP_DIR / DEPLOY_DIST_DIR / DEPLOY_SERVICE / DEPLOY_CURL_OPTS
#   DEPLOY_SERVED_URL / DEPLOY_ASSET_BASE / DEPLOY_RESTART_CMD / DEPLOY_ACTIVE_CMD / DEPLOY_READY_TRIES
set -uo pipefail
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
R="${R:-/home/euripides/projects/blockchain-platform}"
D="${DEPLOY_APP_DIR:-$R/frontend/apps/user-console}"
DIST="${DEPLOY_DIST_DIR:-$D/dist}"
SERVICE="${DEPLOY_SERVICE:-blockchain-user-console}"
SERVED_URL="${DEPLOY_SERVED_URL:-https://127.0.0.1:8448/app-v2/}"
ASSET_BASE="${DEPLOY_ASSET_BASE:-https://127.0.0.1:8448/app-v2/assets}"
CURL_OPTS="${DEPLOY_CURL_OPTS:--s -k}"
RESTART_CMD="${DEPLOY_RESTART_CMD:-systemctl --user restart $SERVICE}"
ACTIVE_CMD="${DEPLOY_ACTIVE_CMD:-systemctl --user is-active --quiet $SERVICE}"
READY_TRIES="${DEPLOY_READY_TRIES:-30}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/udl01-deploy-v3-$TS}"
STAGED="${STAGED:-}"

if [ -z "$STAGED" ] || [ ! -d "$STAGED" ]; then echo "STAGED_MISSING $STAGED"; exit 3; fi
if [ ! -f "$STAGED/index.html" ]; then echo "STAGED_NO_INDEX"; exit 3; fi
if [ ! -d "$DIST" ]; then echo "DIST_MISSING $DIST"; exit 3; fi
mkdir -p "$EV"; chmod 700 "$EV"
LOG="$EV/deploy.log"
PASS=0; FAIL=0; PRECHECK_FAILS=""; POST_FAILS=""
ok(){ PASS=$((PASS+1)); echo "PASS $1 :: $2" >> "$LOG"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1 :: $2" >> "$LOG"; }
prechk(){ if [ "$2" = 1 ]; then ok "$1" "$3"; else bad "$1" "$3"; PRECHECK_FAILS="$PRECHECK_FAILS $1"; fi; }
postchk(){ if [ "$2" = 1 ]; then ok "$1" "$3"; else bad "$1" "$3"; POST_FAILS="$POST_FAILS $1"; fi; }

echo "evidence=$EV" | tee "$LOG"
echo "script_sha256=$(sha256sum "$0" | cut -d" " -f1)" | tee -a "$LOG"
echo "staged=$STAGED" | tee -a "$LOG"
echo "app_dir=$D dist=$DIST service=$SERVICE" | tee -a "$LOG"
echo "served_url=$SERVED_URL asset_base=$ASSET_BASE restart_cmd=$RESTART_CMD" | tee -a "$LOG"

sha_of(){ if [ -f "$1" ]; then sha256sum "$1" | cut -d" " -f1; else echo ""; fi; }
discover(){ ls "$1" 2>/dev/null | grep -E "$2" | head -1; }

echo "=== PHASE1 precheck（只读；任一失败即退出且不触碰现网） ==="
STAGED_PAGE="$(discover "$STAGED/assets" "^DownloadNewPage-.*[.]js$")"
STAGED_WORKER="$(discover "$STAGED/assets" "^import-parse[.]worker-.*[.]js$")"
NEW_INDEX_SHA="$(sha_of "$STAGED/index.html")"
OLD_INDEX_SHA="$(sha_of "$DIST/index.html")"
OLD_PAGE="$(discover "$DIST/assets" "^DownloadNewPage-.*[.]js$")"
OLD_WORKER="$(discover "$DIST/assets" "^import-parse[.]worker-.*[.]js$")"
OLD_SERVED_SHA="$(curl $CURL_OPTS "$SERVED_URL" | sha256sum | cut -d" " -f1)"
REFS=0; U5U=0; U5C=0
if [ -n "$STAGED_PAGE" ] && [ -n "$STAGED_WORKER" ]; then
  grep -q "$STAGED_WORKER" "$STAGED/assets/$STAGED_PAGE" && REFS=1 || REFS=0
  grep -q "毫秒" "$STAGED/assets/$STAGED_PAGE" && U5U=1 || U5U=0
  grep -q "内没有完成" "$STAGED/assets/$STAGED_PAGE" && U5C=1 || U5C=0
fi
prechk PRE1 "$([ -n "$NEW_INDEX_SHA" ] && echo 1 || echo 0)" "staged index.html sha=$NEW_INDEX_SHA"
prechk PRE2 "$([ -d "$STAGED/assets" ] && echo 1 || echo 0)" "staged assets 目录存在"
prechk PRE3 "$([ -n "$STAGED_PAGE" ] && echo 1 || echo 0)" "staged 页面 chunk=$STAGED_PAGE（空名禁止 curl /assets/，禁止对目录取 sha）"
prechk PRE4 "$([ -n "$STAGED_WORKER" ] && echo 1 || echo 0)" "staged Worker chunk=$STAGED_WORKER"
prechk PRE5 "$REFS" "staged 页面 chunk 引用 Worker（$STAGED_PAGE）"
prechk PRE6 "$U5U" "staged 页面 chunk 含真实预算单位（毫秒）"
prechk PRE7 "$U5C" "staged 页面 chunk 含超时文案（内没有完成）"
prechk PRE8 "$([ -n "$OLD_INDEX_SHA" ] && echo 1 || echo 0)" "现网 dist index sha=$OLD_INDEX_SHA"
prechk PRE9 "$([ -n "$OLD_INDEX_SHA" ] && [ "$OLD_INDEX_SHA" = "$OLD_SERVED_SHA" ] && echo 1 || echo 0)" "部署前 served==disk（served=$OLD_SERVED_SHA）"
prechk PRE10 "$([ -n "$OLD_PAGE" ] && echo 1 || echo 0)" "现网页面 chunk=$OLD_PAGE"
prechk PRE11 "$([ -n "$OLD_WORKER" ] && echo 1 || echo 0)" "现网 Worker chunk=$OLD_WORKER"
echo "precheck_fails=$(echo $PRECHECK_FAILS | tr -s " " | sed "s/^ *//")" >> "$LOG"
if [ -n "$(echo $PRECHECK_FAILS | tr -d " ")" ]; then
  echo "PRECHECK_FAILURES=$PRECHECK_FAILS" | tee -a "$LOG"
  echo "deploy_rc=2 rollback_attempted=0 rollback_rc=na old_restored=na live_untouched=1 precheck_fails=$PRECHECK_FAILS" | tee "$EV/deploy-result.txt"
  echo "UDL01_DEPLOY_VERDICT=PRECHECK_FAIL deploy_rc=2 live_untouched=1"
  exit 2
fi
echo "precheck_all_pass=1" | tee -a "$LOG"

echo "=== PHASE2 复制到同父目录临时路径 + 逐字节验证 ==="
TMP="$DIST.new-udl01-$TS"
if [ -e "$TMP" ]; then echo "TMP_EXISTS_FAILCLOSED $TMP"; exit 3; fi
cp -a "$STAGED" "$TMP"; CP_RC=$?
COPY_OK=0
if [ "$CP_RC" = 0 ] && [ -f "$TMP/index.html" ]; then
  ( cd "$STAGED" && find . -type f -print0 | sort -z | xargs -0 sha256sum | sort -k2 ) > "$EV/manifest-staged.txt"
  ( cd "$TMP" && find . -type f -print0 | sort -z | xargs -0 sha256sum | sort -k2 ) > "$EV/manifest-copy.txt"
  if diff "$EV/manifest-staged.txt" "$EV/manifest-copy.txt" > "$EV/manifest-copy.diff" 2>&1; then COPY_OK=1; else COPY_OK=0; fi
fi
if [ "$COPY_OK" != 1 ]; then
  bad P2 "复制到临时路径失败或字节不一致（cp_rc=$CP_RC tmp=$TMP）；现网未切换，失败目录保留"
  echo "deploy_rc=3 rollback_attempted=0 rollback_rc=na old_restored=na live_untouched=1" | tee "$EV/deploy-result.txt"
  echo "UDL01_DEPLOY_VERDICT=FAIL deploy_rc=3 live_untouched=1"
  exit 3
fi
ok P2 "临时路径 $TMP 与 staged 逐字节一致（$(grep -c . "$EV/manifest-copy.txt") 个文件）"

echo "=== PHASE3 备份 + 可恢复 rename 序列切换 ==="
BAK="$DIST.bak-udl01-$TS"
PREV="$DIST.prev-udl01-$TS"
FAILED_NEW="$DIST.failed-udl01-$TS"
cp -a "$DIST" "$BAK"; B1=$?
BAK_SHA="$(sha_of "$BAK/index.html")"
BAK_OK=0
if [ "$B1" = 0 ] && [ -f "$BAK/index.html" ] && [ "$BAK_SHA" = "$OLD_INDEX_SHA" ]; then
  ( cd "$DIST" && find . -type f -print0 | sort -z | xargs -0 sha256sum | sort -k2 ) > "$EV/manifest-live.txt"
  ( cd "$BAK" && find . -type f -print0 | sort -z | xargs -0 sha256sum | sort -k2 ) > "$EV/manifest-bak.txt"
  if diff "$EV/manifest-live.txt" "$EV/manifest-bak.txt" > "$EV/manifest-bak.diff" 2>&1; then BAK_OK=1; else BAK_OK=0; fi
fi
if [ "$BAK_OK" = 1 ]; then ok P3 "旧 dist 全量备份逐字节一致 $BAK（$(grep -c . "$EV/manifest-bak.txt") 个文件）"; else bad P3 "备份不完整或字节不一致 rc=$B1 sha=$BAK_SHA（禁止进入任何 mv）"; fi
if [ "$BAK_OK" != 1 ]; then
  echo "post_fails=backup-incomplete" >> "$LOG"
  echo "deploy_rc=6 rollback_attempted=0 rollback_rc=na old_restored=na switch_started=0 live_untouched=1 backup_incomplete=1" | tee "$EV/deploy-result.txt"
  echo "UDL01_DEPLOY_VERDICT=FAIL deploy_rc=6 live_untouched=1 backup_incomplete=1"
  exit 6
fi
mv "$DIST" "$PREV"; B2=$?
if [ "$B2" = 0 ] && [ -d "$PREV" ]; then ok P4 "旧 dist 已移为 $PREV（可回滚冻结件）"; else bad P4 "移动旧 dist 失败 rc=$B2"; fi
SWITCH_OK=0
SWITCH_STARTED=0
if [ "$B2" = 0 ]; then SWITCH_STARTED=1; fi
if [ "$B2" = 0 ]; then
  mv "$TMP" "$DIST"; B3=$?
  if [ "$B3" = 0 ] && [ -f "$DIST/index.html" ] && [ "$(sha_of "$DIST/index.html")" = "$NEW_INDEX_SHA" ]; then SWITCH_OK=1; ok P5 "新 dist 就位（index sha=$NEW_INDEX_SHA）"; else bad P5 "新 dist 就位失败 rc=$B3（将回滚）"; fi
else
  bad P5 "未执行切换（旧 dist 移动失败，现网未动）"
fi

rollback(){
  ROLLBACK_ATTEMPTED=1
  FR=0; RR=1
  if [ -d "$DIST" ] && [ ! -d "$FAILED_NEW" ]; then mv "$DIST" "$FAILED_NEW"; FR=$?; fi
  if [ -d "$PREV" ]; then mv "$PREV" "$DIST"; RR=$?; fi
  bash -c "$RESTART_CMD" > "$EV/rollback-restart.log" 2>&1; RRC=$?
  bash -c "$ACTIVE_CMD" > "$EV/rollback-active.log" 2>&1; RACT=$?
  RB=0; i=0
  for i in $(seq 1 "$READY_TRIES"); do
    code="$(curl $CURL_OPTS -o /dev/null -w "%{http_code}" "$SERVED_URL" 2>/dev/null || true)"
    if [ "$code" = 200 ]; then RB=1; break; fi
    sleep 1
  done
  RSERVED="$(curl $CURL_OPTS "$SERVED_URL" 2>/dev/null | sha256sum | cut -d" " -f1)"
  OLD_RESTORED=0
  if [ "$RSERVED" = "$OLD_SERVED_SHA" ]; then OLD_RESTORED=1; fi
  ROLLBACK_RC=1
  # 只有 move 成功 ∧ 重启 rc=0 ∧ active rc=0 ∧ 有界就绪 ∧ 旧 served SHA 一致，才算回滚成功；
  # 否则旧静态内容可能仍由旧进程提供，必须诚实报告 rollback_rc 非 0（NO-GO）。
  if [ "$FR" = 0 ] && [ "$RR" = 0 ] && [ "$RRC" = 0 ] && [ "$RACT" = 0 ] && [ "$RB" = 1 ] && [ "$OLD_RESTORED" = 1 ]; then ROLLBACK_RC=0; fi
  echo "rollback_move_rc=$FR restore_rc=$RR restart_rc=$RRC active_rc=$RACT ready=$RB served=$RSERVED old_served=$OLD_SERVED_SHA old_restored=$OLD_RESTORED rollback_rc=$ROLLBACK_RC" >> "$LOG"
}

if [ "$SWITCH_OK" != 1 ]; then
  echo "bad_move_rc=$B2" >> "$LOG"
fi

echo "=== PHASE4 重启 + 就绪 + 服务端字节门 ==="
GATES_RC=0
if [ "$SWITCH_OK" = 1 ]; then
  bash -c "$RESTART_CMD" > "$EV/restart.log" 2>&1; SVC=$?
  postchk P6 "$([ "$SVC" = 0 ] && echo 1 || echo 0)" "重启命令 rc=$SVC（$RESTART_CMD）"
  READY=0; i=0
  for i in $(seq 1 "$READY_TRIES"); do
    if curl $CURL_OPTS -o /dev/null -w "%{http_code}" "$SERVED_URL" 2>/dev/null | grep -q 200; then READY=1; break; fi
    sleep 1
  done
  postchk P7 "$READY" "有界就绪（$i 次尝试）"
  bash -c "$ACTIVE_CMD" > /dev/null 2>&1; ACT_RC=$?
  postchk P8 "$([ "$ACT_RC" = 0 ] && echo 1 || echo 0)" "服务 active 检查 rc=$ACT_RC（$ACTIVE_CMD）"
  NEW_SERVED_SHA="$(curl $CURL_OPTS "$SERVED_URL" 2>/dev/null | sha256sum | cut -d" " -f1)"
  postchk P9 "$([ "$NEW_SERVED_SHA" = "$NEW_INDEX_SHA" ] && echo 1 || echo 0)" "served index == 新 dist sha（served=$NEW_SERVED_SHA）"
  NEW_PAGE="$(discover "$DIST/assets" "^DownloadNewPage-.*[.]js$")"
  NEW_WORKER="$(discover "$DIST/assets" "^import-parse[.]worker-.*[.]js$")"
  P_OK=0; W_OK=0; P_BYTES=0; W_BYTES=0; U5U_S=0; U5C_S=0; CSP=0
  if [ -n "$NEW_PAGE" ]; then
    C1="$(curl $CURL_OPTS -o "$EV/served-page-chunk.js" -w "%{http_code}" "$ASSET_BASE/$NEW_PAGE" 2>/dev/null || true)"
    if [ "$C1" = 200 ]; then P_OK=1; fi
    if [ "$(sha_of "$EV/served-page-chunk.js")" = "$(sha_of "$DIST/assets/$NEW_PAGE")" ]; then P_BYTES=1; fi
    grep -q "毫秒" "$EV/served-page-chunk.js" && U5U_S=1 || U5U_S=0
    grep -q "内没有完成" "$EV/served-page-chunk.js" && U5C_S=1 || U5C_S=0
  fi
  if [ -n "$NEW_WORKER" ]; then
    C2="$(curl $CURL_OPTS -o "$EV/served-worker-chunk.js" -w "%{http_code}" "$ASSET_BASE/$NEW_WORKER" 2>/dev/null || true)"
    if [ "$C2" = 200 ]; then W_OK=1; fi
    if [ "$(sha_of "$EV/served-worker-chunk.js")" = "$(sha_of "$DIST/assets/$NEW_WORKER")" ]; then W_BYTES=1; fi
  fi
  CSP="$(curl $CURL_OPTS -D - -o /dev/null "$SERVED_URL" 2>/dev/null | grep -ci "content-security-policy")"
  postchk P10 "$P_OK" "服务端页面 chunk 200（$NEW_PAGE）"
  postchk P11 "$P_BYTES" "服务端页面 chunk 字节与磁盘一致"
  postchk P12 "$W_OK" "服务端 Worker chunk 200（$NEW_WORKER）"
  postchk P13 "$W_BYTES" "服务端 Worker chunk 字节与磁盘一致"
  postchk P14 "$U5U_S" "服务端页面 chunk 含真实预算单位（毫秒）"
  postchk P15 "$U5C_S" "服务端页面 chunk 含超时文案（内没有完成）"
  postchk P16 "$([ "$CSP" -ge 1 ] && echo 1 || echo 0)" "CSP 头在位（$CSP）"
  postchk P17 "$([ -f "$PREV/index.html" ] && [ "$(sha_of "$PREV/index.html")" = "$OLD_INDEX_SHA" ] && echo 1 || echo 0)" "旧 dist 原样保留在 $PREV（可回滚）"
  printf "post_index_sha=%s
post_served_sha=%s
page_chunk=%s
worker_chunk=%s
backup_dir=%s
prev_dir=%s
" "$NEW_INDEX_SHA" "$NEW_SERVED_SHA" "$NEW_PAGE" "$NEW_WORKER" "$BAK" "$PREV" > "$EV/version-binding-post.txt"
fi

echo "post_fails=$(echo $POST_FAILS | tr -s " " | sed "s/^ *//")" >> "$LOG"
DEPLOY_RC=0
if [ "$SWITCH_OK" != 1 ]; then DEPLOY_RC=4; fi
if [ -n "$(echo $POST_FAILS | tr -d " ")" ]; then DEPLOY_RC=5; fi

ROLLBACK_ATTEMPTED=0; ROLLBACK_RC=na; OLD_RESTORED=na; LIVE_UNTOUCHED=0
if [ "$DEPLOY_RC" != 0 ]; then
  if [ "$SWITCH_STARTED" = 1 ]; then
    echo "=== 门禁失败 -> 自动回滚（恢复冻结 prev 并验证旧 served SHA） ===" | tee -a "$LOG"
    rollback
  else
    echo "=== 门禁失败但切换未开始 -> 现网未动，无需回滚 ===" | tee -a "$LOG"
    LIVE_UNTOUCHED=1
  fi
  echo "PASS=$PASS FAIL=$FAIL" | tee -a "$LOG"
  echo "deploy_rc=$DEPLOY_RC rollback_attempted=$ROLLBACK_ATTEMPTED rollback_rc=$ROLLBACK_RC old_restored=$OLD_RESTORED switch_started=$SWITCH_STARTED switch_ok=$SWITCH_OK live_untouched=$LIVE_UNTOUCHED post_fails=$POST_FAILS failed_new_dir=$FAILED_NEW tmp_left=$TMP" | tee "$EV/deploy-result.txt"
  echo "UDL01_DEPLOY_VERDICT=FAIL deploy_rc=$DEPLOY_RC rollback_attempted=$ROLLBACK_ATTEMPTED rollback_rc=$ROLLBACK_RC old_restored=$OLD_RESTORED live_untouched=$LIVE_UNTOUCHED"
  exit "$DEPLOY_RC"
fi
echo "PASS=$PASS FAIL=$FAIL" | tee -a "$LOG"
echo "deploy_rc=0 rollback_attempted=0 rollback_rc=na old_restored=na switch_ok=1 backup_verified=1 backup=$BAK prev=$PREV" | tee "$EV/deploy-result.txt"
echo "UDL01_DEPLOY_VERDICT=PASS deploy_rc=0 switch_ok=1 old_dist_kept=$PREV backup=$BAK"
