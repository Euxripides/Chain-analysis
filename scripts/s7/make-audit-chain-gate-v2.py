#!/usr/bin/env python3
# make-audit-chain-gate-v2.py -- audit chain gate v2: turns the app-credential DR path into a HARD gate.
# v1 (preserved) only recorded the "permission denied" as an observation. v2 (after the auditverify
# -db-role/SET ROLE fix) requires the app credential to actually verify the chain and requires the
# evidence to show session_user = app credential while current_user = runtime role.
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
SRC = sys.argv[1] if len(sys.argv) > 1 else R + "/scripts/s7/r10-audit-chain-gate-v1.sh"
DST = sys.argv[2] if len(sys.argv) > 2 else R + "/scripts/s7/r10-audit-chain-gate-v2.sh"
EXPECT_SRC_SHA = "11b26f69321098246fe5f9961459d7c56cbb6be6e3cd991adc24fa1b24440973"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if sha != EXPECT_SRC_SHA:
    print("FATAL baseline sha mismatch"); sys.exit(3)
text = raw.decode("utf-8")
P = []

P.append(("header",
  "# r10-audit-chain-gate-v1.sh — R10 v8 新增：不可抵赖审计链（audit_events_v2 / chain='audit'）独立闸门。",
  "# r10-audit-chain-gate-v2.sh — v1 的升级版：把**应用凭据验链**从只记录提升为硬判据。\n"
  "#\n"
  "# v1（原样保留，不 hot-edit）实测到的真实运维缺陷：auditverify 用应用凭据（DATABASE_URL，\n"
  "# user=blockchain_api）运行时报 permission denied for table audit_integrity_incidents (SQLSTATE 42501)，\n"
  "# 根因 blockchain_api 是 blockchain_app 的成员但 rolinherit=false（必须显式 SET ROLE）。\n"
  "# 修复：auditverify 增加 -db-role（默认取 DB_RUNTIME_ROLE）并在 AfterConnect 执行 SET ROLE，\n"
  "# 不新增任何 GRANT（最小授权不变）。v2 因此要求：应用凭据 + SET ROLE 必须 rc=0、valid=true、\n"
  "# 且证据里 session_user=应用凭据、current_user=运行角色 —— 即 DR 恢复后可用应用凭据验链可机器判定。\n"
  "# 其余 G0/G1/G3-G6 与 v1 逐字相同。"))

P.append(("g2-block",
  '''echo "=== G2 应用凭据跑验链（真实运维缺陷，如实记录，不作 PASS/FAIL） ==="
DATABASE_URL="$DATABASE_URL" "$AV" -chain audit -timeout 60s > "$EV/auditverify-appcred.json" 2> "$EV/auditverify-appcred.err"
APP_RC=$?
APP_ERR=$(head -c 200 "$EV/auditverify-appcred.err" | tr -d '\\n')
echo "app_credential_rc=$APP_RC err=$APP_ERR" | tee "$EV/audit-chain-gate-observations.txt"
echo "OBSERVATION: auditverify 用 DATABASE_URL(blockchain_api) 运行 rc=$APP_RC —— $APP_ERR" >> "$EV/audit-chain-gate-observations.txt"
echo "OBSERVATION: 根因 blockchain_api 是 blockchain_app 的成员但 rolinherit=false，应用侧靠 SET ROLE blockchain_app(DB_RUNTIME_ROLE)；独立验链工具不做 SET ROLE。属真实 DR/运维缺陷，已记录，未以静默换用户方式掩盖。" >> "$EV/audit-chain-gate-observations.txt"''',
  '''echo "=== G2 应用凭据 + SET ROLE 验链（v2 硬判据：DR 恢复后必须能用应用凭据验链） ==="
if [ -z "${DB_RUNTIME_ROLE:-}" ]; then
  bad "G2.0 DB_RUNTIME_ROLE present" "empty (app credential cannot reach the audit tables without SET ROLE)"
else
  DATABASE_URL="$DATABASE_URL" "$AV" -db-role "$DB_RUNTIME_ROLE" -chain audit -timeout 180s \\
    > "$EV/auditverify-appcred.json" 2> "$EV/auditverify-appcred.err"
  APP_RC=$?
  APP_ERR=$(head -c 200 "$EV/auditverify-appcred.err" | tr -d '\\n')
  chk "$APP_RC" "0" "G2.1 应用凭据(含 SET ROLE) rc=0" "err=$APP_ERR"
  python3 - "$EV/auditverify-appcred.json" "$EV/auditverify-appcred-verdict.json" <<'PY' > "$EV/auditverify-appcred-verdict.txt" 2>&1
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
r = (d.get("results") or [{}])[0]
out = {"ok": d.get("ok"), "db_role": d.get("db_role"), "session_user": d.get("session_user"),
       "current_user": d.get("current_user"), "valid": r.get("valid"), "checked": r.get("checked"),
       "broken_at_seq": r.get("broken_at_seq"), "head_hash": r.get("head_hash"),
       "integrity_status": r.get("integrity_status"), "verified_from_seq": r.get("verified_from_seq")}
checks = {"app_ok": out["ok"] is True, "valid": out["valid"] is True,
          "broken_none": out["broken_at_seq"] is None, "checked_positive": (out["checked"] or 0) > 0,
          "session_user_is_app_credential": out["session_user"] == "blockchain_api",
          "current_user_is_runtime_role": out["current_user"] == out["db_role"]}
out["checks"] = checks
json.dump(out, open(sys.argv[2], "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print(json.dumps(out, ensure_ascii=False, indent=1))
sys.exit(0 if all(checks.values()) else 1)
PY
  AVC=$?
  chk "$AVC" "0" "G2.2 应用凭据验链 ok/valid/无断点/session=应用凭据/current=运行角色" "appcred_verdict_rc"
fi'''))

for tag, old, new in P:
    n = text.count(old)
    if n != 1:
        print("FATAL patch %s matched %d times (want 1)" % (tag, n)); sys.exit(4)
    text = text.replace(old, new, 1); print("applied %s" % tag)

if os.path.exists(DST):
    print("FATAL destination exists: %s" % DST); sys.exit(5)
open(DST, "w", encoding="utf-8", newline="\n").write(text)
out = open(DST, "rb").read()
print("dst=%s bytes=%d sha256=%s" % (DST, len(out), hashlib.sha256(out).hexdigest()))
