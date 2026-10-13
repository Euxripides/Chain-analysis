#!/usr/bin/env python3
"""从后端路由表生成 user-v2 / admin-v1 的**索引级**契约。

为什么需要生成：这两个分区有 40+ 个端点，手写完整响应模型的工作量很大，
但"没有契约"会让 §21.1 的检查形同虚设。折中做法是先生成一份**准确**的索引：
路径、方法、operationId、所需权限、错误码骨架都是真的，响应体模型留空。

生成的文件会带 `x-contract-level: index-only` 标记；
scripts/check_openapi.py 会统计两个级别的覆盖率，让缺口始终可见，
而不是靠"文件存在"冒充契约完整。

用法：python3 scripts/gen_openapi_index.py
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
API = ROOT / "backend" / "internal" / "api"

REGIONS = {
    "user-v2": ("/api/v2", ["userv2.go"], "用户层系统 API（§21.5）"),
    "admin-v1": (
        "/api/admin/v1",
        [
            "adminv1.go",
            "adminorg.go",
            # Sprint 6：后台路由按领域拆分，生成器必须一并扫描，否则契约会落后于实现。
            "sprint6_identity.go",
            "sprint6_control.go",
            "sprint6_provider.go",
            "sprint6_governance.go",
            "sprint6_obs.go",
            "sprint6_audit_integrity.go",
        ],
        "后台管理 API（§21.6）",
    ),
}


def handler_name_to_operation_id(handler: str, path: str, method: str) -> str:
    name = re.sub(r"^s\.handle", "", handler)
    if not name:
        return "unknown"
    # 共用处理器（如 handleNotImplementedV2）会产生重复的 operationId，
    # 这类情况用路径 + 方法推导，保证每个 operationId 全局唯一（§21.1 CI 要求）。
    if name.startswith("NotImplemented"):
        parts = [p for p in re.split(r"[/{}\-]+", path) if p and p not in ("api", "v2", "admin", "v1")]
        return method.lower() + "".join(p[:1].upper() + p[1:] for p in parts)
    return name[0].lower() + name[1:]


def parse_routes(files: list[str], prefix: str):
    out = []
    for fname in files:
        body = (API / fname).read_text(encoding="utf-8")
        # A registration is one statement, and a statement may span several lines
        # (s.requireAny(...) with a permission slice, for example). Matching a single line
        # silently degraded those endpoints to operationId "unknown" with no permission, so the
        # statement is reassembled until its parentheses balance.
        lines = body.split("\n")
        statements = []
        for i, line in enumerate(lines):
            m = re.match(r'^\s*(?:r|pr)\.(Get|Post|Patch|Delete|Put)\("([^"]+)"\s*,\s*(.*)$', line)
            if not m:
                continue
            tail = m.group(3)
            j = i
            while tail.count("(") > tail.count(")") and j + 1 < len(lines):
                j += 1
                tail += " " + lines[j].strip()
            statements.append((m.group(1).upper(), m.group(2), tail.strip()))
        for method, path, tail in statements:
            perm = re.search(r'require(?:Any)?\(\s*(?:\[\]string\{)?\s*"([a-z0-9:_\-]+)"', tail) or re.search(r'require\("([a-z0-9:_\-]+)"', tail)
            handler = re.search(r"(s\.handle\w+)", tail)
            not_impl = "handleNotImplementedV2" in tail
            out.append(
                {
                    "method": method,
                    "path": prefix + path,
                    "permission": perm.group(1) if perm else "",
                    "handler": handler.group(1) if handler else "",
                    "operationId": handler_name_to_operation_id(handler.group(1), prefix + path, method) if handler else "unknown",
                    "not_implemented": not_impl,
                }
            )
    # 同一路径多方法时按方法排序，保证输出稳定
    out = sorted(out, key=lambda r: (r["path"], r["method"]))
    # operationId 必须全局唯一（§21.1）。同 handler 服务多方法时追加方法名，
    # 避免 YAML 重复 key 或重复 operationId 让后一个方法悄悄消失。
    seen: dict[str, int] = {}
    for r in out:
        base = r["operationId"]
        if base in seen:
            candidate = base + r["method"][0] + r["method"][1:].lower()
            n = 2
            while candidate in seen:
                candidate = f"{base}{r['method'].lower()}{n}"
                n += 1
            r["operationId"] = candidate
        seen[r["operationId"]] = seen.get(r["operationId"], 0) + 1
    return out


def render(name: str, title: str, routes) -> str:
    lines = [
        "openapi: 3.1.0",
        "info:",
        f"  title: {title}",
        "  version: 1.0.0",
        "  description: |",
        "    本文件由 scripts/gen_openapi_index.py 从后端路由表生成，请勿手工编辑。",
        "",
        "    契约级别：**索引级（index-only）**。路径、方法、operationId、所需权限",
        "    与错误码骨架均来自真实代码；业务响应体模型尚未定义（见 §21.1 的后续工作）。",
        "    auth-v1.yaml 是完整级契约，可作为写法参考。",
        "x-contract-level: index-only",
        "x-generated-by: scripts/gen_openapi_index.py",
        # Sprint 7 API-layer guards. They are enforced by the middleware for every operation
        # in this file, so the contract must make them visible; the authoritative contract
        # (allowlist, audit, fail-closed rules) lives in common/errors.yaml.
        "x-must-change-password-guard:",
        "  refused_code: AUTHN_PASSWORD_CHANGE_REQUIRED",
        "  refused_status: 403",
        "  see: './common/errors.yaml#/MustChangePasswordGuard'",
        "x-session-workspace-scope-guard:",
        "  refused_code: AUTHZ_WORKSPACE_SCOPE_STALE",
        "  refused_status: 409",
        "  see: './common/errors.yaml#/SessionWorkspaceScopeGuard'",
        "servers:",
        "  - url: /",
        "security:",
        "  - sessionCookie: []",
        "components:",
        "  securitySchemes:",
        "    sessionCookie:",
        "      type: apiKey",
        "      in: cookie",
        "      name: cs_user_session",
        "paths:",
    ]
    grouped: dict[str, list] = {}
    for r in routes:
        grouped.setdefault(r["path"], []).append(r)
    for path, methods in grouped.items():
        lines.append(f"  {path}:")
        for r in methods:
            lines.append(f"    {r['method'].lower()}:")
            lines.append(f"      operationId: {r['operationId']}")
            lines.append(f"      tags: [{name}]")
            if r["permission"]:
                # 权限键是路由与授权引擎的契约，必须在契约里可见（§28.5 验收要求 100% 覆盖）。
                lines.append(f"      x-required-permission: {r['permission']}")
            lines.append("      responses:")
            if r["not_implemented"]:
                lines.append("        '501':")
                lines.append("          description: 该能力尚未实现（NOT_IMPLEMENTED）")
                lines.append("          content:")
                lines.append("            application/json:")
                lines.append("              schema: { $ref: './common/errors.yaml#/ErrorEnvelope' }")
            else:
                lines.append("        '200':")
                lines.append("          description: 成功（响应体模型待补充）")
                lines.append("          content:")
                lines.append("            application/json:")
                lines.append("              schema: { $ref: './common/envelope.yaml#/Envelope' }")
            lines.append("        '401':")
            lines.append("          description: 未认证或会话失效")
            lines.append("          content:")
            lines.append("            application/json:")
            lines.append("              schema: { $ref: './common/errors.yaml#/ErrorEnvelope' }")
            lines.append("        '403':")
            lines.append("          description: 缺少所需权限")
            lines.append("          content:")
            lines.append("            application/json:")
            lines.append("              schema: { $ref: './common/errors.yaml#/ErrorEnvelope' }")
    result = "\n".join(lines) + "\n"
    if name == "user-v2":
        import yaml
        from openapi_redesign_contract import enrich
        return yaml.safe_dump(enrich(yaml.safe_load(result)), allow_unicode=True, sort_keys=False)
    return result


def main() -> int:
    total = 0
    for name, (prefix, files, title) in REGIONS.items():
        routes = parse_routes(files, prefix)
        (ROOT / "openapi" / f"{name}.yaml").write_text(render(name, title, routes), encoding="utf-8", newline="\n")
        total += len(routes)
        print(f"  openapi/{name}.yaml  <- {len(routes)} 个端点")
    print(f"共生成 {total} 个端点的索引级契约。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
