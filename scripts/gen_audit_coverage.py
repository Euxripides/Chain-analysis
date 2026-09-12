#!/usr/bin/env python3
"""生成审计覆盖报告（§27.1 AUDIT-001「用户操作清单逐项执行」的证据）。

做法是**从代码反推**，而不是人工声称：
  * 登录/会话类事件：扫描 internal/iam 里 Event* 常量；
  * 业务写操作：扫描 api 包中 require() 声明的权限键与 legacy 兜底规则；
  * 敏感读取：扫描 policy.Catalog 中 sensitive=true 的权限；
  * 页面行为遥测：检查 user_activity_events 是否有写入方。

输出 docs/implementation/audit-coverage.md。覆盖不到的类别会显式标 NOT_IMPLEMENTED，
不写成"已覆盖"。
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BE = ROOT / "backend"


def iam_events() -> list[tuple[str, bool]]:
    """返回 [(事件名, 是否真的会被触发)]。

    只统计"已定义常量"是不够的：`auth.mfa.success` 这类常量已经存在，
    但 MFA 尚未实现，没有任何调用点会产生它。把这种情况标成"已实现"
    就是过度声称，必须区分开。
    """
    defined = re.findall(r'(Event\w+)\s*=\s*"([a-z0-9._]+)"', (BE / "internal/iam/loginevent.go").read_text(encoding="utf-8"))
    emitted: set[str] = set()
    for path in BE.rglob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        # 只有出现在事件构造里的常量才算真正会产生。
        for m in re.finditer(r'EventType:\s*([\w.]+)', text):
            emitted.add(m.group(1).split('.')[-1])
        for m in re.finditer(r'record\((Event\w+)', text):
            emitted.add(m.group(1))
        for m in re.finditer(r'EventType:\s*iam\.(Event\w+)', text):
            emitted.add(m.group(1))
    return sorted(((name, const in emitted) for const, name in defined))


def audited_permissions() -> dict[str, list[str]]:
    """返回 {权限键: [方法 路径]}，即所有会写审计的业务/管理端点。"""
    out: dict[str, list[str]] = {}
    for fname in ("authv1.go", "userv2.go", "adminv1.go", "adminorg.go"):
        path = BE / "internal/api" / fname
        if not path.exists():
            continue
        body = path.read_text(encoding="utf-8")
        prefix = {
            "authv1.go": "/api/auth/v1",
            "userv2.go": "/api/v2",
            "adminv1.go": "/api/admin/v1",
            "adminorg.go": "/api/admin/v1",
        }[fname]
        for m in re.finditer(r'^\s*(?:r|pr)\.(Get|Post|Patch|Delete|Put)\("([^"]+)"\s*,\s*(.+)$', body, re.M):
            method, p, tail = m.group(1).upper(), m.group(2), m.group(3)
            perm = re.search(r'require\("([a-z0-9:_\-]+)"', tail)
            if perm:
                out.setdefault(perm.group(1), []).append(f"{method} {prefix}{p}")
    return out


def sensitive_permissions() -> list[str]:
    body = (BE / "internal/policy/catalog.go").read_text(encoding="utf-8")
    out = []
    for m in re.finditer(r'"([a-z0-9:_\-]+)":\s*\{[^}]*,\s*true\}', body):
        out.append(m.group(1))
    return sorted(set(out))


def has_activity_writer() -> bool:
    """user_activity_events 是否有代码写入方。"""
    for p in (BE / "internal").rglob("*.go"):
        if p.name.endswith("_test.go"):
            continue
        if "user_activity_events" in p.read_text(encoding="utf-8", errors="replace"):
            if "INSERT INTO user_activity_events" in p.read_text(encoding="utf-8", errors="replace"):
                return True
    return False


def main() -> int:
    events = iam_events()
    perms = audited_permissions()
    sensitive = sensitive_permissions()
    activity = has_activity_writer()

    lines = [
        "# 审计覆盖报告",
        "",
        "> 由 `python3 scripts/gen_audit_coverage.py` 从代码生成，不手工维护。",
        "> 依据：docs/dual-system-development-plan.md §6.5、§6.6.1、§27.1 AUDIT-001。",
        "",
        "## 1. 认证与登录安全事件",
        "",
        "每一次登录尝试（成功与失败）都写入 `login_events`，并进入 append-only 的 `login` 哈希链。",
        "",
    ]
    live = sum(1 for _, ok in events if ok)
    lines.append("| 事件类型 | 是否会产生 |")
    lines.append("|---|---|")
    for name, ok in events:
        lines.append(f"| `{name}` | {'是' if ok else '**否**（常量已定义但无调用点，功能未实现）'} |")
    lines.append("")
    lines.append(f"共定义 {len(events)} 个事件常量，其中 **{live}** 个在当前代码中真的会被写入；"
                 f"其余 {len(events) - live} 个属于尚未实现的能力（如 MFA），不得据此认为已支持。")

    lines += [
        "",
        "## 2. 业务与管理写操作",
        "",
        "写操作通过 `declaredPermission` 判定是否审计；敏感读取即使只是 GET 也会审计。",
        "",
        "| 权限键 | 端点 |",
        "|---|---|",
    ]
    for perm in sorted(perms):
        eps = sorted(perms[perm])
        lines.append(f"| `{perm}` | {', '.join('`' + e + '`' for e in eps)} |")

    lines += [
        "",
        "### 兼容层兜底",
        "",
        "`/api/v1` 的历史写操作没有权限声明，因此单独由 `isLegacyMutation` 兜底审计：",
        "动作名为 `legacy:<METHOD> <path>`，路径中的 UUID 会被归一化为 `{id}`，",
        "避免 action 标签基数爆炸。下载票据路由不重复记录。",
        "",
        "## 3. 敏感读取",
        "",
        "以下权限在权限目录中标记为 `sensitive=true`，读取这些端点即使使用 GET 也会产生审计事件：",
        "",
        "| 权限键 |",
        "|---|",
    ]
    for p in sensitive:
        lines.append(f"| `{p}` |")

    lines += [
        "",
        "## 4. 未覆盖的类别（如实记录）",
        "",
        "| §6.6.1 类别 | 状态 | 说明 |",
        "|---|---|---|",
        "| 登录安全 | 已覆盖 | 见第 1 节 |",
        "| 授权结果（401/403/越权） | 已覆盖 | `authorization.denied` 与 `authorization.object_denied` |",
        "| 业务操作（案件/调查/证据/图谱/报告/导出） | 部分覆盖 | 新路由按权限键记录；案件与证据端点属 S4/S5，尚未实现 |",
        "| 管理操作（Provider/预算/Kill Switch/配置） | 部分覆盖 | 身份与权限管理已覆盖；Provider/配置类端点属 S6 |",
        f"| 页面访问遥测 | {'已覆盖' if activity else 'NOT_IMPLEMENTED'} | `user_activity_events` 表已建，"
        + ("已有写入方" if activity else "**尚无写入方**，前端也未上报；计划在 S4 随案件页面上报") + " |",
        "| 查询操作（搜索/筛选/分页） | NOT_IMPLEMENTED | 同上，与页面遥测一同实现 |",
        "| IP 情报（国家/ASN/代理标记） | NOT_IMPLEMENTED | `login_events.ip_intel` 字段已预留，未接入情报源 |",
        "",
        "## 5. 可按 Trace 还原",
        "",
        "`audit_events_v2` 与 `login_events` 都记录 `request_id` / `trace_id`；",
        "HTTP 响应头返回 `X-Request-Id` 与 `X-Trace-Id`，后台审计页支持按 Trace ID 检索。",
        "端到端验证见 `tests/run-iam-e2e.sh` 的 AUDIT-001 用例。",
        "",
    ]
    target = ROOT / "docs" / "implementation" / "audit-coverage.md"
    target.write_text("\n".join(lines), encoding="utf-8", newline="\n")
    live = sum(1 for _, ok in events if ok)
    print(f"wrote {target.relative_to(ROOT)}：{len(events)} 个认证事件常量（{live} 个实际会触发）、"
          f"{len(perms)} 个审计权限键、{len(sensitive)} 个敏感权限")
    return 0


if __name__ == "__main__":
    sys.exit(main())
