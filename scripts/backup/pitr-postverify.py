#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""PITR 演练独立后验（只读）。
用法: python3 pitr-postverify.py <TS> [restoreA_port]
不修改原 frozen 脚本、原报告、原 JSON 与任何运行中的实例；只新增一个机器可读后验文件：
  acceptance-evidence/dual-system-v1/sprint7/dr/pitr-<TS>.neg-postverify.json
目的：把"缺段负向成立"与"目标恢复成立"绑定到**具体证据行 + 具体 sha256**，
      并纠正原 JSON 中 log_evidence 取到无关 .history 行的口径问题。
"""
import hashlib, json, os, re, subprocess, sys

TS = sys.argv[1] if len(sys.argv) > 1 else ""
APORT = sys.argv[2] if len(sys.argv) > 2 else "5554"
if not TS:
    print("usage: pitr-postverify.py <TS> [restoreA_port]", file=sys.stderr); sys.exit(2)
DR = "/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/dr"
LOG = os.path.join(DR, "pitr-" + TS + ".logs")
OUT = os.path.join(DR, "dr-postverify-pitr-" + TS + ".json")
NEG = "pitr-neg-" + TS
RA = "pitr-restore-" + TS

def sha256_file(p):
    try:
        h = hashlib.sha256()
        with open(p, "rb") as fh:
            for b in iter(lambda: fh.read(1 << 20), b""):
                h.update(b)
        return h.hexdigest()
    except OSError:
        return None

def run(cmd):
    try:
        p = subprocess.run(cmd, shell=True, capture_output=True, text=True, timeout=90)
        return p.returncode, p.stdout.strip(), p.stderr.strip()
    except Exception as e:  # noqa
        return -1, "", str(e)

def readtext(p):
    try:
        return open(p, errors="replace").read()
    except OSError:
        return ""

res = {"drill": "pitr", "postverify_for_ts": TS, "verdict": "FAIL", "checks": {}, "evidence": {}}
fails = []
def chk(name, ok, detail):
    res["checks"][name] = {"ok": bool(ok), "detail": detail}
    if not ok:
        fails.append(name + ": " + str(detail))

# ---- 绑定：frozen 脚本 / 报告 / JSON / 日志 的 sha256
res["evidence"]["frozen_script"] = os.path.join(LOG, "frozen", "pitr-drill.sh")
res["evidence"]["frozen_script_sha256"] = sha256_file(res["evidence"]["frozen_script"])
res["evidence"]["report_sha256"] = sha256_file(os.path.join(DR, "pitr-" + TS + ".md"))
res["evidence"]["result_sha256"] = sha256_file(os.path.join(DR, "pitr-" + TS + ".json"))
neglog_path = os.path.join(LOG, "restoreNeg-container.log")
res["evidence"]["neg_container_log"] = neglog_path
res["evidence"]["neg_container_log_sha256"] = sha256_file(neglog_path)
chk("frozen_script_present", bool(res["evidence"]["frozen_script_sha256"]), res["evidence"]["frozen_script_sha256"])
chk("neg_log_present", bool(res["evidence"]["neg_container_log_sha256"]), res["evidence"]["neg_container_log_sha256"])

# ---- 被隐藏的必需段（与运行时同一算法：freeze-target/plan.json 的中位元素）
plan = {}
try:
    plan = json.load(open(os.path.join(LOG, "freeze-target", "plan.json")))
except Exception:
    pass
inc = plan.get("included") or []
seg = inc[len(inc) // 2] if len(inc) >= 3 else (inc[0] if inc else "")
res["evidence"]["hidden_segment"] = seg
chk("hidden_segment_identified", bool(re.fullmatch(r"[0-9A-F]{24}", seg or "")), seg)

negtext = readtext(neglog_path)
seg_lines = [ln for ln in negtext.splitlines() if seg and seg in ln and ("cannot stat" in ln or "No such file" in ln or "could not open" in ln)]
fatal_lines = [ln for ln in negtext.splitlines() if re.search(r"FATAL:\s+recovery ended before configured recovery target", ln)]
stop_lines = [ln for ln in negtext.splitlines() if re.search(r"recovery stopping (before|after)", ln, re.I)]
res["evidence"]["hidden_segment_requests"] = seg_lines[:6]
res["evidence"]["target_not_reached_fatal"] = fatal_lines[:3]
res["evidence"]["target_stop_lines"] = stop_lines[:3]
chk("hidden_segment_actually_requested", len(seg_lines) > 0, seg_lines[:2])
chk("fatal_target_not_reached", len(fatal_lines) > 0, fatal_lines[:1])
chk("no_target_stop_in_negative", len(stop_lines) == 0, stop_lines[:1])

# ---- 容器退出码必须非 0，且状态必须终止（不得仅凭 in_recovery != f）
rc, out, err = run("docker inspect -f '{{.State.Status}} {{.State.ExitCode}}' " + NEG)
parts = out.split()
state = parts[0] if parts else ""
ecode = int(parts[1]) if len(parts) > 1 and parts[1].lstrip("-").isdigit() else None
res["evidence"]["neg_container_state"] = state
res["evidence"]["neg_container_exit_code"] = ecode
chk("neg_container_terminated", state == "exited", state)
chk("neg_container_nonzero_exit", ecode is not None and ecode != 0, ecode)

# ---- 生产归档里被隐藏段仍在，且与冻结时的 sha256 一致
prod_sha = None
rc, out, err = run("docker exec -u postgres deploy-postgres-1 sh -c 'cd /var/lib/postgresql/wal-archive && sha256sum " + seg + "'")
if out:
    prod_sha = out.split()[0]
frozen_sha = None
for ln in readtext(os.path.join(LOG, "freeze-target", "sha256.txt")).splitlines():
    p = ln.split()
    if len(p) == 2 and p[1].lstrip("./") == seg:
        frozen_sha = p[0]
res["evidence"]["prod_archive_segment_sha256_now"] = prod_sha
res["evidence"]["frozen_segment_sha256"] = frozen_sha
chk("prod_archive_segment_intact", prod_sha is not None, prod_sha)
chk("prod_archive_segment_unchanged", prod_sha is not None and prod_sha == frozen_sha, "%s vs %s" % (prod_sha, frozen_sha))

# ---- 目标恢复（A）独立复核：真实端口 + 标记可见性
def aq(sql, db="postgres"):
    rc, out, err = run("docker exec %s psql -p %s -U blockchain -d %s -qAtc \"%s\"" % (RA, APORT, db, sql.replace('"', '\\"')))
    return out if rc == 0 else ""
res["evidence"]["restoreA_port"] = APORT
inrec = aq("select pg_is_in_recovery()")
tli = aq("select timeline_id from pg_control_checkpoint()")
lastc = aq("select coalesce(to_char(pg_last_xact_replay_timestamp() at time zone 'UTC','YYYY-MM-DD HH24:MI:SS.US'),'')")
alines = [ln for ln in readtext(os.path.join(LOG, "restoreA-container.log")).splitlines() if re.search(r"recovery stopping (before|after)", ln, re.I)]
res["evidence"]["restoreA_in_recovery"] = inrec
res["evidence"]["restoreA_timeline"] = tli
res["evidence"]["restoreA_last_completed_xact"] = lastc
res["evidence"]["restoreA_stop_lines"] = alines[:2]
chk("restoreA_promoted", inrec == "f", inrec)
chk("restoreA_new_timeline", tli not in ("", "1"), tli)
chk("restoreA_stop_evidence", len(alines) > 0, alines[:1])

rjson = {}
try:
    rjson = json.load(open(os.path.join(DR, "pitr-" + TS + ".json")))
except Exception:
    pass
mk = (rjson.get("markers") or {})
sb = (mk.get("before") or {}).get("seq"); hb = (mk.get("before") or {}).get("hash")
sf = (mk.get("filler") or {}).get("seq"); sa = (mk.get("after") or {}).get("seq")
r_hash = aq("select coalesce(event_hash,'') from audit_events_v2 where seq=%s" % sb, "blockchain_platform") if sb else ""
r_fill = aq("select count(*) from audit_events_v2 where seq=%s" % sf, "blockchain_platform") if sf else ""
r_after = aq("select count(*) from audit_events_v2 where seq=%s" % sa, "blockchain_platform") if sa else ""
r_max = aq("select coalesce(max(seq),0) from audit_events_v2", "blockchain_platform")
res["evidence"]["restoreA_marker_before"] = {"seq": sb, "prod_hash": hb, "restored_hash": r_hash}
res["evidence"]["restoreA_marker_filler_count"] = r_fill
res["evidence"]["restoreA_marker_after_count"] = r_after
res["evidence"]["restoreA_max_seq"] = r_max
chk("restoreA_before_present_hash_match", bool(hb) and r_hash == hb, "%s vs %s" % (r_hash, hb))
chk("restoreA_after_absent", r_after == "0", r_after)
chk("restoreA_max_leq_before", r_max == str(sb) if sb else False, "%s vs %s" % (r_max, sb))

res["verdict"] = "PASS" if not fails else "FAIL"
res["failed_checks"] = fails
res["note"] = ("本文件为**追加式后验**：不修改原 frozen 脚本/报告/JSON，也不重跑已成立的恢复；"
               "用于把缺段负向与目标恢复绑定到具体段名、具体日志行、具体 sha256 与容器退出码。")
json.dump(res, open(OUT, "w"), indent=2, ensure_ascii=False)
print(json.dumps({"out": OUT, "verdict": res["verdict"], "hidden_segment": seg,
                  "exit_code": ecode, "failed_checks": fails}, ensure_ascii=False))
sys.exit(0 if res["verdict"] == "PASS" else 1)
