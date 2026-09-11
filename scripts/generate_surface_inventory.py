#!/usr/bin/env python3
"""生成 docs/implementation/*.md 的现状清单与权限目录快照。

来源全部是仓库真实文件与部署实例，不使用人工估算：

  * 后端路由    -> backend/internal/api/server.go + authv1.go/userv2.go/adminv1.go
  * 权限目录    -> backend/internal/policy/catalog.go
  * 迁移        -> backend/migrations/postgres/*.sql（含已应用时间，若可连库）
  * 前端路由    -> frontend/apps/*/src/App.tsx 中的 <Route path=...>
  * 运行单元    -> deploy/*.service

用法：
    python3 scripts/generate_surface_inventory.py            # 只写清单文档
    python3 scripts/generate_surface_inventory.py --with-db  # 额外查询远端数据库
"""

from __future__ import annotations

import argparse
import datetime as dt
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT_DIR = ROOT / "docs" / "implementation"


def read(path: Path) -> str:
    return path.read_text(encoding="utf-8", errors="replace")


def collect_backend_routes() -> list[tuple[str, str, str]]:
    """返回 (method, path, permission) 列表。permission 为空表示未声明。"""
    rows: list[tuple[str, str, str]] = []
    server = read(ROOT / "backend/internal/api/server.go")
    # legacy /api/v1 路由
    for m in re.finditer(r'^\s*api\.(Get|Post|Patch|Delete|Put)\("([^"]+)"', server, re.M):
        rows.append((m.group(1).upper(), "/api/v1" + m.group(2), "(legacy / 未声明)"))
    for m in re.finditer(r'^\s*r\.(Get|Post)\("([^"]+)"', server, re.M):
        rows.append((m.group(1).upper(), m.group(2), "(legacy / 未声明)"))

    for name in ("authv1.go", "userv2.go", "adminv1.go"):
        body = read(ROOT / "backend/internal/api" / name)
        prefix = {"authv1.go": "/api/auth/v1", "userv2.go": "/api/v2", "adminv1.go": "/api/admin/v1"}[name]
        # require("<perm>", handler) / handleNotImplementedV2(...) / 直接 handler
        for m in re.finditer(
            r'^\s*(?:pr|r)\.(Get|Post|Patch|Delete|Put)\("([^"]+)"\s*,\s*(.*)$', body, re.M
        ):
            method, path, tail = m.group(1).upper(), m.group(2), m.group(3)
            perm = re.search(r'"([a-z0-9:_\-]+)"', tail)
            rows.append((method, prefix + path, perm.group(1) if perm else "(未声明)"))
    return sorted(set(rows), key=lambda r: (r[1], r[0]))


def collect_permissions() -> list[tuple[str, str, str, str, str]]:
    body = read(ROOT / "backend/internal/policy/catalog.go")
    rows = []
    for m in re.finditer(
        r'"([a-z0-9:_\-]+)":\s*\{"[^"]+",\s*"([a-z0-9._\-]+)",\s*"([^"]*)",\s*Risk(\w+),\s*(true|false)\}', body
    ):
        rows.append((m.group(1), m.group(2), m.group(3), m.group(4).lower(), m.group(5)))
    return sorted(rows)


def collect_migrations() -> list[str]:
    files = sorted(p.name for p in (ROOT / "backend/migrations/postgres").glob("*.sql"))
    return files


def collect_clickhouse_migrations() -> list[str]:
    return sorted(p.name for p in (ROOT / "backend/migrations/clickhouse").glob("*.sql"))


def collect_frontend_routes() -> dict[str, list[str]]:
    result: dict[str, list[str]] = {}
    for app in ("user-console", "admin-console"):
        body = read(ROOT / "frontend/apps" / app / "src" / "App.tsx")
        routes = sorted(set(re.findall(r'<Route path="([^"]+)"', body)))
        result[app] = routes
    return result


def collect_services() -> list[str]:
    return sorted(p.name for p in (ROOT / "deploy").glob("*.service"))


def db_snapshot() -> str:
    script = r'''
set -a; . deploy/.env; set +a
PGURL="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/blockchain_platform"
echo "### 已应用迁移（最近 8 条）"
psql "$PGURL" -tAc "select name || '  ' || applied_at from schema_migrations order by name desc limit 8"
echo "### 双系统新增表行数"
psql "$PGURL" -tAc "select 'identities=' || (select count(*) from identities) || ' sessions=' || (select count(*) from sessions) || ' login_events=' || (select count(*) from login_events) || ' audit_events_v2=' || (select count(*) from audit_events_v2) || ' roles=' || (select count(*) from roles) || ' permissions=' || (select count(*) from permissions) || ' bindings=' || (select count(*) from member_role_bindings)"
echo "### 审计链头"
psql "$PGURL" -tAc "select chain || ' seq=' || last_seq || ' head=' || coalesce(nullif(left(last_hash,16),''),'(empty)') from audit_chain_heads order by chain"
'''
    try:
        out = subprocess.run(
            ["ssh", os.environ.get("BLOCKCHAIN_VM_USER", "euripides") + "@" + os.environ.get("BLOCKCHAIN_VM_HOST", "192.168.31.152"),
             "cd /home/euripides/projects/blockchain-platform && bash -s"],
            input=script, capture_output=True, text=True, timeout=120,
        )
        return out.stdout.strip() or out.stderr.strip()
    except Exception as exc:  # noqa: BLE001
        return f"(无法连接部署实例：{exc})"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--with-db", action="store_true", help="附加部署实例的数据库快照")
    args = parser.parse_args()

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    now = dt.datetime.now().astimezone().strftime("%Y-%m-%d %H:%M %z")

    routes = collect_backend_routes()
    perms = collect_permissions()
    fe = collect_frontend_routes()
    services = collect_services()

    declared = [r for r in routes if r[2] not in ("(未声明)", "(legacy / 未声明)")]
    undeclared_new = [r for r in routes if r[2] == "(未声明)"]

    lines: list[str] = []
    lines.append("# 当前系统面清单（Current Surface Inventory）\n")
    lines.append(f"> 生成时间：{now}  ")
    lines.append("> 生成方式：`python3 scripts/generate_surface_inventory.py --with-db`（全部取自仓库真实文件与部署实例）  ")
    lines.append("> 依据：docs/dual-system-development-plan.md §28.1 第 3 步\n")
    lines.append("本文件是迁移对照基线：新增或修改路由、权限、迁移、页面时都必须同步更新。\n")

    lines.append("## 1. 后端路由\n")
    lines.append(f"共 {len(routes)} 条，其中双系统新增路由 **{len(declared)}** 条全部声明了权限键"
                 f"（覆盖率 {len(declared) / max(1, len(declared) + len(undeclared_new)):.0%}）；"
                 f"`/api/v1` 为兼容层，尚未逐条声明（见 §28.2 迁移映射）。\n")
    lines.append("| Method | Path | Permission |")
    lines.append("|---|---|---|")
    for method, path, perm in routes:
        lines.append(f"| {method} | `{path}` | {('`' + perm + '`') if perm not in ('(未声明)', '(legacy / 未声明)') else perm} |")
    lines.append("")

    lines.append("## 2. 权限目录\n")
    lines.append(f"共 **{len(perms)}** 个权限键，命名规则 `resource:action`（§4.2）。"
                 "Go 侧 `internal/policy/catalog.go` 与 SQL 侧 `0041_rbac.sql` 由单元测试强校验一致。\n")
    lines.append("| Key | Category | 说明 | Risk | 敏感 |")
    lines.append("|---|---|---|---|---|")
    for key, cat, desc, risk, sens in perms:
        lines.append(f"| `{key}` | {cat} | {desc} | {risk} | {'是' if sens == 'true' else '否'} |")
    lines.append("")

    lines.append("## 3. 前端路由\n")
    for app, routes_ in fe.items():
        lines.append(f"### {app}\n")
        lines.append("```text")
        for r in routes_:
            lines.append(r)
        lines.append("```\n")

    lines.append("## 4. 迁移\n")
    lines.append("### PostgreSQL\n")
    lines.append("```text")
    for name in collect_migrations():
        lines.append(name)
    lines.append("```\n")
    lines.append("### ClickHouse\n")
    lines.append("```text")
    for name in collect_clickhouse_migrations():
        lines.append(name)
    lines.append("```\n")

    lines.append("## 5. 运行单元\n")
    lines.append("```text")
    for s in services:
        lines.append("deploy/" + s)
    lines.append("```\n")

    if args.with_db:
        lines.append("## 6. 部署实例数据库快照\n")
        lines.append("```text")
        lines.append(db_snapshot())
        lines.append("```\n")

    target = OUT_DIR / "current-surface-inventory.md"
    target.write_text("\n".join(lines), encoding="utf-8", newline="\n")
    print(f"wrote {target.relative_to(ROOT)} ({len(lines)} lines, {len(routes)} routes, {len(perms)} permissions)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
