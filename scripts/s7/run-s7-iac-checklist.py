#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# =====================================================================
# §16.9 IaC 人工核对清单（可复算、留证据）
#
# 为什么需要它：trivy `config` 对本仓库的 IaC 面（deploy/compose.yaml、systemd unit、
# nginx 配置）**检出配置文件数 0**（用 k8s Pod 对照样本验证过工具本身可用），也就是说
# "扫描通过"在本仓库无法成立。这里用一份**逐项断言 + 引用原文**的清单替代"没有扫描器就跳过"。
#
# 用法：python3 scripts/s7/run-s7-iac-checklist.py
# 输出：acceptance-evidence/dual-system-v1/sprint7/security/iac-checklist-<TS>.{md,json}
# 退出码：0 = 清单执行完成（是否存在 FINDING 看报告）；2 = 前置错误
# 注意：本脚本**只读**，不修改任何 IaC 文件、不重启任何服务。
# =====================================================================
import datetime
import json
import os
import re
import sys

try:
    import yaml
except ImportError:
    print("ABORT: 需要 PyYAML（pip install pyyaml）", file=sys.stderr)
    sys.exit(2)

PROJECT = os.environ.get("PROJECT", "/home/euripides/projects/blockchain-platform")
DEPLOY = os.path.join(PROJECT, "deploy")
TS = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
EV = os.path.join(PROJECT, "acceptance-evidence", "dual-system-v1", "sprint7", "security")
os.makedirs(EV, exist_ok=True)
MD = os.path.join(EV, "iac-checklist-%s.md" % TS)
JS = os.path.join(EV, "iac-checklist-%s.json" % TS)

FINDINGS = []
ROWS = []


def add(scope, item, status, evidence):
    ROWS.append({"scope": scope, "item": item, "status": status, "evidence": evidence})
    if status == "FINDING":
        FINDINGS.append("%s / %s：%s" % (scope, item, evidence))


def line_of(path, pattern):
    """返回文件中第一个匹配 pattern 的行号与原文（用于把结论钉到行）。"""
    try:
        with open(path, encoding="utf-8") as f:
            for i, raw in enumerate(f, 1):
                if re.search(pattern, raw):
                    return "%s:%d %s" % (os.path.basename(path), i, raw.strip()[:120])
    except OSError:
        pass
    return ""


# ---------------- compose ----------------
compose_path = os.path.join(DEPLOY, "compose.yaml")
with open(compose_path, encoding="utf-8") as f:
    compose = yaml.safe_load(f) or {}
services = compose.get("services") or {}
for name, svc in sorted(services.items()):
    svc = svc or {}
    raw = json.dumps(svc, ensure_ascii=False)
    # C1 端口绑定
    ports = svc.get("ports") or []
    bad_ports = [p for p in ports if not str(p).startswith("127.0.0.1:")]
    add("compose:%s" % name, "端口仅绑定回环（127.0.0.1）",
        "FINDING" if bad_ports else "PASS",
        ("未绑回环的端口：%s" % bad_ports) if bad_ports else ("ports=%s" % ports))
    # C2 restart 策略
    rs = svc.get("restart")
    add("compose:%s" % name, "restart 策略存在且为 unless-stopped/always",
        "PASS" if rs in ("unless-stopped", "always") else "FINDING", "restart=%s" % rs)
    # C3 healthcheck
    add("compose:%s" % name, "定义 healthcheck",
        "PASS" if svc.get("healthcheck") else "FINDING",
        "healthcheck=%s" % ("已定义" if svc.get("healthcheck") else "缺失"))
    # C4 镜像 tag 固定
    image = str(svc.get("image") or "")
    add("compose:%s" % name, "镜像 tag 固定（不得 latest/无 tag）",
        "FINDING" if (image.endswith(":latest") or ":" not in image) else "PASS", "image=%s" % image)
    # C5 privileged
    add("compose:%s" % name, "未开启 privileged",
        "FINDING" if svc.get("privileged") else "PASS", "privileged=%s" % svc.get("privileged"))
    # C6 no-new-privileges
    sec = [str(x) for x in (svc.get("security_opt") or [])]
    add("compose:%s" % name, "security_opt 含 no-new-privileges:true",
        "PASS" if any("no-new-privileges" in s for s in sec) else "FINDING", "security_opt=%s" % sec)
    # C7 cap_drop ALL
    cap = [str(x) for x in (svc.get("cap_drop") or [])]
    add("compose:%s" % name, "cap_drop 含 ALL",
        "PASS" if any(c.upper() == "ALL" for c in cap) else "FINDING", "cap_drop=%s" % cap)
    # C8 read_only rootfs
    add("compose:%s" % name, "rootfs 只读（read_only: true）",
        "PASS" if svc.get("read_only") else "FINDING", "read_only=%s" % svc.get("read_only"))
    # C9 资源限制
    limits = svc.get("deploy", {}).get("resources", {}).get("limits") if isinstance(svc.get("deploy"), dict) else None
    has_lim = bool(svc.get("mem_limit") or svc.get("cpus") or limits)
    add("compose:%s" % name, "定义资源限制（mem_limit/cpus/deploy.resources.limits）",
        "PASS" if has_lim else "FINDING", "mem_limit=%s cpus=%s deploy.limits=%s" % (svc.get("mem_limit"), svc.get("cpus"), limits))
    # C10 日志轮转
    logging = svc.get("logging") or {}
    opts = (logging.get("options") or {}) if isinstance(logging, dict) else {}
    add("compose:%s" % name, "日志驱动配置轮转（max-size/max-file）",
        "PASS" if (opts.get("max-size") and opts.get("max-file")) else "FINDING",
        "logging=%s" % json.dumps(logging, ensure_ascii=False))
    # C11 卷为显式主机路径
    vols = [str(v) for v in (svc.get("volumes") or [])]
    anon = [v for v in vols if ":" not in v]
    add("compose:%s" % name, "卷使用显式主机路径（不用匿名卷）",
        "FINDING" if anon else "PASS", "volumes=%s" % vols)
    # C12 敏感值来自环境变量
    env = svc.get("environment") or {}
    if isinstance(env, list):
        env = dict(x.split("=", 1) for x in env if "=" in x)
    hard = [k for k, v in env.items() if re.search(r"PASSWORD|SECRET|TOKEN|KEY", str(k)) and "${" not in str(v)]
    add("compose:%s" % name, "敏感环境变量取自 ${...}（不得硬编码）",
        "FINDING" if hard else "PASS", "硬编码项=%s" % hard)

# ---------------- systemd units ----------------
unit_dir = DEPLOY
units = sorted(f for f in os.listdir(unit_dir) if f.endswith((".service", ".timer")))
for u in units:
    path = os.path.join(unit_dir, u)
    text = open(path, encoding="utf-8", errors="replace").read()
    def has(pat):
        return re.search(pat, text, re.M) is not None
    add("unit:%s" % u, "Restart 策略存在",
        "PASS" if has(r"^Restart=") else "FINDING", line_of(path, r"^Restart=") or "未找到 Restart=")
    add("unit:%s" % u, "NoNewPrivileges=true",
        "PASS" if has(r"^NoNewPrivileges=(true|yes)") else "FINDING",
        line_of(path, r"^NoNewPrivileges=") or "未设置（默认 false）")
    add("unit:%s" % u, "ProtectSystem=strict|full",
        "PASS" if has(r"^ProtectSystem=(strict|full)") else "FINDING",
        line_of(path, r"^ProtectSystem=") or "未设置")
    add("unit:%s" % u, "PrivateTmp=true",
        "PASS" if has(r"^PrivateTmp=(true|yes)") else "FINDING",
        line_of(path, r"^PrivateTmp=") or "未设置")
    add("unit:%s" % u, "ProtectHome=true",
        "PASS" if has(r"^ProtectHome=(true|yes)") else "FINDING",
        line_of(path, r"^ProtectHome=") or "未设置")
    # 日志轮转：StandardOutput=append: 无轮转
    logline = line_of(path, r"^Standard(Output|Error)=")
    add("unit:%s" % u, "日志输出有轮转（append: 视为无轮转）",
        "FINDING" if "append:" in logline else "PASS", logline or "未显式设置")
    add("unit:%s" % u, "未禁用启动限流（StartLimitIntervalSec 不得为 0）",
        "FINDING" if re.search(r"^StartLimitIntervalSec=0", text, re.M) else "PASS",
        line_of(path, r"^StartLimitIntervalSec=") or "未设置")

# ---------------- nginx ----------------
for conf in sorted(f for f in os.listdir(DEPLOY) if f.startswith("nginx") and f.endswith(".conf")):
    path = os.path.join(DEPLOY, conf)
    text = open(path, encoding="utf-8", errors="replace").read()
    add("nginx:%s" % conf, "显式声明 ssl_protocols（且不含 TLSv1/1.1）",
        "PASS" if (re.search(r"ssl_protocols", text) and not re.search(r"ssl_protocols[^;]*TLSv1(\.1)?[^.]", text)) else "FINDING",
        line_of(path, r"ssl_protocols") or "未声明 ssl_protocols（沿用默认）")
    headers = [h for h in ("Strict-Transport-Security", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy") if h in text]
    add("nginx:%s" % conf, "安全响应头 ≥3 项",
        "PASS" if len(headers) >= 3 else "FINDING", "已声明：%s" % (headers or "无"))
    add("nginx:%s" % conf, "配置请求/连接限流（limit_req 或 limit_conn）",
        "PASS" if (re.search(r"limit_req", text) or re.search(r"limit_conn", text)) else "FINDING",
        line_of(path, r"limit_req|limit_conn") or "未配置限流")
    add("nginx:%s" % conf, "显式 client_max_body_size",
        "PASS" if re.search(r"client_max_body_size", text) else "FINDING",
        line_of(path, r"client_max_body_size") or "未声明（默认 1m）")

# ---------------- 输出 ----------------
pass_n = sum(1 for r in ROWS if r["status"] == "PASS")
find_n = sum(1 for r in ROWS if r["status"] == "FINDING")
res = {"checklist": "iac-manual", "ts": TS, "project": PROJECT,
       "method": "trivy config 对本仓库 IaC 面检出 0 个配置文件（工具可用，见 image-scan-*/config-deploy.json 与 k8s 对照样本），故以逐项断言替代",
       "items": ROWS, "pass": pass_n, "findings": find_n, "finding_list": FINDINGS}
with open(JS, "w", encoding="utf-8") as f:
    json.dump(res, f, indent=2, ensure_ascii=False)
with open(MD, "w", encoding="utf-8") as f:
    f.write("# IaC 人工核对清单（%s）\n\n" % TS)
    f.write("- 方法：trivy `config` 对本仓库 IaC 面**检出配置文件数 0**（k8s 对照样本可报 3 条 misconfiguration ⇒ 工具可用），因此改用本清单逐项断言并引用原文\n")
    f.write("- 只读：本清单不修改任何 IaC 文件、不重启任何服务\n")
    f.write("- 结果：**PASS %d 项 / FINDING %d 项**（共 %d 项）\n\n" % (pass_n, find_n, len(ROWS)))
    f.write("| 范围 | 核对项 | 结论 | 证据 |\n|---|---|---|---|\n")
    for r in ROWS:
        f.write("| %s | %s | %s | `%s` |\n" % (r["scope"], r["item"], r["status"], str(r["evidence"]).replace("|", "\\|")))
    if FINDINGS:
        f.write("\n## FINDING 汇总（需处置或书面接受）\n\n")
        for x in FINDINGS:
            f.write("- %s\n" % x)
print("items=%d pass=%d findings=%d" % (len(ROWS), pass_n, find_n))
print("md=%s" % MD)
print("json=%s" % JS)
sys.exit(0)
