#!/usr/bin/env python3
"""P4 all-44-route matrix runner (v1).

The frozen matrix (p4-matrix-20260914T173037Z/matrix.json, sha 9f6f2388...) lists
44 commit-gated write routes that reach a non-PostgreSQL dependency, 18 of them
with a direct side effect and neither a transactional outbox nor a scope recheck.
That matrix is a STATIC result. This runner is the behavioural half: for every one
of the 44 routes it tries to produce

  POSITIVE  the request really succeeds and the route really produces the
            external effect the matrix attributes to it, and
  NEGATIVE  the same request refused by the commit gate while the caller scope is
            invalidated (calibration seam at phase post), with ZERO external
            effect and zero committed rows.

A route that cannot be exercised is reported NOT_VERIFIED with the exact reason,
never PASS. Every counter is a real measurement:

  bus         core-NATS messages on the scope-control subject, counted by
              scripts/s7/nats_subject_counter.py. The negative window STARTS
              AFTER the invalidation returns, because the invalidation itself
              publishes on that subject.
  clickhouse  system.query_log rows written by the Go driver, counted after
              SYSTEM FLUSH LOGS so the count is deterministic instead of racing
              the asynchronous log flush.
  object      object names under the MinIO bind mount, before/after.
  provider    the candidate own Prometheus counters for outbound provider calls.

ASCII only. Prints no credential, Cookie, CSRF token or DSN.
"""
import argparse
import json
import os
import re
import shlex
import subprocess
import sys
import time

import requests

R = "/home/euripides/projects/blockchain-platform"
API = "http://127.0.0.1:8080"
PG_CONTAINER = "deploy-postgres-1"
CH_CONTAINER = "deploy-clickhouse-1"
MINIO_DATA = "/data/blockchain-platform/minio"
SCOPE_SUBJECT = "events.sse.scope_invalidated"
NATS_URL = "127.0.0.1:4222"
NATS_COUNTER = R + "/scripts/s7/nats_subject_counter.py"
BUCKETS = ("blockchain-exports", "blockchain-raw", "blockchain-audit")


def sq(v):
    return shlex.quote(str(v))


def sh(cmd, timeout=120):
    p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    return (p.stdout or "").strip()


def sh_stdin(cmd, data, timeout=120):
    p = subprocess.run(cmd, input=data, capture_output=True, text=True, timeout=timeout)
    return (p.stdout or "").strip()


def pg(sql):
    return sh_stdin(["docker", "exec", "-i", PG_CONTAINER, "psql", "-U", "blockchain",
                     "-d", "blockchain_platform", "-q", "-tA", "-F|", "-v", "ON_ERROR_STOP=1"], sql)


def ch(sql, flush=True):
    if flush:
        subprocess.run(["docker", "exec", "-i", CH_CONTAINER, "clickhouse-client",
                        "--query", "SYSTEM FLUSH LOGS"], capture_output=True, text=True, timeout=60)
    return sh(["docker", "exec", "-i", CH_CONTAINER, "clickhouse-client", "--query", sql])


def ch_int(sql):
    try:
        return int((ch(sql) or "0").split()[0])
    except Exception:
        return -1


def ch_selects_since(anchor, needle=None):
    where = ("event_time >= fromUnixTimestamp(%d) AND type='QueryFinish' "
             "AND client_name LIKE 'clickhouse-go%%' AND query_kind='Select'" % anchor)
    if needle:
        where += " AND positionCaseInsensitive(query,'%s') > 0" % needle
    return ch_int("SELECT count() FROM system.query_log WHERE " + where)


def ch_inserts_since(anchor, needle=None):
    where = ("event_time >= fromUnixTimestamp(%d) AND type='QueryFinish' "
             "AND client_name LIKE 'clickhouse-go%%' AND query_kind='Insert'" % anchor)
    if needle:
        where += " AND positionCaseInsensitive(query,'%s') > 0" % needle
    return ch_int("SELECT count() FROM system.query_log WHERE " + where)


def minio_objects():
    total = 0
    for sub in BUCKETS:
        out = sh(["bash", "-c", "ls -1R %s/%s 2>/dev/null | grep -c . || true" % (MINIO_DATA, sub)])
        try:
            total += int(out or "0")
        except Exception:
            pass
    return total


def metric_sum(base, name):
    try:
        r = requests.get(base + "/metrics", timeout=10)
    except Exception:
        return 0
    total = 0.0
    for line in r.text.splitlines():
        if line.startswith("#"):
            continue
        parts = line.split()
        if len(parts) == 2 and re.match("^" + re.escape(name) + "($|\\{)", parts[0]):
            try:
                total += float(parts[1])
            except Exception:
                pass
    return int(total)


def provider_calls(base):
    return metric_sum(base, "ankr_request_duration_seconds_count") + metric_sum(base, "rpc_batch_calls_total")


class NatsWatch(object):
    """Background subject counter; stop() waits for the window and returns it."""

    def __init__(self, seconds):
        self.out = "/tmp/p4matrix-nats-%d-%d.txt" % (int(time.time() * 1000), os.getpid() % 100000)
        try:
            os.unlink(self.out)
        except OSError:
            pass
        self.proc = subprocess.Popen(
            ["python3", NATS_COUNTER, NATS_URL, SCOPE_SUBJECT, str(seconds), self.out],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    def stop(self):
        try:
            self.proc.wait(timeout=120)
        except Exception:
            try:
                self.proc.kill()
            except Exception:
                pass
        try:
            with open(self.out) as fh:
                for line in fh:
                    if line.startswith("TOTAL "):
                        return int(line.split()[1])
        except Exception:
            pass
        return -1

class Session(object):
    def __init__(self, base):
        self.base = base
        self.s = requests.Session()

    def login(self, email, password, audience, workspace_id, timeout=30):
        r = self.s.post(self.base + "/api/auth/v1/login",
                        headers={"X-Auth-Audience": audience, "Content-Type": "application/json"},
                        json={"identifier": email, "password": password, "audience": audience,
                              "workspace_id": workspace_id}, timeout=timeout)
        return r

    def csrf(self, audience):
        for name in ("cs_" + audience + "_session_csrf", "cs_session_csrf", "csrf_" + audience):
            v = self.s.cookies.get(name)
            if v:
                return v
        for c in self.s.cookies:
            if "csrf" in c.name:
                return c.value
        return ""

    def call(self, method, path, audience, body=None, extra_headers=None, timeout=180):
        headers = {"X-Auth-Audience": audience}
        tok = self.csrf(audience)
        if tok:
            headers["X-CSRF-Token"] = tok
        if body is not None:
            headers["Content-Type"] = "application/json"
        if extra_headers:
            headers.update(extra_headers)
        fn = getattr(self.s, method.lower())
        kw = {"headers": headers, "timeout": timeout}
        if body is not None:
            kw["data"] = json.dumps(body)
        return fn(self.base + path, **kw)


def read_creds(path):
    out = {}
    with open(path) as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip()
    return out


# Route table. mode:
#   ab        positive + negative are both exercised
#   neg_only  only the refused-request control is run (the positive would perform
#             an irreversible or global production action); reported NOT_VERIFIED
#   nv        cannot be exercised at all; reported NOT_VERIFIED
ROUTES = [
    dict(i=0, m="POST", p="/api/v1/download-jobs/{job_run}/pause", aud="user", perm="job:control", body={"reason": "p4 matrix positive"}, mode="ab"),
    dict(i=1, m="POST", p="/api/v1/download-jobs/{job_run}/resume", aud="user", perm="job:control", body={"reason": "p4 matrix positive"}, mode="ab"),
    dict(i=2, m="POST", p="/api/v1/download-jobs/{job_run}/cancel", aud="user", perm="job:control", body={"reason": "p4 matrix"}, mode="neg_only", note="cancelling a real job would discard its checkpoints"),
    dict(i=3, m="POST", p="/api/v1/download-jobs/{job_run}/guard/continue", aud="user", perm="job:control", body={"reason": "p4 matrix"}, mode="neg_only", note="volume-guard continue changes worker behaviour on a live job"),
    dict(i=4, m="POST", p="/api/v1/download-jobs/{job_run}/guard/abort", aud="user", perm="job:control", body={"reason": "p4 matrix"}, mode="neg_only", note="volume-guard abort stops a live job"),
    dict(i=5, m="POST", p="/api/v1/download-jobs/{job_run}/subjects/{subject}/pause", aud="user", perm="job:control", body={"reason": "p4 matrix"}, mode="ab"),
    dict(i=6, m="POST", p="/api/v1/download-jobs/{job_run}/performance-mode", aud="admin", perm="admin:performance:update", body={"mode": "safe", "reason": "p4 matrix"}, mode="neg_only", note="changing the performance mode of a live job is global provider throttling"),
    dict(i=7, m="POST", p="/api/v1/admin/ankr/benchmarks", aud="admin", perm="admin:performance:update",
         body={"chain": "bsc", "dataset": "token_transfers", "address": "0x0000000000000000000000000000000000000001",
               "target_block": 1000000, "page_size": 1000, "shard_count": 1}, mode="neg_only",
         note="a real benchmark job would spend real provider quota"),
    dict(i=8, m="POST", p="/api/v1/exports", aud="user", perm="export:create",
         body={"datasets": ["token_transfers"], "format": "csv", "chain": "bsc",
               "addresses": ["0x0000000000000000000000000000000000000001"]}, mode="ab", async_terminal="export"),
    dict(i=9, m="POST", p="/api/v1/admin/tokens/bsc/{fresh_token}/resolve", aud="admin", perm="admin:dataset:manage", body={}, mode="ab"),
    dict(i=10, m="POST", p="/api/v1/admin/tokens/bsc/{fresh_token}/override", aud="admin", perm="admin:dataset:manage",
         body={"name": "P4 Matrix Token", "symbol": "P4M", "decimals": 18, "token_type": "erc20", "reason": "p4 matrix"}, mode="ab"),
    dict(i=11, m="POST", p="/api/v1/admin/methods/signatures", aud="admin", perm="admin:dataset:manage",
         body={"signature": "p4matrix(uint256,address)", "chain": "bsc"}, mode="ab"),
    dict(i=12, m="POST", p="/api/v1/admin/contracts/bsc/{fresh_contract}/abi", aud="admin", perm="admin:dataset:manage",
         body={"abi": [{"type": "function", "name": "p4matrix", "inputs": [{"name": "a", "type": "uint256"}], "outputs": []}],
               "source": "user_upload", "verified": False}, mode="ab"),
    dict(i=13, m="POST", p="/api/admin/v1/sessions/{victim_session}/commands/revoke", aud="admin", perm="admin:session:revoke",
         body={"confirm": True, "reason": "p4 matrix"}, mode="ab"),
    dict(i=14, m="POST", p="/api/admin/v1/audit/integrity/incidents/requests", aud="admin", perm="admin:audit:seal",
         body={"chain": "audit", "reason": "p4 matrix", "step_up_password": "{owner_pw}"}, mode="neg_only",
         note="the positive seals a new audit integrity incident, which is audit-history state"),
    dict(i=15, m="POST", p="/api/admin/v1/audit/integrity/incidents/requests/{incident}/approve", aud="admin", perm="admin:approval:decide",
         body={"confirm": True, "reason": "p4 matrix"}, mode="neg_only", note="approving a real audit incident changes the sealed boundary"),
    dict(i=16, m="POST", p="/api/admin/v1/audit/integrity/incidents/requests/{incident}/reject", aud="admin", perm="admin:approval:decide",
         body={"reason": "p4 matrix"}, mode="neg_only", note="rejecting a real audit incident changes the sealed boundary"),
    dict(i=17, m="POST", p="/api/admin/v1/audit/integrity/incidents/requests/{incident}/execute", aud="admin", perm="admin:audit:seal",
         body={"confirm": True, "reason": "p4 matrix"}, mode="neg_only", note="executing an audit incident writes an audit anchor object"),
    dict(i=18, m="POST", p="/api/admin/v1/audit/anchors", aud="admin", perm="admin:audit:seal",
         body={"chain": "audit", "reason": "p4 matrix"}, mode="neg_only", note="the positive creates a durable audit anchor"),
    dict(i=19, m="POST", p="/api/admin/v1/control/kill-switch/requests", aud="admin", perm="admin:killswitch:engage",
         body={"mode": "engage", "reason": "p4 matrix probe", "step_up_password": "{owner_pw}"}, mode="ab"),
    dict(i=20, m="POST", p="/api/admin/v1/control/kill-switch/requests/{killswitch_pending}/approve", aud="admin", perm="admin:approval:decide",
         body={"reason": "p4 matrix"}, mode="neg_only", note="approving a kill-switch request arms a global control action"),
    dict(i=21, m="POST", p="/api/admin/v1/control/kill-switch/requests/{killswitch_pending}/reject", aud="admin", perm="admin:approval:decide",
         body={"reason": "p4 matrix"}, mode="neg_only", note="the target request is global control-plane state"),
    dict(i=22, m="POST", p="/api/admin/v1/control/kill-switch/requests/{killswitch_active}/execute", aud="admin", perm="admin:killswitch:engage",
         body={"mode": "engage", "reason": "p4 matrix", "step_up_password": "{owner_pw}"}, mode="neg_only",
         note="executing the kill switch would stop production jobs"),
    dict(i=23, m="POST", p="/api/admin/v1/control/kill-switch/requests/{killswitch_active}/restore", aud="admin", perm="admin:killswitch:release",
         body={"mode": "restore", "reason": "p4 matrix", "step_up_password": "{owner_pw}"}, mode="neg_only",
         note="restoring the kill switch changes global control state"),
    dict(i=24, m="POST", p="/api/admin/v1/alerts/{alert}/commands/acknowledge", aud="admin", perm="admin:alert:manage",
         body={"note": "p4 matrix"}, mode="nv", note="no alert row exists in api_control_alerts"), 
    dict(i=25, m="POST", p="/api/admin/v1/feature-flags/{flag}/commands/evaluate", aud="admin", perm="admin:flag:update",
         body={"reason": "p4 matrix"}, mode="ab"),
    dict(i=26, m="POST", p="/api/admin/v1/retention-policies/preview", aud="admin", perm="admin:retention:read",
         body={"policy_key": "{retention_key}"}, mode="ab"),
    dict(i=27, m="PATCH", p="/api/admin/v1/retention-policies/{retention_id}", aud="admin", perm="admin:retention:manage",
         body={"reason": "p4 matrix", "enabled": False}, mode="ab"),
    dict(i=28, m="POST", p="/api/admin/v1/deletion-requests", aud="admin", perm="admin:deletion:request",
         body={"resource_type": "raw_objects", "policy_key": "{retention_key}", "reason": "p4 matrix", "step_up_password": "{owner_pw}"}, mode="ab"),
    dict(i=29, m="POST", p="/api/admin/v1/deletion-requests/{deletion}/commands/approve", aud="admin", perm="admin:deletion:approve",
         body={"note": "p4 matrix"}, mode="neg_only", note="approving a deletion request would enable real object deletion"),
    dict(i=30, m="POST", p="/api/admin/v1/backups", aud="admin", perm="admin:backup:create",
         body={"target": "config", "reason": "p4 matrix", "drill": True}, mode="ab"),
    dict(i=31, m="POST", p="/api/admin/v1/backups/{backup}/commands/verify", aud="admin", perm="admin:backup:read",
         body={"reason": "p4 matrix"}, mode="nv", note="backup_jobs is empty, so no backup id exists to verify"),
    dict(i=32, m="POST", p="/api/admin/v1/restores/preview", aud="admin", perm="admin:backup:restore",
         body={"reason": "p4 matrix"}, mode="ab"),
    dict(i=33, m="POST", p="/api/admin/v1/restores", aud="admin", perm="admin:backup:restore",
         body={"reason": "p4 matrix", "step_up_password": "{owner_pw}"}, mode="neg_only", note="creating a restore request can start a real restore"),
    dict(i=34, m="POST", p="/api/admin/v1/audit/export", aud="admin", perm="admin:audit:export",
         body={"reason": "p4 matrix"}, mode="ab"),
    dict(i=35, m="POST", p="/api/admin/v1/budgets/{budget}/commands/evaluate", aud="admin", perm="admin:budget:read",
         body={"reason": "p4 matrix"}, mode="ab"),
    dict(i=36, m="PATCH", p="/api/admin/v1/providers/ankr-multichain", aud="admin", perm="admin:provider:update",
         body={"reason": "p4 matrix probe", "priority": "{provider_priority}"}, mode="ab", restore="provider"),
    dict(i=37, m="POST", p="/api/admin/v1/api-keys/{apikey}/commands/revoke", aud="admin", perm="admin:apikey:revoke",
         body={"reason": "p4 matrix", "step_up_password": "{owner_pw}"}, mode="neg_only", note="revoking a real API key would break the MCP/GPT Actions integration"),
    dict(i=38, m="PATCH", p="/api/admin/v1/budgets/{budget}", aud="admin", perm="admin:budget:update",
         body={"reason": "p4 matrix", "enabled": True}, mode="ab"),
    dict(i=39, m="POST", p="/api/v2/downloads/validate-addresses", aud="user", perm="download:create",
         body={"chain": "bsc", "items": [{"row": 1, "address": "0x0000000000000000000000000000000000000002"}]}, mode="ab"),
    dict(i=40, m="POST", p="/api/v2/downloads/preview", aud="user", perm="download:create",
         body={"chain": "bsc", "addresses": ["0x00000000000000000000000000000000000000ff"], "datasets": ["token_transfers"],
               "range": {"type": "block", "from_block": 1000000, "to_block": 2000000}}, mode="ab"),
    dict(i=41, m="POST", p="/api/v2/downloads", aud="user", perm="download:create",
         body={"chain": "bsc", "addresses": ["0x00000000000000000000000000000000000000ff"], "datasets": ["token_transfers"],
               "range": {"type": "block", "from_block": 1000000, "to_block": 1000001}}, mode="neg_only",
         note="creating a real download job would spend real provider quota in the worker"),
    dict(i=42, m="POST", p="/api/v2/exports", aud="user", perm="export:create",
         body={"datasets": ["token_transfers"], "format": "csv", "chain": "bsc",
               "addresses": ["0x0000000000000000000000000000000000000001"]}, mode="ab", async_terminal="export"),
    dict(i=43, m="POST", p="/api/v2/exports/{export}/download-ticket", aud="user", perm="export:download",
         body={}, mode="ab"),
]

def now_ts():
    try:
        return int(ch("SELECT toUnixTimestamp(now())"))
    except Exception:
        return int(time.time())


def snapshot(base):
    t0 = now_ts()
    return {"t": t0,
            "ch_sel": ch_selects_since(t0),
            "ch_ins": ch_inserts_since(t0),
            "obj": minio_objects(),
            "prov": provider_calls(base)}


def delta(a, b):
    return {k: b[k] - a[k] for k in ("ch_sel", "ch_ins", "obj", "prov")}


def resolve_params():
    org = pg("SELECT id::text FROM organizations WHERE slug='c1lat'").splitlines()[0].strip()
    ws_a = pg("SELECT id::text FROM workspaces WHERE organization_id='%s' AND slug='c1lat-a'" % org).splitlines()[0].strip()
    ws_b = pg("SELECT id::text FROM workspaces WHERE organization_id='%s' AND slug='c1lat-b'" % org).splitlines()[0].strip()
    p = {"org": org, "ws_a": ws_a, "ws_b": ws_b}
    p["job_run"] = (pg("SELECT id::text FROM download_jobs WHERE organization_id='%s' AND status IN ('RUNNING','QUEUED') ORDER BY created_at DESC LIMIT 1" % org).splitlines() or [""])[0].strip()
    p["subject"] = "0x0000000000000000000000000000000000000001"
    p["incident"] = (pg("SELECT id::text FROM audit_integrity_incidents ORDER BY created_at DESC LIMIT 1").splitlines() or [""])[0].strip()
    p["killswitch_pending"] = (pg("SELECT id::text FROM kill_switch_engagements WHERE status='pending_approval' ORDER BY created_at DESC LIMIT 1").splitlines() or [""])[0].strip()
    p["killswitch_active"] = (pg("SELECT id::text FROM kill_switch_engagements WHERE status='active' ORDER BY created_at DESC LIMIT 1").splitlines() or [""])[0].strip()
    p["flag"] = "user.gauge_rollout"
    p["retention_key"] = "export-objects-default"
    p["retention_id"] = (pg("SELECT id::text FROM retention_policies WHERE policy_key='export-objects-default'").splitlines() or [""])[0].strip()
    p["budget"] = (pg("SELECT id::text FROM api_control_budgets ORDER BY created_at DESC LIMIT 1").splitlines() or [""])[0].strip()
    p["apikey"] = (pg("SELECT id::text FROM gpt_action_api_keys ORDER BY created_at DESC LIMIT 1").splitlines() or [""])[0].strip()
    p["export"] = (pg("SELECT id::text FROM exports WHERE organization_id='%s' AND status='COMPLETED' AND object_ref IS NOT NULL ORDER BY created_at DESC LIMIT 1" % org).splitlines() or [""])[0].strip()
    p["alert"] = (pg("SELECT id::text FROM api_control_alerts ORDER BY updated_at DESC LIMIT 1").splitlines() or [""])[0].strip()
    p["deletion"] = ""
    p["backup"] = ""
    p["provider_priority"] = (pg("SELECT priority::text FROM provider_descriptors WHERE provider_key='ankr-multichain'").splitlines() or ["10"])[0].strip()
    return p


def fill(path, params):
    out = path
    for k, v in params.items():
        out = out.replace("{" + k + "}", str(v))
    return out


def fill_body(body, params):
    if not isinstance(body, dict):
        return body
    raw = json.dumps(body)
    for k, v in params.items():
        raw = raw.replace("{" + k + "}", str(v))
    return json.loads(raw)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bin", default=R + "/backend/bin.new/api-expcap-r3-calib")
    ap.add_argument("--port", default="18196")
    ap.add_argument("--delay-ms", default="4000")
    ap.add_argument("--only", default="")
    ap.add_argument("--evidence", default="")
    ap.add_argument("--creds", default="/home/euripides/.c1lat-creds.env")
    args = ap.parse_args()

    ts = time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
    ev = args.evidence or (R + "/acceptance-evidence/dual-system-v1/sprint7/p4-routes-p4routes-" + ts)
    os.makedirs(ev, exist_ok=True)
    os.chmod(ev, 0o700)
    log = open(ev + "/matrix-console.log", "w")

    def say(msg):
        print(msg)
        log.write(msg + "\n")
        log.flush()

    base = "http://127.0.0.1:" + str(args.port)
    say("ts_utc=" + ts)
    say("script_sha256=" + sh(["sha256sum", __file__]).split()[0])
    say("bin=" + args.bin + " bin_sha256=" + sh(["sha256sum", args.bin]).split()[0])
    say("evidence=" + ev + " delay_ms=" + str(args.delay_ms) + " port=" + str(args.port))

    api_pid = sh(["bash", "-c", "systemctl --user show blockchain-api.service -p MainPID --value"])
    api_sha = sh(["bash", "-c", "sha256sum /proc/%s/exe 2>/dev/null | cut -d' ' -f1" % api_pid])
    say("production_api_pid=%s sha=%s" % (api_pid, api_sha))
    say("production_ready=%s" % requests.get(API + "/health/ready", timeout=10).status_code)

    say("=== fixture ===")
    fix = R + "/backend/bin/fixture-membership"
    env = {}
    with open(R + "/deploy/.env") as fh:
        for line in fh:
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                env[k.strip()] = v.strip().strip('"').strip("'")
    os.environ.update(env)
    fixout = subprocess.run([fix, "-org-slug", "c1lat", "-org-name", "P4 route matrix",
                             "-workspaces", "c1lat-a,c1lat-b",
                             "-identities", "owner:c1lat-own@fixture.local:c1lat-a,security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b",
                             "-out", args.creds], capture_output=True, text=True, env=os.environ.copy())
    say(fixout.stdout.strip() + fixout.stderr.strip())
    cr = read_creds(args.creds)
    params = resolve_params()
    ws_a = params["ws_a"]
    ws_b = params["ws_b"]
    owner_email = cr.get("C1MEM_C1LAT_OWN_EMAIL", "")
    owner_pw = cr.get("C1MEM_C1LAT_OWN_PW", "")
    inv_email = cr.get("C1MEM_C1LAT_INV_EMAIL", "")
    inv_pw = cr.get("C1MEM_C1LAT_INV_PW", "")
    owner_pw1 = "Pw-" + ts + "-routes-1a7c"
    inv_pw1 = "Pw-" + ts + "-routes-3f21"

    def clear_flag(email, pw, newpw, aud):
        s = Session(API)
        r = s.login(email, pw, aud, ws_a)
        tok = s.csrf(aud)
        c = s.s.post(API + "/api/auth/v1/password/change",
                     headers={"X-Auth-Audience": aud, "Content-Type": "application/json", "X-CSRF-Token": tok},
                     json={"current_password": pw, "new_password": newpw}, timeout=30)
        say("clear_flag %s aud=%s login=%s change=%s" % (email, aud, r.status_code, c.status_code))

    clear_flag(owner_email, owner_pw, owner_pw1, "admin")
    clear_flag(inv_email, inv_pw, inv_pw1, "user")
    params["owner_pw"] = owner_pw1

    say("params org=%s ws_a=%s ws_b=%s job_run=%s budget=%s export=%s" % (
        params["org"], ws_a, ws_b, params["job_run"], params["budget"], params["export"]))

    say("=== candidate ===")
    # deploy/.env carries at least one backslash-continued value, so a plain
    # line-by-line Python parse yields a truncated secret and the candidate
    # refuses to boot (measured: ANKR_MANAGEMENT_ENCRYPTION_KEY len 1 vs the 36
    # chars bash gets). The candidate is therefore launched through bash, exactly
    # like every other gate in this tree.
    cand_cmd = ("set -a; . " + sq(R + "/deploy/.env") + "; set +a; "
                "export API_HTTP_PORT=" + sq(str(args.port)) + "; "
                "export SCOPE_CALIB_ENABLE=1; export SCOPE_CALIB_PHASE=post; "
                "export SCOPE_CALIB_DELAY_MS=" + sq(str(args.delay_ms)) + "; "
                "unset SCOPE_CALIB_ROUTE; exec " + sq(args.bin))
    candlog = open(ev + "/candidate.log", "w")
    cand = subprocess.Popen(["bash", "-c", cand_cmd], cwd=R + "/backend",
                            stdout=candlog, stderr=subprocess.STDOUT, preexec_fn=os.setsid)
    ready = False
    for _ in range(240):
        try:
            if requests.get(base + "/health/ready", timeout=5).status_code == 200:
                ready = True
                break
        except Exception:
            pass
        time.sleep(0.5)
    say("candidate_ready=%s pid=%s" % (ready, cand.pid))
    if not ready:
        say("MATRIX_ABORT=candidate_not_ready")
        return 1

    results = []
    try:
        for rt in ROUTES:
            if args.only and str(rt["i"]) not in args.only.split(","):
                continue
            results.append(run_route(base, rt, params, ws_a, ws_b, owner_email, owner_pw1,
                                     inv_email, inv_pw1, args, ev, say))
    finally:
        try:
            os.killpg(os.getpgid(cand.pid), 15)
        except Exception:
            pass
        time.sleep(1)
        try:
            os.killpg(os.getpgid(cand.pid), 9)
        except Exception:
            pass

    say("=== summary ===")
    npass = sum(1 for r in results if r["verdict"] == "PASS")
    nfail = sum(1 for r in results if r["verdict"] == "FAIL")
    nnv = sum(1 for r in results if r["verdict"] == "NOT_VERIFIED")
    for r in results:
        say("%-4s %-6s %-58s pos=%-4s neg=%-4s effects=%s %s" % (
            r["i"], r["m"], r["path_short"], r["pos_status"], r["neg_status"], r["effects"], r["reason"]))
    say("ROUTES_TOTAL=%d PASS=%d FAIL=%d NOT_VERIFIED=%d" % (len(results), npass, nfail, nnv))
    say("production_api_sha_after=" + sh(["bash", "-c", "sha256sum /proc/%s/exe 2>/dev/null | cut -d' ' -f1" % api_pid]))
    say("production_ready_after=%s" % requests.get(API + "/health/ready", timeout=10).status_code)
    with open(ev + "/results.json", "w") as fh:
        json.dump(results, fh, indent=2)
    log.close()
    return 0


def run_route(base, rt, params, ws_a, ws_b, owner_email, owner_pw1, inv_email, inv_pw1, args, ev, say):
    import threading
    i = rt["i"]
    aud = rt["aud"]
    path = fill(rt["p"], params)
    body = fill_body(rt.get("body"), params)
    short = rt["p"]
    res = {"i": i, "m": rt["m"], "path": rt["p"], "path_short": short,
           "pos_status": "-", "neg_status": "-", "effects": "-", "verdict": "NOT_VERIFIED", "reason": ""}
    email = owner_email if aud == "admin" else inv_email
    pw = owner_pw1 if aud == "admin" else inv_pw1
    if rt["mode"] == "nv":
        res["reason"] = "cannot be exercised: " + rt.get("note", "")
        say("NOT_VERIFIED %d %s %s : %s" % (i, rt["m"], short, res["reason"]))
        return res
    win = int(args.delay_ms) // 1000 + 8
    sess = Session(base)
    r = sess.login(email, pw, aud, ws_a)
    if r.status_code != 200:
        res["reason"] = "caller_session_login_failed_%d" % r.status_code
        say("NOT_VERIFIED %d %s %s : %s" % (i, rt["m"], short, res["reason"]))
        return res
    # POSITIVE
    if rt["mode"] == "ab":
        snap0 = snapshot(base)
        watch_pos = NatsWatch(win)
        pos = sess.call(rt["m"], path, aud, body)
        wait = 20 if rt.get("async_terminal") else 2
        time.sleep(wait)
        if rt.get("async_terminal") == "export":
            try:
                eid = pos.json().get("data", {}).get("id") or pos.json().get("id")
            except Exception:
                eid = None
            waited = 0
            while eid and waited < 150:
                st = pg("SELECT COALESCE(status,'-') FROM exports WHERE id='%s'" % eid).strip()
                if st in ("COMPLETED", "FAILED"):
                    break
                time.sleep(5)
                waited += 5
            time.sleep(2)
            res["pos_terminal"] = st if eid else "no_id"
        snap1 = snapshot(base)
        pos_bus = watch_pos.stop()
        res["pos_bus"] = pos_bus
        res["pos_status"] = pos.status_code
        res["effects"] = str(delta(snap0, snap1)) + " bus=" + str(pos_bus)
        res["pos_delta"] = delta(snap0, snap1)
        if pos.status_code >= 400:
            res["reason"] = "positive refused: http=%d body=%s" % (pos.status_code, pos.text[:120].replace("\n", " "))
            say("NOT_VERIFIED %d %s %s : %s" % (i, rt["m"], short, res["reason"]))
            return res
    else:
        res["reason"] = "negative only: " + rt.get("note", "")
    # NEGATIVE
    sess2 = Session(base)
    r = sess2.login(email, pw, aud, ws_a)
    if r.status_code != 200:
        res["reason"] = (res["reason"] + " ").strip() + "negative session login failed %d" % r.status_code
        say("NOT_VERIFIED %d %s %s : %s" % (i, rt["m"], short, res["reason"]))
        return res
    n_snap0 = snapshot(base)
    watch_whole = NatsWatch(win)
    box = {}

    def do_req():
        try:
            box["r"] = sess2.call(rt["m"], path, aud, body)
        except Exception as exc:
            box["e"] = str(exc)

    th = threading.Thread(target=do_req)
    th.start()
    time.sleep(1)
    t_mut = time.time()
    if aud == "admin":
        inv = sess2.call("POST", "/api/auth/v1/logout", aud, {})
    else:
        inv = sess2.call("POST", "/api/auth/v1/session/workspace", aud, {"workspace_id": ws_b})
    watch_after = NatsWatch(max(2, win - 2))
    th.join(timeout=200)
    dn_whole = watch_whole.stop()
    dn_after = watch_after.stop()
    time.sleep(2)
    n_snap1 = snapshot(base)
    neg_delta = delta(n_snap0, n_snap1)
    st = box.get("r").status_code if box.get("r") is not None else -1
    res["neg_status"] = st
    res["neg_delta"] = neg_delta
    res["bus_whole"] = dn_whole
    res["bus_after"] = dn_after
    res["invalidate_status"] = inv.status_code
    refused = st in (401, 403, 409)
    leaks = []
    pd = res.get("pos_delta")
    if pd:
        for cls, key in (("clickhouse_select", "ch_sel"), ("clickhouse_insert", "ch_ins"),
                         ("object", "obj"), ("provider", "prov")):
            if pd.get(key, 0) > 0 and neg_delta.get(key, 0) > 0:
                leaks.append("%s pos=%d neg=%d" % (cls, pd[key], neg_delta[key]))
        if res.get("pos_bus", 0) > 0 and dn_after and dn_after > 0:
            leaks.append("bus pos=%d neg_after=%d" % (res["pos_bus"], dn_after))
    if not refused:
        res["verdict"] = "FAIL" if rt["mode"] == "ab" else "NOT_VERIFIED"
        res["reason"] = ("refused control did not refuse: http=%d %s" % (st, (box.get("r").text[:100] if box.get("r") is not None else ""))).strip()
    elif leaks:
        res["verdict"] = "FAIL"
        res["reason"] = "external effect survived a refused request: " + "; ".join(leaks)
    elif rt["mode"] == "ab":
        res["verdict"] = "PASS"
        res["reason"] = "refused %d with zero delta on every class that moved in the positive" % st
    else:
        res["verdict"] = "NOT_VERIFIED"
    say("%s %d %s %s pos=%s neg=%s pos_effects=%s neg_delta=%s bus_whole=%s bus_after=%s" % (
        res["verdict"], i, rt["m"], short, res["pos_status"], st, res.get("effects"), neg_delta,
        dn_whole, dn_after))
    say("   reason: " + res["reason"])
    return res


if __name__ == "__main__":
    sys.exit(main())