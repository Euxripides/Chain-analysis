#!/usr/bin/env python3
"""把 Trust Wallet assets 的图标索引**按需子集**导进 token_logo_assets。

为什么不是"完整下载后导入"：本机到 GitHub 批量端点（codeload / git fetch）实测吞吐为 0
（2026-09-20：zip 卡在 16MB、sparse clone 卡在 206MB），但**单文件 raw 与 api.github.com 可用**。
所以这里用 Contents API 列出各链 assets 目录（每页 1000，约 1-3 次/链），
只对**我们库里真实出现过的代币**取 info.json，拼出 raw 的 logo.png URL 落目录表。

完整导入路径不受影响：拿到 zip/克隆后直接跑
    go run ./cmd/tokenindex -trustwallet <解压目录>
即可把全量目录 upsert（同一 source，主键相同）。

用法：
    python3 scripts/logo/tw-subset-import.py [--chains bsc,eth,tron,solana] [--limit-per-chain N] [--dry-run]
"""
from __future__ import annotations

import argparse
import concurrent.futures as futures
import json
import os
import re
import subprocess
import sys
import urllib.parse
import urllib.request

REPO = "trustwallet/assets"
API = "https://api.github.com"
RAW = "https://raw.githubusercontent.com/" + REPO + "/master"
TW_CHAINS = {
    "eth": "ethereum",
    "bsc": "smartchain",
    "polygon": "polygon",
    "arbitrum": "arbitrum",
    "base": "base",
    "optimism": "optimism",
    "avalanche": "avalanchec",
    "solana": "solana",
    "tron": "tron",
}
EVM_CHAINS = {"eth", "bsc", "polygon", "arbitrum", "base", "optimism", "avalanche"}


def load_env(path: str) -> dict:
    env = {}
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, value = line.split("=", 1)
            env[key.strip()] = value.strip()
    return env


def clickhouse_query(env: dict, sql: str) -> str:
    match = re.match(r"clickhouse://([^:]+):([^@]+)@([^:/]+)(?::\d+)?/(.+)", env["CLICKHOUSE_URL"])
    user, password, host, db = match.group(1), urllib.parse.unquote(match.group(2)), match.group(3), match.group(4)
    url = f"http://{host}:8123/?user={user}&password={urllib.parse.quote(password)}&database={db}"
    req = urllib.request.Request(url, data=sql.encode(), method="POST")
    with urllib.request.urlopen(req, timeout=120) as resp:
        return resp.read().decode()


def gh_json(url: str, token: str | None) -> object:
    req = urllib.request.Request(url)
    req.add_header("accept", "application/vnd.github+json")
    req.add_header("user-agent", "blockchain-platform-logo-index")
    if token:
        req.add_header("authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=45) as resp:
        return json.loads(resp.read().decode())


def raw_text(url: str) -> str | None:
    try:
        req = urllib.request.Request(url, headers={"user-agent": "blockchain-platform-logo-index"})
        with urllib.request.urlopen(req, timeout=45) as resp:
            return resp.read().decode("utf-8", "replace")
    except Exception:
        return None


def list_assets_dirs(tw_chain: str, token: str | None) -> dict:
    """返回 {小写目录名: 真实目录名}。"""
    out: dict[str, str] = {}
    page = 1
    while True:
        url = f"{API}/repos/{REPO}/contents/blockchains/{tw_chain}/assets?per_page=1000&page={page}"
        try:
            items = gh_json(url, token)
        except Exception as exc:  # 目录不存在 / 限流
            print(f"  ! 列目录失败 {tw_chain} page={page}: {exc}", flush=True)
            break
        if not isinstance(items, list) or not items:
            break
        for item in items:
            if item.get("type") == "dir":
                out[item["name"].lower()] = item["name"]
        if len(items) < 1000:
            break
        page += 1
    return out


def fetch_info(tw_chain: str, dir_name: str) -> dict | None:
    text = raw_text(f"{RAW}/blockchains/{tw_chain}/assets/{dir_name}/info.json")
    if not text:
        return None
    try:
        return json.loads(text)
    except Exception:
        return None


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--chains", default=",".join(TW_CHAINS))
    parser.add_argument("--limit-per-chain", type=int, default=0, help="0=不限")
    parser.add_argument("--workers", type=int, default=16)
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--env", default=os.path.join(os.path.dirname(__file__), "..", "..", "deploy", ".env"))
    args = parser.parse_args()

    env = load_env(os.path.abspath(args.env))
    token = os.environ.get("GITHUB_TOKEN")
    wanted_chains = [c.strip() for c in args.chains.split(",") if c.strip()]

    # 1) 我们库里真实出现过的代币（按链取形状合法的地址）
    universe: dict[str, set[str]] = {}
    sql = "SELECT DISTINCT chain, token_address FROM blockchain.token_transfers WHERE token_address != '' FORMAT TSV"
    for line in clickhouse_query(env, sql).splitlines():
        if "\t" not in line:
            continue
        chain, address = line.split("\t", 1)
        if chain not in wanted_chains:
            continue
        address = address.strip()
        if chain in EVM_CHAINS:
            if not re.fullmatch(r"0x[0-9a-fA-F]{40}", address):
                continue
        universe.setdefault(chain, set()).add(address.lower() if chain in EVM_CHAINS else address)
    for chain in wanted_chains:
        print(f"库内代币 {chain}: {len(universe.get(chain, ()))}")

    rows: list[tuple] = []
    for chain in wanted_chains:
        addresses = universe.get(chain, set())
        if not addresses:
            continue
        tw_chain = TW_CHAINS[chain]
        print(f"[{chain}] 列 Trust Wallet 目录 {tw_chain} …", flush=True)
        dirs = list_assets_dirs(tw_chain, token)
        print(f"[{chain}] Trust Wallet 目录 {len(dirs)} 个", flush=True)
        matched = [(addr, dirs[addr]) for addr in sorted(addresses) if addr in dirs]
        if args.limit_per_chain > 0:
            matched = matched[: args.limit_per_chain]
        print(f"[{chain}] 命中 {len(matched)} 个", flush=True)
        if not matched:
            continue
        with futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
            infos = list(pool.map(lambda item: fetch_info(tw_chain, item[1]), matched))
        for (addr, dir_name), info in zip(matched, infos):
            symbol = (info or {}).get("symbol") or ""
            name = (info or {}).get("name") or ""
            token_type = (info or {}).get("type") or ""
            decimals = (info or {}).get("decimals") or None
            status = (info or {}).get("status") or ""
            logo_url = f"{RAW}/blockchains/{tw_chain}/assets/{dir_name}/logo.png"
            rows.append((chain, addr, tw_chain, dir_name, symbol, name, token_type, decimals, status, logo_url))

    print(f"待写入目录行：{len(rows)}")
    if args.dry_run:
        for row in rows[:5]:
            print("  ", row)
        return 0
    if not rows:
        return 0

    # 2) 落库（走 psql COPY，幂等 upsert）
    pg = re.match(r"postgres(?:ql)?://([^:]+):([^@]+)@([^:/]+)(?::(\d+))?/([^?]+)", env.get("POSTGRES_URL") or env["DATABASE_URL"])
    pg_env = dict(os.environ, PGPASSWORD=urllib.parse.unquote(pg.group(2)))
    psql = ["psql", "-h", pg.group(3), "-p", pg.group(4) or "5432", "-U", pg.group(1), "-d", pg.group(5), "-v", "ON_ERROR_STOP=1", "-q"]
    csv_lines = "\n".join(
        "\t".join(
            [
                "trustwallet",
                r[0],
                r[1],
                r[2],
                r[3],
                r[4],
                r[5],
                r[6],
                "" if r[7] is None else str(r[7]),
                r[8],
                r[9],
            ]
        )
        for r in rows
    )
    copy_sql = (
        "CREATE TEMP TABLE tw_stage (source text, chain text, normalized_address text, source_chain text, "
        "source_address text, symbol text, name text, token_type text, decimals text, status text, logo_url text);\n"
        "\\copy tw_stage FROM STDIN\n"
        + csv_lines
        + "\n\\.\n"
        "INSERT INTO token_logo_assets (source, chain, normalized_address, source_chain, source_address, symbol, name, token_type, decimals, status, logo_url, updated_at)\n"
        "SELECT source, chain, normalized_address, source_chain, source_address, NULLIF(symbol, ''), NULLIF(name, ''), NULLIF(token_type, ''), NULLIF(decimals, '')::int, NULLIF(status, ''), logo_url, now() FROM tw_stage\n"
        "ON CONFLICT (source, chain, normalized_address) DO UPDATE SET\n"
        "  source_chain = EXCLUDED.source_chain, source_address = EXCLUDED.source_address,\n"
        "  symbol = COALESCE(EXCLUDED.symbol, token_logo_assets.symbol),\n"
        "  name = COALESCE(EXCLUDED.name, token_logo_assets.name),\n"
        "  token_type = COALESCE(EXCLUDED.token_type, token_logo_assets.token_type),\n"
        "  decimals = COALESCE(EXCLUDED.decimals, token_logo_assets.decimals),\n"
        "  status = COALESCE(EXCLUDED.status, token_logo_assets.status),\n"
        "  logo_url = EXCLUDED.logo_url, updated_at = now();\n"
    )
    proc = subprocess.run(psql, input=copy_sql, text=True, capture_output=True, env=pg_env)
    if proc.returncode != 0:
        print(proc.stdout[-800:])
        print(proc.stderr[-800:], file=sys.stderr)
        return 1
    print(f"已写入 token_logo_assets: {len(rows)} 行")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
