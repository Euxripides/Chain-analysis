#!/usr/bin/env bash
# S4-QA-01：USER-001、USER-004..010、AUDIT-001 垂直闭环验收。
#
# 运行位置：Ubuntu VM（可由本仓 scripts 同步后执行）。
# 前置：
#   - 已部署 Sprint 4 版本（含 0047 迁移与两套前端）；
#   - 夹具账号已由 cmd/admin-fixture 重置（密码通过环境变量传入）；
#   - ADMIN_PASSWORD 为平台 Owner 的密码（用于审计链/权限负向 API 验证）。
#   - CLICKHOUSE_PASSWORD 从 deploy/.env 读取（验证 Evidence source_refs 在 ClickHouse 真实存在）。
#
# 设计原则（§27.7 / §32 约束 10）：
#   - 每一条 PASS 都有 API / DB / 文件 / 运行时证据；
#   - 不能观察的项写 NOT_VERIFIED，不用 HTTP 200 冒充业务通过；
#   - 本脚本只创建带 S4 前缀的测试对象，不清理他人数据（保留夹具到 S7）。

set -uo pipefail

BASE=${BASE:-http://127.0.0.1:8080}
INV_EMAIL=${INV_EMAIL:-fixture-investigator@fixture.local}
REV_EMAIL=${REV_EMAIL:-fixture-reviewer@fixture.local}
VIEWER_EMAIL=${VIEWER_EMAIL:-fixture-viewer@fixture.local}
INV_PW=${INVESTIGATOR_PASSWORD:?需要 INVESTIGATOR_PASSWORD}
REV_PW=${REVIEWER_PASSWORD:?需要 REVIEWER_PASSWORD}
VIEWER_PW=${VIEWER_PASSWORD:?需要 VIEWER_PASSWORD}
ADMIN_PW=${ADMIN_PASSWORD:?需要 ADMIN_PASSWORD（Owner，用于审计链与后台负向）}
CH_PW=${CLICKHOUSE_PASSWORD:-}
PROJECT=${PROJECT:-/home/euripides/projects/blockchain-platform}
EVIDENCE_DIR=${EVIDENCE_DIR:-$PROJECT/acceptance-evidence/dual-system-v1/tests/sprint4}
PG="docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc"
CH="curl -sS http://127.0.0.1:8123/?user=blockchain&password=${CH_PW}&database=blockchain"
mkdir -p "$EVIDENCE_DIR"
: > "$EVIDENCE_DIR/run.log"

PASS=0; FAIL=0; NOT_VERIFIED=0
J1=$(mktemp); J2=$(mktemp); J3=$(mktemp); JA=$(mktemp)
trap 'rm -f "$J1" "$J2" "$J3" "$JA"' EXIT

check() { # check <ID> <描述> <实际> <期望>
  if [ "$3" = "$4" ]; then printf '  PASS         %-10s %-52s %s\n' "$1" "$2" "$3" >>"$EVIDENCE_DIR/run.log"; printf '  PASS %-10s %s\n' "$1" "$2"; PASS=$((PASS+1));
  else printf '  FAIL         %-10s %-52s got=%s want=%s\n' "$1" "$2" "$3" "$4" >>"$EVIDENCE_DIR/run.log"; printf '  FAIL %-10s %s got=%s want=%s\n' "$1" "$2" "$3" "$4"; FAIL=$((FAIL+1)); fi
}
nv() { printf '  NOT_VERIFIED %-10s %s\n' "$1" "$2" | tee -a "$EVIDENCE_DIR/run.log"; NOT_VERIFIED=$((NOT_VERIFIED+1)); }
code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
csrf() { grep "$1" "$2" 2>/dev/null | awk '{print $7}'; }
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1"; }

# 多工作区身份登录必须显式选择工作区（P1-20260913）：服务端不替用户猜作用域。
# 夹具账号同时属于 acme-lab 与 acme-lab-other，不带 workspace_id 会得到
# AUTHN_WORKSPACE_SELECTION_REQUIRED。这里按邮箱解析目标工作区并随登录一并提交。
if [ -z "${FIXTURE_WORKSPACE_ID:-}" ] && [ -r /tmp/dual-system-fixture.env ]; then
  set -a; . /tmp/dual-system-fixture.env; set +a
fi
login_workspace() {
  case "$1" in
    *@fixture.local) printf '%s' "${FIXTURE_WORKSPACE_ID:-}" ;;
    *) printf '' ;;
  esac
}
login() { # login <jar> <email> <password> <audience>
  local jar=$1 email=$2 pass=$3 aud=${4:-user}
  local ws; ws=$(login_workspace "$email")
  local body="{\"identifier\":\"$email\",\"password\":\"$pass\",\"audience\":\"$aud\""
  [ -n "$ws" ] && body="$body,\"workspace_id\":\"$ws\""
  body="$body}"
  curl -s -c "$jar" -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "$body" "$BASE/api/auth/v1/login"
}

exec > >(tee -a "$EVIDENCE_DIR/run.log") 2>&1
echo "==== Sprint 4 E2E 开始 $(date -Is) build=$(git -C "$PROJECT" rev-parse --short HEAD 2>/dev/null || echo no-git) ===="

echo "== SETUP：登录 investigator / reviewer / viewer / owner =="
LOGIN_INV=$(login "$J1" "$INV_EMAIL" "$INV_PW"); check SETUP "investigator 用户层登录" "$(echo "$LOGIN_INV" | jget 'd["success"]')" "True"
LOGIN_REV=$(login "$J2" "$REV_EMAIL" "$REV_PW"); check SETUP "reviewer 用户层登录" "$(echo "$LOGIN_REV" | jget 'd["success"]')" "True"
LOGIN_VIEW=$(login "$J3" "$VIEWER_EMAIL" "$VIEWER_PW"); check SETUP "viewer 用户层登录" "$(echo "$LOGIN_VIEW" | jget 'd["success"]')" "True"
LOGIN_ADM=$(login "$JA" admin@local "$ADMIN_PW" admin); check SETUP "Owner 后台登录" "$(echo "$LOGIN_ADM" | jget 'd["success"]')" "True"
CSRF1=$(csrf cs_user_session_csrf "$J1"); CSRF2=$(csrf cs_user_session_csrf "$J2"); CSRF3=$(csrf cs_user_session_csrf "$J3"); CSRFA=$(csrf cs_admin_session_csrf "$JA")
INV_ID=$(echo "$LOGIN_INV" | jget 'd["data"]["user"]["id"]'); REV_ID=$(echo "$LOGIN_REV" | jget 'd["data"]["user"]["id"]')
WORKSPACE_ID=$(echo "$LOGIN_INV" | jget 'd["data"].get("workspace_id","")')
echo "  investigator=$INV_ID reviewer=$REV_ID workspace=$WORKSPACE_ID" | tee -a "$EVIDENCE_DIR/run.log"

echo
echo "== USER-001 新建案件：页面/API/DB/审计一致 =="
CASE_NAME="S4-E2E-$(date +%Y%m%d-%H%M%S)"
CASE_JSON=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"name\":\"$CASE_NAME\",\"description\":\"S4 验收真实案件\",\"classification\":\"confidential\",\"priority\":\"high\",\"status\":\"active\",\"member_ids\":[\"$REV_ID\"]}" \
  "$BASE/api/v2/cases")
CID=$(echo "$CASE_JSON" | jget 'd["data"]["id"]')
check USER-001 "POST /api/v2/cases 创建成功" "$(echo "$CASE_JSON" | jget 'd["success"]')" "True"
check USER-001 "案件包含案件编号" "$([ -n "$(echo "$CASE_JSON" | jget 'd["data"]["case_no"]')" ] && echo yes || echo no)" "yes"
check USER-001 "DB cases 行与 API 一致（name/status/classification）" \
  "$($PG "select name||'|'||status||'|'||classification from cases where id='$CID'")" "$CASE_NAME|active|confidential"
check USER-001 "case_members 自动包含创建人 lead 与指定 reviewer" \
  "$($PG "select count(*) from case_members where case_id='$CID' and role in ('lead','investigator','reviewer')")" "2"
check USER-001 "case.create 已写入 audit_events_v2" \
  "$($PG "select count(*) from audit_events_v2 where action='case.create' and resource_id='$CID'")" "1"
ACTIVITY_CREATE=$($PG "select count(*) from case_activity where case_id='$CID' and event_type='case.created'")
check USER-001 "business activity 已写入 case_activity" "$ACTIVITY_CREATE" "1"
check USER-001 "案件出现在列表 API 中" \
  "$(curl -s -b "$J1" "$BASE/api/v2/cases?q=$CASE_NAME" | jget 'any(i["id"]=="'"$CID"'" for i in d["data"])')" "True"

echo
echo "== revision 冲突：PATCH stale -> 412，不覆盖数据 =="
CASE_REV=$(echo "$CASE_JSON" | jget 'd["data"]["revision"]')
STALE_HTTP=$(code -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H 'If-Match: "99"' -X PATCH -d '{"name":"SHOULD-NOT-WIN"}' "$BASE/api/v2/cases/$CID")
check USER-007 "PATCH 案件 stale revision -> 412" "$STALE_HTTP" "412"
check USER-007 "stale 请求未覆盖名称" "$($PG "select name from cases where id='$CID'")" "$CASE_NAME"

echo
echo "== 案件成员/对象/动态/评论 =="
check FIXTURE "添加案件对象（TRON Address 规范化）" \
  "$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
      -d '{"subject_type":"ADDRESS","chain":"tron","value":"TAkT2zbS81TbsNBLRvqv5fyAq4vCUWBk9m","label":"core"}' \
      "$BASE/api/v2/cases/$CID/subjects" | jget 'd["data"]["normalized_value"]')" "TAkT2zbS81TbsNBLRvqv5fyAq4vCUWBk9m"
COMMENT_JSON=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d '{"body_markdown":"<img src=x onerror=window.__xss=1> 这条评论必须被净化为纯文本"}' "$BASE/api/v2/cases/$CID/comments")
check SEC-002 "评论中的 HTML 标签被服务端移除" "$(echo "$COMMENT_JSON" | jget '"<img" not in d["data"]["body_markdown"] and "onerror" not in d["data"]["body_markdown"]')" "True"
check USER-001 "案件动态 API 返回事件" "$(curl -s -b "$J1" "$BASE/api/v2/cases/$CID/activity" | jget 'len(d["data"])>=3')" "True"
check USER-010 "workspace-members 可选成员" "$(curl -s -b "$J1" "$BASE/api/v2/workspace-members?q=fixture" | jget 'len(d["data"])>=3')" "True"

echo
echo "== USER-004 调查暂停/恢复：Checkpoint 保留、不重复结果 =="
IDEM="s4-e2e-$CID"
INV_PAYLOAD="{\"case_id\":\"$CID\",\"goal\":\"S4 E2E investigation\",\"mode\":\"assisted\",\"subjects\":[{\"type\":\"ADDRESS\",\"chain\":\"tron\",\"value\":\"TAkT2zbS81TbsNBLRvqv5fyAq4vCUWBk9m\"}],\"expected_outputs\":[\"FINDINGS\",\"EVIDENCE_INDEX\"],\"data_requirement\":{\"datasets\":[\"token_transfers\"],\"range\":{\"type\":\"block\",\"from_block\":78238692,\"to_block\":83304697},\"reuse_policy\":\"prefer_local\"},\"budget\":{\"max_provider_cost_usd\":\"0.00\"},\"capability_versions\":{\"analysis.flow\":\"1.0\"}}"
PREVIEW=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"case_id\":\"$CID\",\"subjects\":[{\"type\":\"ADDRESS\",\"chain\":\"tron\",\"value\":\"TAkT2zbS81TbsNBLRvqv5fyAq4vCUWBk9m\"}],\"data_requirement\":{\"datasets\":[\"token_transfers\"],\"range\":{\"type\":\"block\",\"from_block\":78238692,\"to_block\":83304697},\"reuse_policy\":\"prefer_local\"}}" \
  "$BASE/api/v2/investigations/preview")
check USER-004 "Coverage 预览命中本地索引（0 Provider 成本路径）" "$(echo "$PREVIEW" | jget 'd["data"]["coverage"]["overall_ratio"]')" "1"
INV_JSON=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H "Idempotency-Key: $IDEM" -H 'X-Auth-Audience: user' -d "$INV_PAYLOAD" "$BASE/api/v2/investigations")
IID=$(echo "$INV_JSON" | jget 'd["data"]["investigation"]["id"]')
REPLAY=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H "Idempotency-Key: $IDEM" -H 'X-Auth-Audience: user' -d "$INV_PAYLOAD" "$BASE/api/v2/investigations" | jget 'd["data"]["idempotent_replay"]')
check USER-004 "创建调查幂等重放返回既有对象" "$REPLAY" "True"
START=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H 'If-Match: "1"' -H 'X-Auth-Audience: user' -X POST -d '{}' "$BASE/api/v2/investigations/$IID/commands/start")
check USER-004 "start 命令 -> running" "$(echo "$START" | jget 'd["data"]["status"]')" "running"
sleep 0.3
PAUSE=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H 'If-Match: "2"' -H 'X-Auth-Audience: user' -X POST -d '{"reason":"USER-004 pause"}' "$BASE/api/v2/investigations/$IID/commands/pause")
check USER-004 "pause 命令 -> paused" "$(echo "$PAUSE" | jget 'd["data"]["status"]')" "paused"
sleep 2
check USER-004 "暂停期间后台未偷偷推进（仍为 paused）" "$(curl -s -b "$J1" "$BASE/api/v2/investigations/$IID" | jget 'd["data"]["status"]')" "paused"
RESUME=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H 'If-Match: "3"' -H 'X-Auth-Audience: user' -X POST -d '{"reason":"USER-004 resume"}' "$BASE/api/v2/investigations/$IID/commands/resume")
check USER-004 "resume 命令 -> running" "$(echo "$RESUME" | jget 'd["data"]["status"]')" "running"
STATUS=""; FINDING_COUNT=0; EVIDENCE_COUNT=0
for i in $(seq 1 90); do
  DET=$(curl -s -b "$J1" "$BASE/api/v2/investigations/$IID")
  STATUS=$(echo "$DET" | jget 'd["data"]["status"]'); FINDING_COUNT=$(echo "$DET" | jget 'd["data"]["finding_count"]'); EVIDENCE_COUNT=$(echo "$DET" | jget 'd["data"]["evidence_count"]')
  case "$STATUS" in waiting_review|failed|cancelled|completed) break;; esac
  sleep 2
done
check USER-004 "后台分析收敛到 waiting_review" "$STATUS" "waiting_review"
check USER-004 "本地索引复用产生 Finding/Evidence（不触达 Provider）" "$([ "$FINDING_COUNT" -gt 0 ] && [ "$EVIDENCE_COUNT" -gt 0 ] && echo yes || echo no)" "yes"
RUN_COUNT=$($PG "select count(*) from investigation_runs where investigation_id='$IID'")
check USER-004 "暂停时未创建野 run；恢复后 run 数受控（<=1）" "$([ "${RUN_COUNT:-0}" -le 1 ] && echo yes || echo no)" "yes"
CHECKPOINT=$($PG "select coalesce(checkpoint->>'data_reused','') from investigation_runs where investigation_id='$IID' order by created_at limit 1")
check USER-004 "运行 Checkpoint 仍保留（data_reused=true）" "$CHECKPOINT" "true"

echo
echo "== USER-005 Finding 复核 + USER-006 Evidence 版本替代 =="
FINDINGS=$(curl -s -b "$J1" "$BASE/api/v2/investigations/$IID/findings?limit=200")
FID=$(echo "$FINDINGS" | jget 'next((f["id"] for f in d["data"] if f["status"]=="candidate" and len(f["evidence"])>0), "")')
check USER-005 "存在带 Evidence 引用的 candidate Finding" "$([ -n "$FID" ] && echo yes || echo no)" "yes"
EID=$(echo "$FINDINGS" | jget 'next(f["evidence"][0]["id"] for f in d["data"] if f["id"]=="'"$FID"'" and len(f["evidence"])>0)')
FREV=$(echo "$FINDINGS" | jget 'next(f["revision"] for f in d["data"] if f["id"]=="'"$FID"'")')
check USER-005 "Finding 引用了可复核 Evidence" "$([ -n "$EID" ] && echo yes || echo no)" "yes"
# 负向：viewer 不能复核 Finding
check USER-005 "viewer 确认 Finding -> 403" "$(code -b "$J3" -H "X-CSRF-Token: $CSRF3" -H 'Content-Type: application/json' -X POST -d '{"note":"x"}' "$BASE/api/v2/findings/$FID/commands/confirm")" "403"
# reviewer verify evidence
V=$(curl -s -b "$J2" -H "X-CSRF-Token: $CSRF2" -H 'Content-Type: application/json' -X POST -d '{"note":"USER-005 verify"}' "$BASE/api/v2/evidence/$EID/commands/verify")
check USER-005 "reviewer verify Evidence" "$(echo "$V" | jget 'd["data"]["status"]')" "verified"
# reviewer confirm finding
C=$(curl -s -b "$J2" -H "X-CSRF-Token: $CSRF2" -H 'Content-Type: application/json' -H "If-Match: \"$FREV\"" -X POST -d '{"note":"USER-005 confirm"}' "$BASE/api/v2/findings/$FID/commands/confirm")
check USER-005 "reviewer confirm Finding -> confirmed" "$(echo "$C" | jget 'd["data"]["status"]')" "confirmed"
check USER-005 "confirmed Finding 至少一条 verified Evidence" "$($PG "select count(*) from finding_evidence_links l join investigation_evidence e on e.id=l.evidence_id where l.finding_id='$FID' and e.status='verified'")" "1"
check USER-005 "finding:review 已写入审计" "$($PG "select count(*) from audit_events_v2 where action='finding:review' and resource_id='$FID'")" "1"
# evidence supersede
OLD_HASH=$(curl -s -b "$J1" "$BASE/api/v2/evidence/$EID" | jget 'd["data"]["content_hash"]')
SUP=$(curl -s -b "$J2" -H "X-CSRF-Token: $CSRF2" -H 'Content-Type: application/json' -X POST -d '{"note":"USER-006 new version","title":"corrected transaction bundle"}' "$BASE/api/v2/evidence/$EID/commands/supersede")
NEW_EID=$(echo "$SUP" | jget 'd["data"]["id"]')
check USER-006 "新 Evidence 版本创建成功且 status=collected" "$(echo "$SUP" | jget 'd["data"]["status"]')" "collected"
check USER-006 "新版本 supersedes_id 指向原版本" "$(echo "$SUP" | jget 'd["data"]["supersedes_id"]')" "$EID"
OLD=$(curl -s -b "$J1" "$BASE/api/v2/evidence/$EID")
check USER-006 "原 Evidence 状态变为 superseded 但内容 hash 不变" "$(echo "$OLD" | jget 'd["data"]["status"]+"|"+d["data"]["content_hash"]')" "superseded|$OLD_HASH"
check USER-006 "新版本表记录 supersedes 链" "$($PG "select count(*) from investigation_evidence where id='$NEW_EID' and supersedes_id='$EID' and version=2")" "1"

echo
echo "== USER-008 报告审批/签署/下载票据（Investigator -> Reviewer -> approved report） =="
REPORT=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -d "{\"investigation_id\":\"$IID\"}" "$BASE/api/v2/cases/$CID/reports")
RID=$(echo "$REPORT" | jget 'd["data"]["id"]')
check USER-008 "Investigator 生成报告 revision" "$(echo "$REPORT" | jget 'str(d["success"])')" "True"
REVNO=$(echo "$REPORT" | jget 'd["data"]["revision"]'); RHASH=$(echo "$REPORT" | jget 'd["data"]["artifact_sha256"]'); RSTATUS=$(echo "$REPORT" | jget 'd["data"]["status"]')
check USER-008 "新报告 status=generated 且绑定 content_hash" "$RSTATUS|$([ "${#RHASH}" = "64" ] && echo sha256_ok || echo bad)" "generated|sha256_ok"
# submit
SR=$(echo "$REPORT" | jget 'd["data"]["status_revision"]')
SUB=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H "If-Match: \"$SR\"" -X POST -d "{\"reviewer_id\":\"$REV_ID\",\"note\":\"USER-008 submit\"}" "$BASE/api/v2/reports/$RID/commands/submit")
check USER-008 "Investigator 提交复核 -> in_review" "$(echo "$SUB" | jget 'd["data"]["status"]')" "in_review"
# stale status_revision conflict
check USER-008 "stale status_revision 批准 -> 412" "$(code -b "$J2" -H "X-CSRF-Token: $CSRF2" -H 'Content-Type: application/json' -H 'If-Match: "999"' -X POST -d '{"note":"stale"}' "$BASE/api/v2/reports/$RID/commands/approve")" "412"
# separation of duty: submitter cannot approve
SR=$(echo "$SUB" | jget 'd["data"]["status_revision"]')
check USER-008 "提交人不能批准自己的报告 -> 403" "$(code -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -H "If-Match: \"$SR\"" -X POST -d '{"note":"self approve"}' "$BASE/api/v2/reports/$RID/commands/approve")" "403"
# reviewer approve
APP=$(curl -s -b "$J2" -H "X-CSRF-Token: $CSRF2" -H 'Content-Type: application/json' -H "If-Match: \"$SR\"" -X POST -d '{"note":"USER-008 approve"}' "$BASE/api/v2/reports/$RID/commands/approve")
check USER-008 "Reviewer 批准 -> approved" "$(echo "$APP" | jget 'd["data"]["status"]')" "approved"
check USER-008 "report_approvals 绑定 revision + content_hash" "$($PG "select count(*) from report_approvals where report_id='$RID' and report_revision='$REVNO' and content_hash='$RHASH' and decision='approve'")" "1"
SR=$(echo "$APP" | jget 'd["data"]["status_revision"]')
SIGN=$(curl -s -b "$J2" -H "X-CSRF-Token: $CSRF2" -H 'Content-Type: application/json' -H "If-Match: \"$SR\"" -X POST -d '{}' "$BASE/api/v2/reports/$RID/commands/sign")
check USER-008 "Reviewer 签署 -> signed" "$(echo "$SIGN" | jget 'd["data"]["status"]')" "signed"
check USER-008 "签署产生不可空 signature_hash" "$([ -n "$(echo "$SIGN" | jget 'd["data"]["signature_hash"]')" ] && echo yes || echo no)" "yes"
# download ticket
TICKET=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -d '{}' "$BASE/api/v2/reports/$RID/download-ticket")
TURL=$(echo "$TICKET" | jget 'd["data"]["url"]'); THASH=$(echo "$TICKET" | jget 'd["data"]["content_hash"]')
check USER-008 "60 秒一次性下载票据签发" "$(echo "$TICKET" | jget 'str(d["data"]["expires_in_seconds"])')" "60"
check USER-008 "票据返回 SHA-256 与审批绑定 hash 一致" "$THASH" "$RHASH"
D1HTTP=$(curl -s -b "$J1" -o "$EVIDENCE_DIR/$RID-report.md" -w '%{http_code}' "$BASE$TURL"); D1HASH=$(sha256sum "$EVIDENCE_DIR/$RID-report.md" | awk '{print $1}')
check USER-008 "GET 票据下载 200" "$D1HTTP" "200"
check USER-008 "下载文件 SHA-256 与 DB/API 一致" "$D1HASH" "$RHASH"
D2HTTP=$(code -b "$J1" "$BASE$TURL")
check USER-008 "票据重复使用 -> 410" "$D2HTTP" "410"
# artifact revision immutability + re-generate creates new revision
REGEN=$(curl -s -b "$J1" -H "X-CSRF-Token: $CSRF1" -H 'Content-Type: application/json' -d "{\"investigation_id\":\"$IID\"}" "$BASE/api/v2/cases/$CID/reports")
REVNO2=$(echo "$REGEN" | jget 'd["data"]["revision"]')
check USER-008 "重新生成产生新 revision，不覆盖已批准 artifact" "$([ "${REVNO2:-0}" -gt "${REVNO:-0}" ] && echo yes || echo no)" "yes"
check USER-008 "原报告仍可读取且 status=signed" "$(curl -s -b "$J1" "$BASE/api/v2/reports/$RID" | jget 'd["data"]["status"]+"|"+str(d["data"]["revision"])')" "signed|$REVNO"

echo
echo "== USER-009 全局搜索：只返回有权对象 + 类型分组 =="
SEARCH=$(curl -s -b "$J1" "$BASE/api/v2/search?q=$CASE_NAME")
check USER-009 "搜索命中本人案件" "$(echo "$SEARCH" | jget 'any(i["type"]=="case" and i["id"]=="'"$CID"'" for i in d["data"]["items"])')" "True"
# S5 起 /search 扩展了 job/report/address/transaction 类型；S4 期望 case+investigation 必须存在。
check USER-009 "搜索结果声明类型分组" "$(echo "$SEARCH" | jget '"case" in d["data"]["scopes"] and "investigation" in d["data"]["scopes"]')" "True"
CROSS=$(curl -s -b "$J1" "$BASE/api/v2/search?q=FIXTURE-PRIMARY")
check USER-009 "跨工作区案件不会出现在搜索结果" "$(echo "$CROSS" | jget 'any(i["id"]=="ae8326ed-a6dc-414c-821f-b9a2973fc258" for i in d["data"]["items"])')" "False"

echo
echo "== USER-010 通知跳转与安全失败 =="
NOTIFS=$(curl -s -b "$J2" "$BASE/api/v2/notifications?limit=50")
NID=$(echo "$NOTIFS" | jget 'next((n["id"] for n in d["data"] if n["object_type"]=="report"), "")')
check USER-010 "reviewer 收到报告复核通知" "$([ -n "$NID" ] && echo yes || echo no)" "yes"
check USER-010 "通知 open 返回可访问目标" "$(curl -s -b "$J2" -H "X-CSRF-Token: $CSRF2" -H 'X-Auth-Audience: user' -X POST "$BASE/api/v2/notifications/$NID/open" | jget 'd["data"]["target"]')" "/reports/$RID"
check USER-010 "他人打开该通知 -> 404（安全失败）" "$(code -b "$J3" -H "X-CSRF-Token: $CSRF3" -H 'X-Auth-Audience: user' -X POST "$BASE/api/v2/notifications/$NID/open")" "404"
INBOX=$(curl -s -b "$J2" "$BASE/api/v2/inbox?status=approved")
check USER-010 "reviewer Inbox 包含已处理的 report 复核项" "$(echo "$INBOX" | jget 'any(i["resource_type"]=="report" and i["resource_id"]=="'"$RID"'" for i in d["data"])')" "True"

echo
echo "== 数据层：PostgreSQL / ClickHouse / MinIO / 运行日志 =="
EVIDENCE_SOURCES=$($PG "select source_refs->>0 from investigation_evidence where id='$NEW_EID' or id='$EID' order by version desc limit 1")
TX=${EVIDENCE_SOURCES//\"/}
if [ -n "$CH_PW" ] && [ -n "$TX" ]; then
  CH_COUNT=$($CH --data-binary "select count() from blockchain.token_transfers where tx_hash='$TX' FORMAT TSV")
  check DATA "Evidence source_refs 的 tx_hash 在 ClickHouse 真实存在" "$([ "${CH_COUNT:-0}" -gt 0 ] && echo yes || echo no)" "yes"
else
  nv DATA "ClickHouse 凭据或 source_ref 缺失，未执行链上存在性核对"
fi
OBJECT_KEY=$($PG "select object_key from investigation_reports where id='$RID'")
if docker ps --format '{{.Names}}' | grep -q '^deploy-minio-1$'; then
  MINIO_OK=$(docker exec deploy-minio-1 sh -c "[ -f /data/blockchain-reports/$OBJECT_KEY/xl.meta ] && echo yes || echo no" 2>/dev/null)
  check DATA "报告 artifact 存在于 MinIO blockchain-reports" "$MINIO_OK" "yes"
else
  nv DATA "MinIO 容器不可见，未执行对象存在性核对"
fi
check DATA "报告文件与 DB size_bytes 一致" "$([ "$(wc -c < "$EVIDENCE_DIR/$RID-report.md")" = "$($PG "select size_bytes from investigation_reports where id='$RID'")" ] && echo yes || echo no)" "yes"
check RUNTIME "API 日志无 panic/fatal" "$(grep -cE 'panic|fatal' /tmp/api.log 2>/dev/null | head -1 || true)" "0"
check RUNTIME "API 日志包含 Sprint 4 调查命令" "$([ "$(grep -c '/api/v2/investigations/' /tmp/api.log 2>/dev/null || echo 0)" -gt 0 ] && echo yes || echo no)" "yes"
# 后台审计链 + 权限负向 API
# 审计链完整性：valid 只说明"实际被校验到的区间"自洽，链路是否完整由 integrity_status 表达。
# 存在已封存不可恢复缺口时，integrity_status=sealed_incident 且 prefix_status=sealed_unrecoverable；
# 未封存缺口（undeclared_gap）与边界冲突（boundary_conflict）都必须显式声明，不能被当作通过。
AUDIT_JSON=$(curl -s -b "$JA" -H "X-CSRF-Token: $CSRFA" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' "$BASE/api/admin/v1/audit/verify")
AUDIT_OK=$(echo "$AUDIT_JSON" | jget 'd["data"]["valid"] if "data" in d else d')
check AUDIT-001 "审计哈希链可验证区间 valid=true" "$AUDIT_OK" "True"
AUDIT_INTEG=$(echo "$AUDIT_JSON" | jget 'd["data"]["integrity_status"] if "data" in d else "missing"')
case "$AUDIT_INTEG" in
  intact|sealed_incident) INTEG_DECLARED=declared ;;
  *) INTEG_DECLARED="undeclared:$AUDIT_INTEG" ;;
esac
check AUDIT-001 "审计链完整性状态已明确声明（intact 或已封存缺口）" "$INTEG_DECLARED" "declared"
AUDIT_FROM=$(echo "$AUDIT_JSON" | jget 'd["data"]["verified_from_seq"] if "data" in d else 0')
check AUDIT-001 "审计链校验声明了可信起点 seq" "$([ -n "$AUDIT_FROM" ] && [ "$AUDIT_FROM" -ge 1 ] && echo yes || echo no)" "yes"
for action in case.create investigation:create investigation:start finding:review report:create report:approve report:download; do
  N=$($PG "select count(*) from audit_events_v2 where action='$action' and organization_id='627c131a-3842-4e19-bade-d20ffb8aea8f'")
  check AUDIT-001 "关键动作已落审计：$action" "$([ "${N:-0}" -gt 0 ] && echo yes || echo no)" "yes"
done
check AUDIT-001 "跨组织读对象有 object_denied 审计" "$([ "$($PG "select count(*) from audit_events_v2 where action='authorization.object_denied'")" -gt 0 ] && echo yes || echo no)" "yes"
check AUTHZ "viewer 创建案件 -> 403" "$(code -b "$J3" -H "X-CSRF-Token: $CSRF3" -H 'Content-Type: application/json' -d '{"name":"viewer-must-fail"}' "$BASE/api/v2/cases")" "403"
check AUTHZ "viewer 跨组织读取案件 -> 404（不泄露存在性）" "$(code -b "$J3" "$BASE/api/v2/cases/ae8326ed-a6dc-414c-821f-b9a2973fc258")" "404"
check AUTHZ "无会话访问 /api/v2/cases -> 401" "$(code "$BASE/api/v2/cases")" "401"
check AUTHZ "用户层会话访问后台 -> 401" "$(code -b "$J1" "$BASE/api/admin/v1/overview")" "401"

echo
echo "==== 结果：PASS=$PASS FAIL=$FAIL NOT_VERIFIED=$NOT_VERIFIED ===="
echo "CASE_ID=$CID INVESTIGATION_ID=$IID REPORT_ID=$RID FINDING_ID=$FID EVIDENCE_ID=$EID NEW_EVIDENCE_ID=$NEW_EID" > "$EVIDENCE_DIR/ids.env"
[ "$FAIL" -eq 0 ]
