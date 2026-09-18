#!/usr/bin/env bash
# r10-audit-chain-gate-v1.sh — R10 v8 新增：不可抵赖审计链（audit_events_v2 / chain='audit'）独立闸门。
#
# 判定原则（对齐根的要求）：
#   - 用**从冻结源码构建**的 auditverify（不是部署目录里的旧二进制、不是"接口 200"）做密码学校验；
#   - 只对可信段 2344+ 判定；seq 1-2343 永久缺失（0055 / ADR-018）如实标注，**绝不声称完整历史**；
#   - 链头必须等于库中最大 seq 行的 event_hash（证明新写的 export.completed 真在链上，而不是旁路表）；
#   - 用应用凭据跑验链会 permission denied —— 这是**真实运维缺陷**，如实记录（observation），
#     不用"换个只读用户"把它掩盖过去；正式校验使用 audit 表属主凭据。
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
EV="${1:?usage: r10-audit-chain-gate-v1.sh <evidence_dir> [expect_api_sha]}"
EXPECT_API_SHA="${2:-484e2707e6574cce10e41e68047176ff5255aa231d1615c4b10cddd3fe5db2e4}"
SEALED_TAIL="3db79aeb77ce12ae8ccab3540891a7ff241bfc34af8f4a9dcdfd283578686ad1"
TRUSTED_FROM=2344
exec > >(tee "$EV/audit-chain-gate.log") 2>&1
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1 :: $2"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1 :: $2"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3" "have=$1"; else bad "$3" "have=$1 want=$2"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }
one(){ pgq <<< "$1" | head -1 | tr -d '[:space:]'; }

echo "gate_start_utc=$(date -u +%FT%TZ) script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "expect_api_sha=$EXPECT_API_SHA evidence=$EV"

APIPID=$(systemctl --user show -p MainPID --value blockchain-api)
API_SHA=$(sha256sum /proc/$APIPID/exe | cut -d' ' -f1)
chk "$API_SHA" "$EXPECT_API_SHA" "G0 running api sha equals expected artifact" "pid=$APIPID"

AV="$EV/auditverify.build"
if [ ! -x "$AV" ]; then bad "G1 frozen auditverify binary present" "missing $AV"; echo "GATE_FAIL=$FAIL"; exit 1; fi
ok "G1 frozen auditverify present" "sha16=$(sha256sum "$AV" | cut -c1-16)"

set -a; . "$R/deploy/.env"; set +a

echo "=== G2 应用凭据跑验链（真实运维缺陷，如实记录，不作 PASS/FAIL） ==="
DATABASE_URL="$DATABASE_URL" "$AV" -chain audit -timeout 60s > "$EV/auditverify-appcred.json" 2> "$EV/auditverify-appcred.err"
APP_RC=$?
APP_ERR=$(head -c 200 "$EV/auditverify-appcred.err" | tr -d '\n')
echo "app_credential_rc=$APP_RC err=$APP_ERR" | tee "$EV/audit-chain-gate-observations.txt"
echo "OBSERVATION: auditverify 用 DATABASE_URL(blockchain_api) 运行 rc=$APP_RC —— $APP_ERR" >> "$EV/audit-chain-gate-observations.txt"
echo "OBSERVATION: 根因 blockchain_api 是 blockchain_app 的成员但 rolinherit=false，应用侧靠 SET ROLE blockchain_app(DB_RUNTIME_ROLE)；独立验链工具不做 SET ROLE。属真实 DR/运维缺陷，已记录，未以静默换用户方式掩盖。" >> "$EV/audit-chain-gate-observations.txt"

echo "=== G3 冻结源码构建的 auditverify：完整密码学校验（属主凭据，只读） ==="
DATABASE_URL="$MIGRATION_DATABASE_URL" "$AV" -chain audit -timeout 180s > "$EV/auditverify-frozen.json" 2> "$EV/auditverify-frozen.err"
AV_RC=$?
cat "$EV/auditverify-frozen.json"
chk "$AV_RC" "0" "G3 auditverify rc=0 (链自洽)" "rc=$AV_RC"

python3 - "$EV" "$SEALED_TAIL" "$TRUSTED_FROM" <<'PY' > "$EV/audit-chain-gate-verdict.txt" 2>&1
import json, os, sys
ev, sealed_tail, trusted_from = sys.argv[1], sys.argv[2], int(sys.argv[3])
d = json.load(open(os.path.join(ev, "auditverify-frozen.json"), encoding="utf-8"))
res = (d.get("results") or [{}])[0]
sb = res.get("sealed_boundary") or {}
out = {
    "ok": d.get("ok"), "chain": res.get("chain"), "checked": res.get("checked"),
    "valid": res.get("valid"), "broken_at_seq": res.get("broken_at_seq"),
    "integrity_status": res.get("integrity_status"), "prefix_status": res.get("prefix_status"),
    "verified_from_seq": res.get("verified_from_seq"), "head_hash": res.get("head_hash"),
    "sealed_tail_hash": sb.get("sealed_tail_hash"), "gap": [sb.get("gap_from_seq"), sb.get("gap_to_seq")],
    "errors": res.get("errors"),
}
checks = {
    "ok_true": d.get("ok") is True,
    "valid_true": res.get("valid") is True,
    "no_broken_seq": res.get("broken_at_seq") is None,
    "status_acceptable": res.get("integrity_status") in ("intact", "sealed_incident"),
    "verified_from_is_trusted_anchor": res.get("verified_from_seq") == trusted_from,
    "sealed_tail_matches_adr018": sb.get("sealed_tail_hash") == sealed_tail,
    "errors_empty": not (res.get("errors") or []),
}
out["checks"] = checks
json.dump(out, open(os.path.join(ev, "audit-chain-gate-machine.json"), "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print(json.dumps(out, ensure_ascii=False, indent=1))
sys.exit(0 if all(checks.values()) else 1)
PY
VRC=$?
chk "$VRC" "0" "G4 冻结验链器判定：ok/valid/无断点/状态可接受/可信锚点/封存尾哈希/无错误" "verdict_rc=$VRC"

HEAD_REPORTED=$(python3 -c 'import json,sys;print((json.load(open(sys.argv[1])).get("results") or [{}])[0].get("head_hash") or "")' "$EV/auditverify-frozen.json")
DB_HEAD=$(one "SELECT event_hash FROM audit_events_v2 WHERE chain='audit' ORDER BY seq DESC LIMIT 1;")
chk "$HEAD_REPORTED" "$DB_HEAD" "G5 链头 == 库中最大 seq 行的 event_hash（新写事件真在链上）" "head16=${HEAD_REPORTED:0:16}"

echo "=== G6 SQL 侧独立复核（不依赖验链器） ==="
FIRST_SEQ=$(one "SELECT seq FROM audit_events_v2 WHERE chain='audit' ORDER BY seq LIMIT 1;")
FIRST_PREV=$(one "SELECT prev_hash FROM audit_events_v2 WHERE chain='audit' ORDER BY seq LIMIT 1;")
GAPS=$(one "SELECT count(*) FROM (SELECT seq, lag(seq) OVER (ORDER BY seq) AS p FROM audit_events_v2 WHERE chain='audit') t WHERE p IS NOT NULL AND seq <> p+1;")
LINKS=$(one "SELECT count(*) FROM (SELECT seq, prev_hash, lag(event_hash) OVER (ORDER BY seq) AS pe FROM audit_events_v2 WHERE chain='audit') t WHERE pe IS NOT NULL AND prev_hash <> pe;")
BELOW=$(one "SELECT count(*) FROM audit_events_v2 WHERE chain='audit' AND seq < $TRUSTED_FROM;")
chk "$FIRST_SEQ" "$TRUSTED_FROM" "G6.1 首个 seq == 可信锚点 2344" "first_seq=$FIRST_SEQ"
chk "$FIRST_PREV" "$SEALED_TAIL" "G6.2 首行 prev_hash == sealed_tail_hash(ADR-018)" "prev16=${FIRST_PREV:0:16}"
chk "$GAPS" "0" "G6.3 可信段无 seq 空洞" "gaps=$GAPS"
chk "$LINKS" "0" "G6.4 可信段无 prev_hash 断链" "broken=$LINKS"
chk "$BELOW" "0" "G6.5 锚点以下 0 行（1-2343 永久缺失，未回填未伪造）" "below=$BELOW"

echo "=== RESULT PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" -eq 0 ] || exit 1
exit 0
