#!/usr/bin/env python3
"""OpenAPI 契约守卫（§21.1 / §28.3 S1-API-01）。

CI 必须验证：
  1. 所有契约文件可解析；
  2. operationId 在文件内唯一；
  3. 每个已文档化的路径都真实存在于后端路由表中（防止"文档写了但没实现"）；
  4. 每个需要会话的端点都声明了错误响应模型（§21.1"响应错误模型完整"）；
  5. 契约中引用的错误码都在 common/errors.yaml 的 KnownErrorCodes 里登记。

反过来也会检查：auth/v2/admin 下**已实现**的端点必须有契约覆盖
（允许登记在 ALLOWLIST 里并写明原因，不允许默默漏掉）。

用法：
    python3 scripts/check_openapi.py            # 本地校验
    python3 scripts/check_openapi.py --json     # 机器可读输出
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
OPENAPI_DIR = ROOT / "openapi"
API_DIR = ROOT / "backend" / "internal" / "api"

# 已实现但暂未写契约的端点。新增条目必须写明原因与目标 Sprint，否则视为遗漏。
ALLOWLIST: dict[str, str] = {
    # Sprint 6 已把原先的 501 占位端点全部落地并写入契约，因此这里不再保留管理侧豁免。
    "/api/v2/notifications": "占位端点，返回 501，S4",
    "/api/v2/notifications/{id}/read": "占位端点，返回 501，S4",
    "/api/v2/notifications/read-all": "占位端点，返回 501，S4",
}


def load_specs() -> tuple[dict[str, dict], list[str]]:
    """返回 {文件: 解析后的文档} 与错误列表。"""
    specs: dict[str, dict] = {}
    errors: list[str] = []
    for path in sorted(OPENAPI_DIR.rglob("*.yaml")):
        if path.name in {"errors.yaml", "envelope.yaml", "pagination.yaml"}:
            continue
        try:
            doc = yaml.safe_load(path.read_text(encoding="utf-8"))
        except yaml.YAMLError as exc:
            errors.append(f"{path.relative_to(ROOT)}: YAML 解析失败：{exc}")
            continue
        if not isinstance(doc, dict) or "paths" not in doc:
            errors.append(f"{path.relative_to(ROOT)}: 不是有效的 OpenAPI 文档（缺少 paths）")
            continue
        specs[str(path.relative_to(ROOT))] = doc
    return specs, errors


def collect_router_paths() -> dict[str, set[str]]:
    """从 Go 源码提取已注册的 (路径 -> 方法集合)，覆盖 auth / v2 / admin 三个分区。"""
    routes: dict[str, set[str]] = {}
    files = {
        "auth": API_DIR / "authv1.go",
        "v2": API_DIR / "userv2.go",
        "admin": API_DIR / "adminv1.go",
        "adminorg": API_DIR / "adminorg.go",
        # Sprint 6 起，后台路由按领域拆到 sprint6_*.go；这些文件同样挂在 /api/admin/v1 下。
        "adminS6Identity": API_DIR / "sprint6_identity.go",
        "adminS6Control": API_DIR / "sprint6_control.go",
        "adminS6Provider": API_DIR / "sprint6_provider.go",
        "adminS6Governance": API_DIR / "sprint6_governance.go",
        "adminS6Obs": API_DIR / "sprint6_obs.go",
        "adminS6AuditIntegrity": API_DIR / "sprint6_audit_integrity.go",
    }
    prefixes = {
        "auth": "/api/auth/v1",
        "v2": "/api/v2",
        "admin": "/api/admin/v1",
        "adminorg": "/api/admin/v1",
        "adminS6Identity": "/api/admin/v1",
        "adminS6Control": "/api/admin/v1",
        "adminS6Provider": "/api/admin/v1",
        "adminS6Governance": "/api/admin/v1",
        "adminS6Obs": "/api/admin/v1",
        "adminS6AuditIntegrity": "/api/admin/v1",
    }

    def add(full: str, method: str) -> None:
        # 把 chi 的 {id} 参数统一成 OpenAPI 风格
        routes.setdefault(full, set()).add(method)

    for key, path in files.items():
        if not path.exists():
            continue
        body = path.read_text(encoding="utf-8")
        for m in re.finditer(r'^\s*(?:r|pr|api|ar|ur)\.(Get|Post|Patch|Delete|Put)\("([^"]+)"', body, re.M):
            add(prefixes[key] + m.group(2), m.group(1).upper())
        # 分组路由 r.Route("/xxx", ...) 由调用方拼接，这里只处理额外挂载的组织路由
    extra = API_DIR / "adminv1.go"
    if extra.exists():
        body = extra.read_text(encoding="utf-8")
        if "registerAdminOrgRoutes" in body:
            for m in re.finditer(r'^\s*r\.(Get|Post|Patch|Delete|Put)\("([^"]+)"', (API_DIR / "adminorg.go").read_text(encoding="utf-8"), re.M):
                add("/api/admin/v1" + m.group(2), m.group(1).upper())
    return routes


def normalize(path: str) -> str:
    return re.sub(r"\{[^}]+\}", "{param}", path)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()

    specs, errors = load_specs()
    documented: dict[str, set[str]] = {}
    full_paths: set[str] = set()
    index_only_paths: set[str] = set()
    for name, doc in specs.items():
        level = doc.get("x-contract-level", "full")
        bucket = index_only_paths if level == "index-only" else full_paths
        seen_ids: set[str] = set()
        for path, item in (doc.get("paths") or {}).items():
            for method, op in item.items():
                if not isinstance(op, dict):
                    continue
                method = method.upper()
                if method not in {"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}:
                    continue
                op_id = op.get("operationId")
                if not op_id:
                    errors.append(f"{name}: {method} {path} 缺少 operationId")
                elif op_id in seen_ids:
                    errors.append(f"{name}: operationId 重复：{op_id}")
                else:
                    seen_ids.add(op_id)

                responses = op.get("responses") or {}
                # 需要会话的端点必须声明 401/403 之一，保证错误模型完整。
                needs_session = not (op.get("security") == [])
                if needs_session and not ({"401", "403"} & set(responses.keys())):
                    errors.append(f"{name}: {method} {path} 需要会话但未声明 401/403 响应模型")
                for code, resp in responses.items():
                    if str(code).startswith(("4", "5")) and isinstance(resp, dict) and "content" not in resp:
                        errors.append(f"{name}: {method} {path} 的 {code} 响应缺少 content（错误模型）")
                documented.setdefault(normalize(path), set()).add(method)
                bucket.add(normalize(path))

    router = {normalize(k): v for k, v in collect_router_paths().items()}

    # 方向一：契约里有、路由里没有
    for path, methods in documented.items():
        if path not in router:
            errors.append(f"契约声明的路径在后端路由中不存在：{path}")
            continue
        for method in methods:
            if method not in router[path]:
                errors.append(f"契约声明的 {method} {path} 在后端路由中不存在")

    # 方向二：路由里有、契约里没有
    for path, methods in router.items():
        if path in documented or path in ALLOWLIST:
            continue
        errors.append(f"已实现的端点缺少契约：{sorted(methods)} {path}（如确属占位，请登记进 ALLOWLIST）")

    known_codes: set[str] = set()
    err_doc = yaml.safe_load((OPENAPI_DIR / "common" / "errors.yaml").read_text(encoding="utf-8"))
    known_codes = set((err_doc.get("KnownErrorCodes") or {}).keys())

    # 方向三：契约里引用的错误码必须已登记
    code_re = re.compile(r"\b(?:AUTHN|AUTHZ|VALIDATION|RESOURCE|CONFLICT|RATE_LIMIT|PROVIDER|DATA|INTERNAL|NOT_IMPLEMENTED)[A-Z_]*\b")
    for name, doc in specs.items():
        raw = (ROOT / name).read_text(encoding="utf-8")
        for m in set(code_re.findall(raw)):
            if m in {"AUTHN", "AUTHZ", "VALIDATION", "RESOURCE", "CONFLICT", "RATE_LIMIT", "INTERNAL"}:
                continue
            if m not in known_codes:
                errors.append(f"{name}: 引用了未登记的错误码 {m}（请加入 common/errors.yaml#/KnownErrorCodes）")

    summary = {
        "spec_files": sorted(specs.keys()),
        "documented_paths": len(documented),
        "fully_specified_paths": len(full_paths),
        "index_only_paths": len(index_only_paths),
        "router_paths": len(router),
        "allowlisted": len(ALLOWLIST),
        "errors": errors,
    }
    if args.json:
        print(json.dumps(summary, ensure_ascii=False, indent=2))
    else:
        print("OpenAPI 契约检查")
        print(f"  契约文件      : {', '.join(summary['spec_files']) or '（无）'}")
        print(f"  契约路径      : {summary['documented_paths']}"
              f"（完整级 {summary['fully_specified_paths']} / 索引级 {summary['index_only_paths']}）")
        print(f"  后端路由      : {summary['router_paths']}")
        print(f"  允许豁免      : {summary['allowlisted']}（占位端点）")
        if errors:
            print("\n发现以下问题：")
            for e in errors:
                print("  - " + e)
        else:
            print("\n契约与实现一致，错误模型完整。")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
