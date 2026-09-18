#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# =====================================================================
# WAL 观察窗口（用户指令 2026-09-17：上线后观察 30 分钟，目标同任务量下 WAL 降 >=80%）
#
# 每分钟采样一次：
#   * PostgreSQL WAL 生成量（pg_current_wal_lsn() 差值，MB/分钟）
#   * 任务量口径（"相同任务量"的可比性）：job_event_log 新增行数、活跃下载任务数、provider_usage 行数
#   * postgres 主进程 write_bytes 增量（与文件系统层交叉核对）
# 用法：python3 scripts/ops/observe-wal.py [分钟数] [标签]
# 输出：acceptance-evidence/dual-system-v1/sprint7/perf/wal-observation-<标签>-<TS>.{csv,md}
# =====================================================================
import datetime
import os
import subprocess
import sys
import time

PROJECT = os.environ.get("PROJECT", "/home/euripides/projects/blockchain-platform")
MINUTES = int(sys.argv[1]) if len(sys.argv) > 1 else 30
LABEL = sys.argv[2] if len(sys.argv) > 2 else "window"
EV = os.path.join(PROJECT, "acceptance-evidence", "dual-system-v1", "sprint7", "perf")
os.makedirs(EV, exist_ok=True)
TS = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
CSV = os.path.join(EV, "wal-observation-%s-%s.csv" % (LABEL, TS))
MD = os.path.join(EV, "wal-observation-%s-%s.md" % (LABEL, TS))

env = {}
with open(os.path.join(PROJECT, "deploy", ".env"), encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            k, v = line.split("=", 1)
            env[k] = v
PG_USER = env.get("POSTGRES_USER", "blockchain")
PG_DB = env.get("POSTGRES_DB", "blockchain_platform")
PG_PASS = env.get("POSTGRES_PASSWORD", "")


def psql(sql):
    cmd = ["sudo", "-n", "docker", "exec", "-e", "PGPASSWORD=" + PG_PASS, "deploy-postgres-1",
           "psql", "-U", PG_USER, "-d", PG_DB, "-Atc", sql]
    out = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    if out.returncode != 0:
        raise RuntimeError(out.stderr.strip()[:200])
    return out.stdout.strip()


def wal_bytes():
    s = psql("select pg_current_wal_lsn()")
    hi, lo = s.split("/")
    return int(hi, 16) * 16 ** 8 + int(lo, 16)


def pg_write_bytes():
    try:
        pid = subprocess.run(["pgrep", "-f", "postgres.*-D"], capture_output=True, text=True).stdout.split()[0]
        with open("/proc/%s/io" % pid) as f:
            for line in f:
                if line.startswith("write_bytes"):
                    return int(line.split()[1])
    except Exception:
        pass
    return 0


def sample():
    return (wal_bytes(),
            int(psql("select count(*) from job_event_log")),
            int(psql("select count(*) from provider_usage")),
            int(psql("select count(*) from download_jobs where status in ('RUNNING','QUEUED')")),
            pg_write_bytes())


print("== WAL 观察 [%s] 开始 %s，共 %d 分钟 ==" % (LABEL, datetime.datetime.now(datetime.timezone.utc).isoformat(), MINUTES))
rows = []
prev = sample()
for i in range(1, MINUTES + 1):
    time.sleep(60)
    cur = sample()
    wal_mb = (cur[0] - prev[0]) / 1048576.0
    pg_mb = (cur[4] - prev[4]) / 1048576.0
    ev = cur[1] - prev[1]
    us = cur[2] - prev[2]
    rows.append({"minute": i, "wal_mb": round(wal_mb, 2), "event_rows": ev, "active_jobs": cur[3],
                 "usage_rows": us, "pg_write_mb": round(pg_mb, 2)})
    print("  %2d 分钟：WAL %.2f MB（事件 %d 行 / 活跃任务 %d）" % (i, wal_mb, ev, cur[3]))
    prev = cur

n = len(rows) or 1
wal_avg = sum(r["wal_mb"] for r in rows) / n
pg_avg = sum(r["pg_write_mb"] for r in rows) / n
ev_avg = sum(r["event_rows"] for r in rows) / n
jobs_avg = sum(r["active_jobs"] for r in rows) / n
with open(CSV, "w", encoding="utf-8") as f:
    f.write("minute,wal_mb,event_rows,active_jobs,usage_rows,pg_write_mb\n")
    for r in rows:
        f.write("%(minute)d,%(wal_mb).2f,%(event_rows)d,%(active_jobs)d,%(usage_rows)d,%(pg_write_mb).2f\n" % r)
with open(MD, "w", encoding="utf-8") as f:
    f.write("# WAL 观察窗口（%s）\n\n" % LABEL)
    f.write("- 时长：%d 分钟（样本 %d 个）\n" % (MINUTES, n))
    f.write("- **WAL 生成：平均 %.2f MB/分钟（约 %.1f GB/天）**\n" % (wal_avg, wal_avg * 1440 / 1024))
    f.write("- postgres 进程 write_bytes：平均 %.2f MB/分钟\n" % pg_avg)
    f.write("- 任务量口径：job_event_log 新增 %.1f 行/分钟；活跃任务平均 %.1f 个\n" % (ev_avg, jobs_avg))
    f.write("\n| 分钟 | WAL MB | 事件行 | 活跃任务 | usage 行 | pg write MB |\n|---:|---:|---:|---:|---:|---:|\n")
    for r in rows:
        f.write("| %(minute)d | %(wal_mb).2f | %(event_rows)d | %(active_jobs)d | %(usage_rows)d | %(pg_write_mb).2f |\n" % r)
print("WAL 平均 %.2f MB/分钟（约 %.1f GB/天）；事件 %.1f 行/分钟；活跃任务 %.1f" % (wal_avg, wal_avg * 1440 / 1024, ev_avg, jobs_avg))
print("csv=%s" % CSV)
print("md=%s" % MD)
