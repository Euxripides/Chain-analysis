#!/usr/bin/env bash
# run-r10-export-excelize-v7.sh — R10 v7：唯一、冻结、七类 crosscheck 缺陷已修的服务端 excelize 端到端验收。
#
# v4 第 2 轮（r10-export-excelize-v4-20260913T034232Z）：playwright_rc=0、uc-desktop 8/8 + uc-mobile R10-01 全 expected，
# crosscheck 143 项仅 1 项失败，且该失败是 crosscheck 自身缺陷 D7（矩阵用 "passed" 比 JSON reporter 的 "expected"）。
# 根 11:46 驳回 v5 的"宽松字符串映射"，要求矩阵判据联合校验 status/expectedStatus/result status/errors/duration/evidence，
# 且 skip 只允许明确 7 项。v6 用 r10-crosscheck-v6.py 实现该强化判据。
#
# 根 11:53 对 v6 的实际运行裁定（**v6 整轮 FAIL / 不可验收，原样保留**）：
#   v6 的 runner 在 crosscheck_rc=0 / 144-0 之后，summary 渲染用的 python 抛了 Traceback
#   （KeyError: 'actual'，因为它还在读旧矩阵结构），脚本却继续打印 R10_V6_VERDICT=PASS。
#   这既是**未解释异常**，又是**fail-open 最终判定**，因此该轮不得作为验收证据。
# v7 的更正（逐条对应根 11:53 的要求）：
#   G1 summary 渲染兼容矩阵结构（observed 可能是 dict（v5/v6+）也可能是字符串（v3/v4））；
#   G2 **所有 post-processing 命令一律捕获 rc 并纳入最终 gate**（freeze-writer / report-check / crosscheck / summary-render）；
#   G3 **Traceback 扫描**：整轮控制台日志（runner.console.log，tee 落盘）+ 各阶段输出文件里只要出现
#      "Traceback (most recent call last)" 即 gate 失败——靠机器判定，不靠"看输出"；
#   G4 PASS 只在 playwright rc=0 ∧ report-check rc=0 ∧ crosscheck rc=0 ∧ summary-render rc=0
#      ∧ freeze-writer rc=0 ∧ freeze 稳定 ∧ 无 Traceback **全部满足**时打印；
#   G5 负向自测开关 R10_SELFTEST_BREAK_SUMMARY=1：在 summary 环节**真的**抛异常（KeyError），
#      用来证明 runner 在这种情况下**不会**打印 PASS（先自测，再正式跑）；
#   G6 不使用任何 `|| true` 吞异常；每个命令的 rc 单独留证在 gate.txt。
# 其余冻结/预检/证据语义与 v4/v5/v6 完全一致。
#
# v9（本文件）：R10 v8 失败轮的**测量侧**更正轮 + 审计修复的运行认证轮。
# 裁定：v8 的 5 项失败**不是产品回归**（真值在 v8 轮已由 R10-02/03 的 v2 完成事件证明），
# 但也**绝不因此记 PASS** —— 其中 3 项是我 crosscheck 的排序缺陷（"SELECT seq::text ... ORDER BY seq"
# 被 PostgreSQL 解析为按输出列(text)排序，首尾 10000/9999），2 项是断言写错了对象
# （R10-01 复用了 02:49 的**修复前**导出 f62e4308，其完成事件只在 legacy 表是历史事实）。
# v9 相对 v8 的增量（1420 交接 §5 施工单 1-5；对应 K1-K5）：
#   K1 crosscheck v8：first/last 一律 ORDER BY seq::bigint（数值排序），修掉 3 项假失败；
#   K2 export.completed **作用域感知**（不是削弱）：本轮真实新建 = 硬性要求 + 同一次发生证明
#      （v2 完成行的 request_id/trace_id 必须等于 UI 创建请求与 export.create 行的同一对 id）；
#      复用到的历史导出若已有 v2 行 = 同样按硬性判据核；若没有 v2 行 = 仅当创建于 05:55:08Z
#      审计修复部署**之前**才降级 observation（legacy 有 + 早于修复 + **绝不记 PASS**），否则硬失败；
#   K3 spec v4：R10-01 通过真实 UI 打开 include_meta，使**任务维度**导出不再复用修复前导出；
#      新增 R10-09：结构上不可能复用的**地址维度**全新导出（HTTP 202）认证用例 ——
#      落地 Export/Job/Request/Trace/文件 全部证据，供 crosscheck 硬断言"同一次完成事件"；
#   K4 runner 新增 P0d **静态自检**：testMatch 必须命中 v4 spec、R10-09 必须在场、且
#      "地址维度导出不可能复用"的两条源码依据（复用分支在 if job != nil 内 / job 仅在 job_id 非空时加载）
#      必须在 export_v2.go 里当场成立，否则 exit 4（不给缺失输入发绿）；
#   K5 矩阵扩至 11 项（R10-09 桌面+移动各一项），按设计排除仍为 7 项（uc-mobile 的其余 7 项）。
# 运行身份不变（冻结校验 + P4b 审计链闸门照旧）；旧轮/旧对象一律不改写。
#
# v8（本文件）：R10 v7 的"审计完整性撤回"补课轮。裁定：v7 虽 144/0、9UI PASS，但它**没有证明**
# export.completed 在 v2 不可变审计链；且 v7 绑定的是不含审计修复的 4ec819ad。
# v8 相对 v7 的增量：
#   H1 运行身份改为 484e2707…（= 审计修复 + request 维度真实化，两处均为真实缺陷修复）；
#   H2 crosscheck v7：export.completed 必须是 audit_events_v2 真实行，scope/actor/request/trace/链链接逐项判定，
#      route 必须真实（显式拒绝伪造值 /api/auth/v1/refresh）；
#   H3 新增独立阶段 P4b「审计链闸门」：用**从冻结源码构建**的 auditverify 做密码学校验，
#      要求 ok/valid/无断点/可信锚点 2344/封存尾哈希/链头==库中最大 seq 行 event_hash；rc 纳入最终 gate（fail-closed）。
#
# v1/v2/v3 轮（run-r10-export-excelize*.sh、r10-crosscheck*.py、r10v2-*.spec.ts）全部原样保留，不 hot-edit。
# v3 轮已由根 11:06 判定 INVALIDATED_BY_MIDRUN_DEPLOY（证据目录内 0444 标记）；v4 是"修完 crosscheck 缺陷后"的
# 全新唯一测量。
#
# v3 -> v4 的更正：
#   1) crosscheck v4（audit_events_v2 / 非导出证据分派 / 增量只算真正新建 / mc 独立校验可用 /
#      R10-07 探针与契约 / R10-08 用有 export:create 的跨工作区身份）。
#   2) spec v3（r10v3-*.spec.ts）+ config v3（playwright.r10v3.config.ts）。
#   3) **冻结绑定**：本轮开始/结束各记录一次 api pid/exe/sha 与 worker sha，写 freeze.json；
#      测试运行期间**禁止任何部署/替换 bundle/替换 script**；api sha 必须等于 FREEZE_API_SHA，
#      且运行前后不得变化（mid-run deploy 必须被判为不成立，不能"看不见"）。
#   4) 跨工作区身份改用 P1 轮的追加夹具（只读身份 + 有 export:create 身份），
#      POST 必须带 Idempotency-Key（P1 轮真实 400 缺陷的回归）。
#
# 红线：不 TRUNCATE/reset/clean；不删旧证据/旧二进制/失败轮；只新增；不给缺失输入发绿；
#       测试运行期间不做任何部署。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
if [ "${R10_SELFTEST:-0}" = "1" ]; then
  EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v9-SELFTEST-$TS}"
else
  EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v9-$TS}"
fi
# R10 v9：运行身份与 v8 相同（**不部署、不热改**）：484e2707 = 含 D-R10V4-01 两面修复的冻结产物。
# R10 v8：运行身份 = **含 D-R10V4-01 两面修复**的冻结产物（writeAuditCtx 写 v2 链 + request 维度真实）。
# 前一候选 569f40ec 已被定点实测判为不合格（export.completed 的 route 是伪造的 /api/auth/v1/refresh）。
FREEZE_API_SHA="${FREEZE_API_SHA:-484e2707e6574cce10e41e68047176ff5255aa231d1615c4b10cddd3fe5db2e4}"
# R10 v9：crosscheck v8 = v7 + 数值排序修复(X1) + 完成事件作用域感知(X2) + 同一次发生证明(X2b) + 认证核心(X3)。
R10C="${R10_CONSOLE:-$R/scripts/s7/r10-crosscheck-v8.py}"
# 冻结源码构建的验链器（auditverify）与审计链闸门脚本（v8 新增阶段 P4b）。
AUDITVERIFY_SRC="${R10_AUDITVERIFY:-$R/acceptance-evidence/dual-system-v1/sprint7/r10v8-auditrequest-build-20260913T060129Z/auditverify.build}"
AUDIT_CHAIN_GATE="${R10_AUDIT_CHAIN_GATE:-$R/scripts/s7/r10-audit-chain-gate-v1.sh}"
gate_note(){ printf '%s\n' "$1" >> "$EV/gate.txt"; }
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
# 整轮控制台落盘（tee）：Traceback 扫描的对象就是这个文件，避免"看不见自己输出的异常"。
exec > >(tee -a "$EV/runner.console.log") 2>&1
echo "selftest_mode=${R10_SELFTEST:-0} selftest_break_summary=${R10_SELFTEST_BREAK_SUMMARY:-0}" | tee -a "$EV/run-meta.txt"
MINIO_ENV="/tmp/r10v7-minio-restricted-$TS.env"
cleanup(){ rm -f "$MINIO_ENV"; }
trap cleanup EXIT
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee "$EV/run-meta.txt"
echo "crosscheck_sha256=$(sha256sum "$R10C" | cut -d' ' -f1)" | tee -a "$EV/run-meta.txt"
echo "evidence=$EV" | tee -a "$EV/run-meta.txt"
echo "expected_api_sha=$FREEZE_API_SHA" | tee -a "$EV/run-meta.txt"
echo "started_utc=$TS" | tee -a "$EV/run-meta.txt"

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }
api_pid(){ pgrep -f "$R/backend/bin/api\$" | head -1; }
worker_pid(){ pgrep -f "$R/backend/bin/worker\$" | head -1; }

echo "=== P0a 冻结基线（测试运行期间不得变化） ==="
API_PID_BEFORE="$(api_pid)"; WORKER_PID_BEFORE="$(worker_pid)"
if [ -z "$API_PID_BEFORE" ] || [ -z "$WORKER_PID_BEFORE" ]; then echo "FATAL no running api/worker"; exit 8; fi
API_EXE_BEFORE="$(readlink -f "/proc/$API_PID_BEFORE/exe")"
API_SHA_BEFORE="$(sha256sum "/proc/$API_PID_BEFORE/exe" | cut -d' ' -f1)"
WORKER_SHA_BEFORE="$(sha256sum "/proc/$WORKER_PID_BEFORE/exe" | cut -d' ' -f1)"
echo "api_pid=$API_PID_BEFORE exe=$API_EXE_BEFORE sha=$API_SHA_BEFORE" | tee -a "$EV/run-meta.txt"
echo "worker_pid=$WORKER_PID_BEFORE sha=$WORKER_SHA_BEFORE" | tee -a "$EV/run-meta.txt"
if [ "$API_SHA_BEFORE" != "$FREEZE_API_SHA" ]; then
  echo "FREEZE_MISMATCH run api sha=$API_SHA_BEFORE expected=$FREEZE_API_SHA - refusing to run"
  echo "freeze_mismatch=1" | tee -a "$EV/run-meta.txt"; exit 7
fi
if [ "$API_EXE_BEFORE" != "$R/backend/bin/api" ]; then echo "FREEZE_BAD_EXE $API_EXE_BEFORE"; exit 7; fi

echo "=== P0b readiness（真实 API，不接受裸 200） ==="
READY_OK=0
for i in $(seq 1 30); do
  BODY="$(curl -s -m 5 http://127.0.0.1:8080/health/ready || true)"
  if printf '%s' "$BODY" | python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
except Exception:
    sys.exit(1)
c=d.get("checks") or {}
sys.exit(0 if d.get("status")=="ready" and all(c.get(k)=="ok" for k in ("postgres","clickhouse","nats","minio")) else 1)' ; then READY_OK=1; break; fi
  sleep 1
done
echo "ready_attempts=$i ready_ok=$READY_OK body=$BODY" | tee -a "$EV/run-meta.txt"
if [ "$READY_OK" -ne 1 ]; then echo "READINESS_GATE_FAILED (8080/health/ready JSON 四依赖必须全 ok)"; exit 8; fi

echo "=== P0c preflight（正则白名单 + 作用域 membership，缺项即失败） ==="
PRE_MISSING=0
for f in /home/euripides/.s7jobscope-creds.env /home/euripides/.p1gate-inv-b.env /home/euripides/.p1gate-view-b.env; do
  [ -f "$f" ] || { echo "PREFLIGHT_MISSING file $f"; PRE_MISSING=1; }
done
set -a; . /home/euripides/.s7jobscope-creds.env; set +a
for v in S7JS_INVESTIGATOR_EMAIL S7JS_INVESTIGATOR_PW S7JS_INVESTIGATOR_WS S7JS_ORG_ID; do
  eval "val=\$$v"; if [ -z "${val:-}" ]; then echo "PREFLIGHT_MISSING $v"; PRE_MISSING=1; fi
done
VIEW_B_EMAIL="$(grep -E '^S7JS_INVESTIGATOR_EMAIL=' /home/euripides/.p1gate-inv-b.env | cut -d= -f2-)"
VIEW_B_PW="$(grep -E '^S7JS_INVESTIGATOR_PW=' /home/euripides/.p1gate-inv-b.env | cut -d= -f2-)"
VIEW_B_WS="$(grep -E '^S7JS_INVESTIGATOR_WS=' /home/euripides/.p1gate-inv-b.env | cut -d= -f2-)"
RO_B_EMAIL="$(grep -E '^S7JS_VIEWER_EMAIL=' /home/euripides/.p1gate-view-b.env | cut -d= -f2-)"
RO_B_PW="$(grep -E '^S7JS_VIEWER_PW=' /home/euripides/.p1gate-view-b.env | cut -d= -f2-)"
RO_B_WS="$(grep -E '^S7JS_VIEWER_WS=' /home/euripides/.p1gate-view-b.env | cut -d= -f2-)"
for pair in "view_b_email:$VIEW_B_EMAIL" "view_b_pw:$VIEW_B_PW" "view_b_ws:$VIEW_B_WS" "ro_b_email:$RO_B_EMAIL" "ro_b_pw:$RO_B_PW" "ro_b_ws:$RO_B_WS"; do
  k="${pair%%:*}"; v="${pair#*:}"; if [ -z "$v" ]; then echo "PREFLIGHT_MISSING $k"; PRE_MISSING=1; fi
done
JOB_NO="${R10_JOB_NO:-DL-20260913-0018}"
CHAIN="${R10_CHAIN:-bsc}"
ADDRESS_IN="${R10_ADDRESS:-0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649}"
DATASET="${R10_DATASET:-transactions}"
echo "$CHAIN" | grep -Eq '^[a-z0-9_-]{2,20}$' || { echo "PREFLIGHT_BAD chain=$CHAIN"; PRE_MISSING=1; }
echo "$DATASET" | grep -Eq '^(transactions|token_transfers|nft_transfers|logs|balances|internal_transactions)$' || { echo "PREFLIGHT_BAD dataset=$DATASET"; PRE_MISSING=1; }
echo "$JOB_NO" | grep -Eq '^[A-Za-z0-9._-]{1,64}$' || { echo "PREFLIGHT_BAD job_no=$JOB_NO"; PRE_MISSING=1; }
if ! echo "$ADDRESS_IN" | grep -Eqi '^(0x[0-9a-f]{40}|T[1-9A-HJ-NP-Za-km-z]{33})$'; then echo "PREFLIGHT_BAD address"; PRE_MISSING=1; fi
if [ "$PRE_MISSING" -ne 0 ]; then echo "PREFLIGHT_FAILED - refusing to run (no green from missing/invalid inputs)" | tee -a "$EV/run-meta.txt"; exit 3; fi
if [ "$CHAIN" != "tron" ]; then ADDRESS="$(printf '%s' "$ADDRESS_IN" | tr 'A-Z' 'a-z')"; else ADDRESS="$ADDRESS_IN"; fi
JOB_ID="$(printf "SELECT id FROM download_jobs WHERE job_no='%s';\n" "$JOB_NO" | pgq | tr -d '[:space:]')"
if ! printf '%s' "$JOB_ID" | grep -Eq '^[0-9a-f-]{36}$'; then echo "PREFLIGHT_MISSING job $JOB_NO"; exit 3; fi

read -r JOB_ORG JOB_WS JOB_STATUS JOB_CHAIN JOB_DATASETS <<EOF
$(printf "SELECT organization_id, workspace_id, status, chain, array_to_string(datasets,',') FROM download_jobs WHERE id='%s';\n" "$JOB_ID" | pgq | tr '|' ' ')
EOF
for pair in "org:$JOB_ORG:$S7JS_ORG_ID" "ws:$JOB_WS:$S7JS_INVESTIGATOR_WS"; do
  kind="${pair%%:*}"; rest="${pair#*:}"; have="${rest%%:*}"; want="${rest#*:}"
  if [ "$have" != "$want" ]; then echo "PREFLIGHT_SCOPE_MISMATCH $kind have=$have want=$want"; exit 3; fi
done
if [ "$JOB_STATUS" != "COMPLETED" ]; then echo "PREFLIGHT_JOB_NOT_COMPLETED $JOB_STATUS"; exit 3; fi
case ",$JOB_DATASETS," in *",$DATASET,"*) : ;; *) echo "PREFLIGHT_DATASET_NOT_IN_JOB $DATASET vs $JOB_DATASETS"; exit 3 ;; esac
SUBJECTS="$(printf "SELECT subject FROM download_subjects WHERE job_id='%s';\n" "$JOB_ID" | pgq | tr -d '[:space:]')"
if ! printf '%s\n' "$SUBJECTS" | grep -qx "$ADDRESS"; then echo "PREFLIGHT_ADDRESS_NOT_IN_JOB $ADDRESS"; exit 3; fi
for pair in "viewer_ws:$VIEW_B_WS:$JOB_WS" "readonly_ws:$RO_B_WS:$JOB_WS"; do
  kind="${pair%%:*}"; rest="${pair#*:}"; have="${rest%%:*}"; want="${rest#*:}"
  if [ "$have" = "$want" ]; then echo "PREFLIGHT_CROSS_WS_NOT_DISTINCT $kind=$have"; exit 3; fi
done
for pair in "$VIEW_B_EMAIL:$VIEW_B_WS" "$RO_B_EMAIL:$RO_B_WS"; do
  em="${pair%%:*}"; ws="${pair#*:}"
  N="$(printf "SELECT count(*) FROM identities i JOIN workspace_memberships wm ON wm.identity_id=i.id AND wm.status='active' WHERE i.email='%s' AND wm.workspace_id='%s';\n" "$em" "$ws" | pgq | tr -d '[:space:]')"
  if [ "$N" != "1" ]; then echo "PREFLIGHT_MEMBERSHIP_MISSING $em ws=$ws count=$N"; exit 3; fi
done
# 权限真值来自 member_role_bindings -> role_permissions（不是 identity_permissions，该表不存在——v4 自查更正）
PERM="$(printf "SELECT count(*) FROM identities i JOIN member_role_bindings mrb ON mrb.identity_id=i.id AND mrb.revoked_at IS NULL JOIN role_permissions rp ON rp.role_id=mrb.role_id AND rp.permission_key='export:create' WHERE i.email='%s';\n" "$VIEW_B_EMAIL" | pgq | tr -d '[:space:]')"
if [ "${PERM:-0}" -lt 1 ]; then echo "PREFLIGHT_EXPORT_CREATE_MISSING $VIEW_B_EMAIL ($PERM)"; exit 3; fi
echo "preflight=OK job_no=$JOB_NO job_id=$JOB_ID org=$JOB_ORG ws=$JOB_WS chain=$JOB_CHAIN datasets=$JOB_DATASETS address=$ADDRESS dataset=$DATASET viewer_b_ws=$VIEW_B_WS readonly_b_ws=$RO_B_WS" | tee -a "$EV/run-meta.txt"

cat > "$EV/scope.json" <<EOF
{"job_no":"$JOB_NO","job_id":"$JOB_ID","org_id":"$JOB_ORG","ws_id":"$JOB_WS","chain":"$JOB_CHAIN","address":"$ADDRESS","dataset":"$DATASET","viewer_ws_id":"$VIEW_B_WS","readonly_ws_id":"$RO_B_WS","api_sha":"$FREEZE_API_SHA"}
EOF
chmod 600 "$EV/scope.json"

echo "=== P0d spec/config 静态自检（testMatch 命中 v4 spec；R10-09 在场；"地址维度不可复用"的源码依据成立） ==="
SPEC_PATH="$R/frontend/tests/r10v4-export-excelize.spec.ts"
CFG_PATH="$R/frontend/playwright.r10v4.config.ts"
SPEC_OK=1
[ -f "$SPEC_PATH" ] || { echo "SPEC_SELFCHECK missing spec $SPEC_PATH"; SPEC_OK=0; }
[ -f "$CFG_PATH" ] || { echo "SPEC_SELFCHECK missing config $CFG_PATH"; SPEC_OK=0; }
if [ -f "$CFG_PATH" ]; then
  grep -qF 'testMatch: /r10v4-.*\.spec\.ts/' "$CFG_PATH" || { echo "SPEC_SELFCHECK config testMatch 未命中 r10v4"; SPEC_OK=0; }
fi
if [ -f "$SPEC_PATH" ]; then
  grep -q 'R10-09 全新导出' "$SPEC_PATH" || { echo "SPEC_SELFCHECK spec 缺少 R10-09 用例"; SPEC_OK=0; }
  grep -q 'toBe(202)' "$SPEC_PATH" || { echo "SPEC_SELFCHECK spec 缺少 新建=202/复用=200 断言"; SPEC_OK=0; }
fi
# 静态依据（不是"跑一次看看"）：export_v2.go 的复用分支写在 if job != nil 之内，
# 而 job 只在请求携带 job_id 时加载 => 地址维度导出（不带 job_id）在服务端**结构上**不可能复用。
grep -q 'if req.JobID != "" {' "$R/backend/internal/api/export_v2.go" || { echo "SPEC_SELFCHECK export_v2.go 缺 job_id 守卫"; SPEC_OK=0; }
grep -q 'if job != nil {' "$R/backend/internal/api/export_v2.go" || { echo "SPEC_SELFCHECK export_v2.go 缺复用分支守卫"; SPEC_OK=0; }
echo "spec_sha256=$(sha256sum "$SPEC_PATH" 2>/dev/null | cut -d' ' -f1) config_sha256=$(sha256sum "$CFG_PATH" 2>/dev/null | cut -d' ' -f1) spec_selfcheck=$SPEC_OK" | tee -a "$EV/run-meta.txt"
if [ "$SPEC_OK" -ne 1 ]; then echo "SPEC_SELFCHECK_FAILED - refusing to run (no green from missing/invalid spec)"; exit 4; fi

echo "=== P1 pre-state（PK 快照，供原 PK 子集核验） ==="
set -e
pkcol_at(){ case "$1" in checkpoints) echo shard_id ;; *) echo id ;; esac; }
for t in download_jobs download_shards checkpoints outbox_events exports; do
  printf "SELECT %s::text FROM %s ORDER BY %s::text;\n" "$(pkcol_at "$t")" "$t" "$(pkcol_at "$t")" | pgq > "$EV/pre-ids-$t.txt"
  printf "%s=%s\n" "$t" "$(wc -l < "$EV/pre-ids-$t.txt")" >> "$EV/p1-pre-state.txt"
done
set +e
cat "$EV/p1-pre-state.txt"

echo "=== P1b MinIO 独立校验准备（mc 从容器提取，凭据只经进程环境；MC_CONFIG_DIR 必须存在） ==="
mkdir -p /tmp/r10-mc /tmp/r10-mc-cfg
if [ ! -x /tmp/r10-mc/mc ]; then docker cp deploy-minio-1:/usr/bin/mc /tmp/r10-mc/mc >/dev/null && chmod 755 /tmp/r10-mc/mc; fi
/tmp/r10-mc/mc --version | head -1 | tee "$EV/p1b-minio-tool.txt"
umask 077
grep -E '^(MINIO_ROOT_USER|MINIO_ROOT_PASSWORD|MINIO_ENDPOINT|EXPORT_BUCKET|RAW_BUCKET)=' "$R/deploy/.env" > "$MINIO_ENV"
if ! grep -q '^RAW_BUCKET=' "$MINIO_ENV"; then echo 'RAW_BUCKET=blockchain-raw' >> "$MINIO_ENV"; fi
chmod 600 "$MINIO_ENV"
echo "minio_env_keys=$(cut -d= -f1 "$MINIO_ENV" | sort | tr '\n' ',')" | tee -a "$EV/p1b-minio-tool.txt"

echo "=== P2 real browser run（reporters 只来自 config） ==="
cd "$R/frontend" || exit 90
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export R10_OUT="$EV" R10_EMAIL="$S7JS_INVESTIGATOR_EMAIL" R10_PASSWORD="$S7JS_INVESTIGATOR_PW"
export R10_JOB_ID="$JOB_ID" R10_JOB_NO="$JOB_NO" R10_ADDRESS="$ADDRESS" R10_CHAIN="$CHAIN" R10_DATASET="$DATASET"
export R10_VIEWER_EMAIL="$VIEW_B_EMAIL" R10_VIEWER_PASSWORD="$VIEW_B_PW"
export R10_VIEWONLY_EMAIL="$RO_B_EMAIL" R10_VIEWONLY_PASSWORD="$RO_B_PW"
node node_modules/@playwright/test/cli.js test --config playwright.r10v4.config.ts > "$EV/playwright.console.log" 2>&1
RC=$?
echo "$RC" > "$EV/playwright.rc"
echo "playwright_rc=$RC" | tee -a "$EV/run-meta.txt"
tail -n 60 "$EV/playwright.console.log"

echo "=== P3 冻结复核 + 机器 report 实际校验（每个 post-processing 命令都捕获 rc） ==="
API_PID_AFTER="$(api_pid)"; WORKER_PID_AFTER="$(worker_pid)"
API_EXE_AFTER="$(readlink -f "/proc/$API_PID_AFTER/exe" 2>/dev/null || echo '')"
API_SHA_AFTER="$(sha256sum "/proc/$API_PID_AFTER/exe" 2>/dev/null | cut -d' ' -f1)"
WORKER_SHA_AFTER="$(sha256sum "/proc/$WORKER_PID_AFTER/exe" 2>/dev/null | cut -d' ' -f1)"
FREEZE_RC=0
python3 - "$EV/freeze.json" "$API_PID_BEFORE" "$API_PID_AFTER" "$API_EXE_BEFORE" "$API_EXE_AFTER" \
  "$API_SHA_BEFORE" "$API_SHA_AFTER" "$WORKER_PID_BEFORE" "$WORKER_PID_AFTER" "$WORKER_SHA_BEFORE" "$WORKER_SHA_AFTER" \
  "$FREEZE_API_SHA" > "$EV/p3-freeze.out" 2>&1 <<'PY'
import json, sys
(out, a_pid_b, a_pid_a, a_exe_b, a_exe_a, a_sha_b, a_sha_a,
 w_pid_b, w_pid_a, w_sha_b, w_sha_a, expected) = sys.argv[1:13]
json.dump({
 "api_pid_before": a_pid_b, "api_pid_after": a_pid_a,
 "api_exe_before": a_exe_b, "api_exe_after": a_exe_a,
 "api_sha_before": a_sha_b, "api_sha_after": a_sha_a,
 "worker_pid_before": w_pid_b, "worker_pid_after": w_pid_a,
 "worker_sha_before": w_sha_b, "worker_sha_after": w_sha_a,
 "expected_api_sha": expected,
}, open(out, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
FREEZE_RC=$?
chmod 600 "$EV/freeze.json" 2>/dev/null
FREEZE_STABLE=0
if [ "$API_SHA_AFTER" = "$API_SHA_BEFORE" ] && [ "$API_PID_AFTER" = "$API_PID_BEFORE" ] \
   && [ "$WORKER_SHA_AFTER" = "$WORKER_SHA_BEFORE" ] && [ "$API_SHA_AFTER" = "$FREEZE_API_SHA" ] \
   && [ "$API_EXE_AFTER" = "$R/backend/bin/api" ]; then FREEZE_STABLE=1; fi
{ echo "freeze_writer_rc=$FREEZE_RC"; echo "freeze_stable=$FREEZE_STABLE";
  echo "api_pid_before=$API_PID_BEFORE api_pid_after=$API_PID_AFTER";
  echo "api_sha_before=$API_SHA_BEFORE"; echo "api_sha_after=$API_SHA_AFTER";
  echo "worker_sha_before=$WORKER_SHA_BEFORE"; echo "worker_sha_after=$WORKER_SHA_AFTER"; } >> "$EV/gate.txt"
cat "$EV/p3-freeze.out" | tee -a "$EV/run-meta.txt"

REPORT_RC=0
if [ ! -s "$EV/playwright-report.json" ]; then
  echo "PLAYWRIGHT_REPORT_MISSING_OR_EMPTY $EV/playwright-report.json" | tee "$EV/p3-report-check.txt"; REPORT_RC=1
else
  python3 - "$EV/playwright-report.json" "$EV/p3-report-check.txt" <<'PY'
import json, sys
p, outp = sys.argv[1], sys.argv[2]
rep = json.load(open(p, encoding="utf-8"))
suites = rep.get("suites") or []
stats = rep.get("stats") or {}
lines = ["report_path=%s" % p, "bytes=%d" % len(open(p, "rb").read()), "suites=%d" % len(suites),
         "expected=%s unexpected=%s skipped=%s flaky=%s" % (stats.get("expected"), stats.get("unexpected"), stats.get("skipped"), stats.get("flaky"))]
open(outp, "w", encoding="utf-8").write("\n".join(lines) + "\n")
print("\n".join(lines))
sys.exit(0 if suites else 1)
PY
  REPORT_RC=$?
fi
if [ "$REPORT_RC" -ne 0 ]; then echo "report_check=FAIL" | tee -a "$EV/p3-report-check.txt"; else echo "report_check=OK" | tee -a "$EV/p3-report-check.txt"; fi
echo "report_check_rc=$REPORT_RC" >> "$EV/gate.txt"

echo "=== P4 严格对照（DB / ClickHouse / audit_events_v2 / MinIO / 状态不变量 / 冻结） ==="
R10_MINIO_ENV="$MINIO_ENV" python3 "$R10C" "$EV" > "$EV/r10-summary.json" 2> "$EV/r10-crosscheck.err"
XRC=$?
echo "crosscheck_rc=$XRC" | tee -a "$EV/run-meta.txt"
echo "crosscheck_rc=$XRC" >> "$EV/gate.txt"

echo "=== P4b 审计链独立闸门（v9 沿用：冻结源码构建的 auditverify 做密码学校验 + 链头==库中最大 seq 行 + 可信 2344+ 链） ==="
if [ ! -x "$AUDITVERIFY_SRC" ]; then
  echo "AUDIT_GATE_FAILCLOSED missing frozen auditverify: $AUDITVERIFY_SRC" | tee "$EV/p4b-audit-chain-gate.out"
  ACRC=97
else
  mkdir -p "$EV/audit-chain"
  cp -p "$AUDITVERIFY_SRC" "$EV/audit-chain/auditverify.build"
  echo "auditverify_src=$AUDITVERIFY_SRC sha256=$(sha256sum "$AUDITVERIFY_SRC" | cut -d' ' -f1)" | tee "$EV/p4b-audit-chain-gate.out"
  bash "$AUDIT_CHAIN_GATE" "$EV/audit-chain" "$FREEZE_API_SHA" >> "$EV/p4b-audit-chain-gate.out" 2>&1
  ACRC=$?
fi
tail -n 30 "$EV/p4b-audit-chain-gate.out"
echo "audit_chain_gate_rc=$ACRC" >> "$EV/gate.txt"

echo "=== P5 summary 渲染（结构兼容；rc 纳入 gate；负向自测开关 R10_SELFTEST_BREAK_SUMMARY=1） ==="
if [ "${R10_SELFTEST_BREAK_SUMMARY:-0}" = "1" ]; then
  # 负向自测：故意访问不存在的键 -> 真实抛 KeyError（不 try/except、不 || true）
  python3 - "$EV/r10-summary.json" > "$EV/p5-summary-render.out" 2>&1 <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
print("SELFTEST deliberate summary failure:", data["__selftest_missing_key__"])
PY
  SUMMARY_RC=$?
else
  python3 - "$EV/r10-summary.json" > "$EV/p5-summary-render.out" 2>&1 <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
print("verdict=%s checks_total=%s checks_failed=%s" % (data.get("verdict"), data.get("checks_total"), data.get("checks_failed")))
for row in (data.get("failed") or [])[:40]:
    print("FAILED: %s -> %s" % (row.get("check"), json.dumps(row.get("detail"), ensure_ascii=False)[:300]))
for proj in ("uc-desktop", "uc-mobile"):
    parts = []
    for row in (data.get("matrix") or []):
        if not isinstance(row, dict) or row.get("project") != proj:
            continue
        observed = row.get("observed")
        if isinstance(observed, dict):
            label = observed.get("status")
        elif isinstance(observed, str):
            label = observed
        else:
            label = None
        if label is None:
            label = row.get("actual") or row.get("expected")
        parts.append("%s=%s" % (row.get("test"), label))
    if parts:
        print("%s: %s" % (proj, ", ".join(parts)))
for obs in (data.get("observations") or []):
    name = str(obs.get("observation", ""))
    if "bucket" in name or "delta" in name:
        print("OBS: %s -> %s" % (name, json.dumps(obs.get("detail"), ensure_ascii=False)[:300]))
PY
  SUMMARY_RC=$?
fi
cat "$EV/p5-summary-render.out"
echo "summary_render_rc=$SUMMARY_RC" >> "$EV/gate.txt"
if [ -s "$EV/r10-crosscheck.err" ]; then echo "--- crosscheck stderr ---"; head -20 "$EV/r10-crosscheck.err"; fi

echo "=== P6 最终 gate（fail-closed：任一 rc 非 0 或出现 Traceback 即 FAIL） ==="
sleep 1
TB_FOUND=0
for f in "$EV/runner.console.log" "$EV/playwright.console.log" "$EV/r10-crosscheck.err" \
         "$EV/p3-report-check.txt" "$EV/p5-summary-render.out" "$EV/p3-freeze.out"; do
  [ -f "$f" ] || continue
  if grep -q "Traceback (most recent call last)" "$f"; then TB_FOUND=1; echo "traceback_found_in=$f"; fi
done
echo "traceback_found=$TB_FOUND" >> "$EV/gate.txt"

GATE_FAILS=""
gate_chk(){ if [ "$2" -ne 0 ]; then GATE_FAILS="$GATE_FAILS $1(rc=$2)"; fi; }
gate_chk playwright "$RC"
gate_chk report-check "$REPORT_RC"
gate_chk crosscheck "$XRC"
gate_chk summary-render "$SUMMARY_RC"
gate_chk freeze-writer "$FREEZE_RC"
gate_chk audit-chain-gate "$ACRC"
[ "$FREEZE_STABLE" -eq 1 ] || GATE_FAILS="$GATE_FAILS freeze-unstable"
[ "$TB_FOUND" -eq 0 ] || GATE_FAILS="$GATE_FAILS traceback-found"
echo "gate_failures=$(echo $GATE_FAILS | tr -s ' ' | sed 's/^ *//')" >> "$EV/gate.txt"
cat "$EV/gate.txt" | tee -a "$EV/run-meta.txt"

if [ -n "$GATE_FAILS" ]; then
  echo "R10_V9_VERDICT=FAIL gate_failures=$GATE_FAILS" | tee -a "$EV/run-meta.txt"
  exit 1
fi
echo "R10_V9_VERDICT=PASS playwright_rc=$RC report_rc=$REPORT_RC crosscheck_rc=$XRC summary_render_rc=$SUMMARY_RC freeze_writer_rc=$FREEZE_RC audit_chain_gate_rc=$ACRC spec_selfcheck=$SPEC_OK freeze_stable=$FREEZE_STABLE traceback=$TB_FOUND" | tee -a "$EV/run-meta.txt"
exit 0
