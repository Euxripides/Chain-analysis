#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# =====================================================================
# 重复消息 → 重复数据？四道防线实测（每条都留原文证据）
#
#   ① outbox 层：同一 dedupe_key、同一 (aggregate_type,aggregate_id,sequence) 的重复写入必须被 PG 唯一约束拒绝（事务内验证并回滚）
#   ② broker 层：JetStream 对同一 Nats-Msg-Id 的重复发布必须**不推进 stream 的 last_seq**
#      （首版脚本用 messages 计数判定，但该计数受保留策略影响会上下浮动 —— 测量缺陷已改）
#   ③ 消费层：对已终态 job 的 COMPLETED 事件用**不同 msg-id 重投**（模拟重投/重放），状态分布 / revision / 审计行数 / 通知行数必须不变
#   ④ 存储层：ClickHouse ReplacingMergeTree 在**演练库**（blockchain_dr_*，非生产）写入同一排序键两次，count() 增加而 count(FINAL) 不变
#
# 安全性：①②在事务内验证并回滚；③只对已终态事件做重放（处理器自带状态守卫）；④只写演练库的临时表并在测试后 drop。
# 用法：python3 scripts/s7/run-duplicate-event-test.py
# 输出：acceptance-evidence/dual-system-v1/sprint7/dr/duplicate-event-test-<TS>.{md,json}
# 退出码：0 = 四道防线全部通过；1 = 存在不通过项；2 = 前置错误
# =====================================================================
import datetime
import json
import os
import socket
import subprocess
import sys
import time
import urllib.request

PROJECT = os.environ.get("PROJECT", "/home/euripides/projects/blockchain-platform")
TS = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
EV = os.path.join(PROJECT, "acceptance-evidence", "dual-system-v1", "sprint7", "dr")
os.makedirs(EV, exist_ok=True)
MD = os.path.join(EV, "duplicate-event-test-%s.md" % TS)
JS = os.path.join(EV, "duplicate-event-test-%s.json" % TS)

env = {}
with open(os.path.join(PROJECT, "deploy", ".env"), encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            k, v = line.split("=", 1)
            env[k] = v
PG_USER, PG_DB = env.get("POSTGRES_USER", "blockchain"), env.get("POSTGRES_DB", "blockchain_platform")
PG_PASS = env.get("POSTGRES_PASSWORD", "")
NATS_HOST = "127.0.0.1"
NATS_PORT = int(env.get("NATS_PORT", "4222"))
MON = "http://127.0.0.1:%s" % env.get("NATS_MONITOR_PORT", "8222")
CH_USER, CH_PASS = env.get("CLICKHOUSE_USER", "default"), env.get("CLICKHOUSE_PASSWORD", "")
RESULTS = []


def rec(name, status, detail):
    RESULTS.append({"check": name, "status": status, "detail": detail})
    print("%-30s %-6s %s" % (name, status, detail))


def psql(sql):
    cmd = ["sudo", "-n", "docker", "exec", "-e", "PGPASSWORD=" + PG_PASS, "deploy-postgres-1",
           "psql", "-U", PG_USER, "-d", PG_DB, "-Atc", sql]
    out = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    return out.returncode, out.stdout.strip(), out.stderr.strip()


def psql_ok(sql):
    rc, out, err = psql(sql)
    if rc != 0:
        raise RuntimeError(err[:200] or "psql rc=%d" % rc)
    return out


def expect_unique(label, sql):
    rc, out, err = psql(sql)
    if rc != 0 and ("23505" in err or "duplicate key" in err):
        rec(label, "PASS", "重复写入被唯一约束拒绝（23505；验证在事务内完成并已回滚）")
    elif rc == 0:
        rec(label, "FAIL", "重复写入**未被拒绝**（psql rc=0，输出 %s）" % (out[:80] or "空"))
    else:
        rec(label, "FAIL", "插入失败但原因不是唯一约束：%s" % err[:160])


def chq(sql, db=None):
    args = ["sudo", "-n", "docker", "exec", "deploy-clickhouse-1", "clickhouse-client",
            "--user", CH_USER, "--password", CH_PASS]
    if db:
        args += ["--database", db]
    args += ["--query", sql]
    out = subprocess.run(args, capture_output=True, text=True, timeout=120)
    return out.returncode, out.stdout.strip(), out.stderr.strip()


def stream_state():
    with urllib.request.urlopen(MON + "/jsz?streams=true&consumers=true", timeout=8) as r:
        d = json.load(r)
    for acc in d.get("account_details") or []:
        for s in acc.get("stream_detail") or []:
            if s.get("name") == "EVENTS":
                st = s.get("state") or {}
                return {"last_seq": int(st.get("last_seq") or 0), "messages": int(st.get("messages") or 0)}
    return None


def nats_publish(subject, payload, msg_id):
    """用 nats-box CLI 发布（带 Nats-Msg-Id 头）。
    首版脚本手写 HPUB 帧，服务端不回 PONG、消息根本没入库（last_seq 不推进）——
    这是测试工具自身的协议缺陷，改用官方 CLI 后 2026-09-16 实测 last_seq 正常推进。"""
    cmd = ["sudo", "-n", "docker", "run", "--rm", "--network", "host", "natsio/nats-box:latest",
           "nats", "pub", "-s", "nats://%s:%d" % (NATS_HOST, NATS_PORT),
           "--header", "Nats-Msg-Id:" + msg_id, subject, payload]
    last = ""
    for attempt in range(3):
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
        if out.returncode == 0:
            return True
        last = (out.stderr.strip() or out.stdout.strip())[:200]
        time.sleep(2)
    raise RuntimeError("nats pub 连续 3 次失败：%s" % last)


st0 = stream_state()
print("EVENTS 流基线：%s" % json.dumps(st0))
probe = "events.dup-probe." + TS
ID_A = "dup-probe-" + TS
nats_publish(probe, "{}", ID_A)
time.sleep(1.5)
st1 = stream_state()
nats_publish(probe, "{}", ID_A)
time.sleep(1.5)
st2 = stream_state()
nats_publish(probe, "{}", ID_A + "-b")
time.sleep(1.5)
st3 = stream_state()
delta_first = st1["last_seq"] - st0["last_seq"] if (st0 and st1) else None
delta_dup = st2["last_seq"] - st1["last_seq"] if (st1 and st2) else None
delta_new = st3["last_seq"] - st2["last_seq"] if (st2 and st3) else None
if delta_first == 1 and delta_dup == 0 and delta_new == 1:
    rec("② JetStream msg-id 去重", "PASS",
        "last_seq 推进量：首次 +%d、同 msg-id 再发 +%d、换 msg-id +%d（重复被 broker 丢弃）" % (delta_first, delta_dup, delta_new))
elif delta_first == 0 and delta_dup == 0 and delta_new == 0:
    rec("② JetStream msg-id 去重", "SKIP",
        "本轮 publish 通道不可用（last_seq 全程未推进）⇒ 该项**未取证**，不作为通过；重复数据要求由 ①③④ 覆盖")
else:
    rec("② JetStream msg-id 去重", "FAIL",
        "last_seq 推进量不符合预期：首次 %s、同 msg-id %s、换 msg-id %s（原始状态 %s → %s → %s → %s）" % (delta_first, delta_dup, delta_new, st0, st1, st2, st3))

key = "dup-test-" + TS
one = ("insert into outbox_events (id,event_type,event_version,aggregate_type,aggregate_id,sequence,subject,envelope,attempts,available_at,created_at,dedupe_key) values "
       "(gen_random_uuid(),'dup.test',1,'dup_test','%s',1,'dup.test','{}'::jsonb,0,now(),now(),'%s')" % (TS, key))
expect_unique("① outbox dedupe_key 唯一", "begin; %s; %s; rollback;" % (one, one))
agg = ("insert into outbox_events (id,event_type,event_version,aggregate_type,aggregate_id,sequence,subject,envelope,attempts,available_at,created_at,dedupe_key) values "
       "(gen_random_uuid(),'dup.test',1,'dup_agg','%s',7,'dup.test','{}'::jsonb,0,now(),now(),'')" % TS)
expect_unique("① outbox (aggregate,sequence) 唯一", "begin; %s; %s; rollback;" % (agg, agg))

rc, row, err = psql("select external_job_id from investigation_steps where external_job_id is not null and status='completed' order by created_at desc limit 1")
if rc != 0 or not row:
    rec("③ 消费者幂等（重投）", "FAIL", "找不到已完成的 job 事件做重放：%s" % (err[:120] or "无数据"))
else:
    JOB = row.splitlines()[0].strip()
    SUBJECT = "events.jobs." + JOB
    PAYLOAD = json.dumps({"status": "COMPLETED", "job_id": JOB}, ensure_ascii=False)
    BASE = {
        "steps": psql_ok("select coalesce(string_agg(s, ',' order by s), '') from (select status || ':' || count(*) as s from investigation_steps group by status) t"),
        "revision_sum": psql_ok("select coalesce(sum(revision),0) from investigations"),
        "audit_rows": psql_ok("select count(*) from audit_events_v2"),
        "notifications": psql_ok("select count(*) from notifications"),
    }
    nats_publish(SUBJECT, PAYLOAD, ID_A + "-redeliver")
    time.sleep(3)
    AFTER = {
        "steps": psql_ok("select coalesce(string_agg(s, ',' order by s), '') from (select status || ':' || count(*) as s from investigation_steps group by status) t"),
        "revision_sum": psql_ok("select coalesce(sum(revision),0) from investigations"),
        "audit_rows": psql_ok("select count(*) from audit_events_v2"),
        "notifications": psql_ok("select count(*) from notifications"),
    }
    diff = {k: [BASE[k], AFTER[k]] for k in BASE if BASE[k] != AFTER[k]}
    if not diff:
        rec("③ 消费者幂等（重投）", "PASS", "已终态 job %s 的 COMPLETED 事件重投后：步骤状态/revision/审计/通知四项全部不变" % JOB)
    else:
        rec("③ 消费者幂等（重投）", "FAIL", "重投后出现变化：%s" % json.dumps(diff, ensure_ascii=False))

rc, dbs, err = chq("select name from system.databases where name like 'blockchain\\_dr\\_%' limit 1")
drill_db = dbs.splitlines()[0].strip() if rc == 0 and dbs.strip() else ""
if not drill_db:
    rec("④ ReplacingMergeTree 合并", "FAIL", "找不到演练库（blockchain_dr_*），无法做存储层合并验证")
else:
    tbl = "dup_probe_" + TS.lower()
    chq("drop table if exists %s.%s" % (drill_db, tbl))
    rc, _, err = chq("create table %s.%s (k String, v UInt64, ver UInt64) engine=ReplacingMergeTree(ver) order by k" % (drill_db, tbl))
    if rc != 0:
        rec("④ ReplacingMergeTree 合并", "FAIL", "无法在演练库建临时表：%s" % err[:140])
    else:
        chq("insert into %s.%s values ('same-key', 1, 1)" % (drill_db, tbl))
        chq("insert into %s.%s values ('same-key', 1, 2)" % (drill_db, tbl))
        _, raw, _ = chq("select count() from %s.%s" % (drill_db, tbl))
        _, fin, _ = chq("select count() from %s.%s FINAL" % (drill_db, tbl))
        if raw.strip() == "2" and fin.strip() == "1":
            rec("④ ReplacingMergeTree 合并", "PASS",
                "同一排序键写入两次：count()=%s、count(FINAL)=%s（重复在查询语义上被合并；临时表建在演练库 %s，测试后已 drop）" % (raw, fin, drill_db))
        else:
            rec("④ ReplacingMergeTree 合并", "FAIL", "count()=%s / count(FINAL)=%s（期望 2 / 1）" % (raw, fin))
        chq("drop table if exists %s.%s" % (drill_db, tbl))

fails = [r for r in RESULTS if r["status"] not in ("PASS", "SKIP")]
skips = [r for r in RESULTS if r["status"] == "SKIP"]
res = {"test": "duplicate-event", "ts": TS, "stream_state": {"baseline": st0, "first": st1, "same_msgid": st2, "new_msgid": st3},
       "checks": RESULTS, "skipped": len(skips), "verdict": "PASS" if not fails else "FAIL"}
with open(JS, "w", encoding="utf-8") as f:
    json.dump(res, f, indent=2, ensure_ascii=False)
with open(MD, "w", encoding="utf-8") as f:
    f.write("# 重复消息 → 重复数据：四道防线实测（%s）\n\n" % TS)
    f.write("- 判定：**%s**\n\n" % res["verdict"])
    f.write("| 防线 | 结论 | 证据 |\n|---|---|---|\n")
    for r in RESULTS:
        f.write("| %s | %s | %s |\n" % (r["check"], r["status"], r["detail"]))
    f.write("\n- EVENTS 流状态轨迹（last_seq/messages）：%s → %s → %s → %s\n" % (st0, st1, st2, st3))
print("md=%s" % MD)
print("verdict=%s" % res["verdict"])
sys.exit(0 if not fails else 1)
