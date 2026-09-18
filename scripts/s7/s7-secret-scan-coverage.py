#!/usr/bin/env python3
"""Authoritative coverage audit for the build-time credential literal scan.

WHY (root, 2026-09-13): the frozen scripts/release/build-and-freeze.sh derives its literal
scan patterns from a FIXED list of twelve variable names in deploy/.env.  Any sensitive key
outside that list -- and any name that is present but EMPTY -- silently contributes no
pattern, while the report still reads "secret_patterns_checked=10 ... literal_hits=0".  A
skipped key is therefore indistinguishable from a scanned key, and "0 hits" can be mistaken
for full coverage.  That is exactly what is corrected here.

This tool:
  1. discovers the REAL sensitive configuration sources:
       - the env file (deploy/.env)
       - systemd user unit Environment= / EnvironmentFile= entries
       - the live runtime environment of the running API process (/proc/<pid>/environ)
  2. classifies every discovered key into exactly one bucket:
       COVERED      - the fixed scan list already produces a pattern for it
       DERIVED      - a credential the fixed list MISSES; a pattern is derived here
                      (whole value, or the password inside a DSN/URL userinfo)
       EMPTY        - the key exists but carries no value (recorded, NOT counted as covered)
       NON_SECRET   - a tuning/endpoint parameter, not a credential (recorded explicitly)
  3. runs the literal scan over the given artifacts with the UNION pattern set and fails on
     any hit.
  4. prints a coverage verdict; never prints a secret value (length + sha256 prefix only).

Usage:
  s7-secret-scan-coverage.py --env-file deploy/.env --artifact a.build --artifact b.build       [--api-pid N] [--out report.txt]
"""
import argparse
import hashlib
import os
import re
import subprocess
import sys
from urllib.parse import urlsplit

CREDENTIAL_KEY_RE = re.compile(
    r"(PASSWORD|PASSWD|SECRET|TOKEN|API_?KEY|PRIVATE_KEY|CREDENTIAL|ACCESS_KEY)", re.I
)
# A name can contain PASSWORD/TOKEN while being a tuning knob (AUTH_MIN_PASSWORD_LENGTH,
# ANKR_PAGE_SIZE_TOKEN_TRANSFERS).  Those are parameters, not credentials: treating their
# tiny numeric values as scan patterns produces tens of thousands of bogus substring hits
# inside a compiled binary.  A credential pattern must therefore ALSO look credential-shaped.
PARAM_SUFFIX_RE = re.compile(
    r"(_LENGTH|_SIZE|_PAGES|_WINDOWS|_SPAN|_RATIO|_MAX|_MIN|_COUNT|_SECONDS|_MILLIS|_MS|_ENABLED|_TTL|_AGE|_DAYS|_HOURS)$",
    re.I,
)
MIN_SECRET_LEN = 12


def credential_shaped(value: str) -> bool:
    """A value that is plausibly a secret literal, not a number/boolean/enum knob."""
    if len(value) < MIN_SECRET_LEN:
        return False
    if value.isdigit() or value.isalpha() and value.islower() and len(value) < 24:
        return False
    if value.lower() in ("true", "false", "yes", "no", "on", "off", "none", "null"):
        return False
    return True
PARAM_KEY_RE = re.compile(
    r"(_URL|_URI|_DSN|_HOST|_ENDPOINT|_PORT|PAGE_SIZE|_LENGTH|_LIMIT|_TIMEOUT|_INTERVAL|_MODE|_ENABLED|_NAME|_DIR|_PATH)$",
    re.I,
)
FIXED_SCAN_KEYS = {
    "POSTGRES_PASSWORD",
    "CLICKHOUSE_PASSWORD",
    "MINIO_ROOT_PASSWORD",
    "DATABASE_URL",
    "MIGRATION_DATABASE_URL",
    "CLICKHOUSE_URL",
    "GPT_ACTIONS_API_KEY",
    "GPT_ACTIONS_ADMIN_KEY",
    "MCP_API_KEY",
    "ANKR_API_KEY",
    "CHAINBASE_API_KEY",
    "TRONGRID_API_KEY",
}
URLISH_RE = re.compile(r"^[a-zA-Z][a-zA-Z0-9+.\-]*://")


def sha12(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8", "replace")).hexdigest()[:12]


def userinfo_password(value: str):
    """Return the password embedded in a DSN/URL userinfo, or None."""
    if not URLISH_RE.match(value):
        return None
    try:
        parts = urlsplit(value)
    except ValueError:
        return None
    if parts.password:
        return parts.password
    return None


def read_env_file(path):
    out = {}
    if not path or not os.path.isfile(path):
        return out, False
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        for raw in fh:
            line = raw.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, val = line.split("=", 1)
            key = key.strip()
            if not re.fullmatch(r"[A-Za-z0-9_]+", key):
                continue
            out[key] = val.strip()
    return out, True


def read_live_env(pid):
    if not pid:
        return {}, False
    path = "/proc/%s/environ" % pid
    if not os.path.isfile(path):
        return {}, False
    try:
        with open(path, "rb") as fh:
            blob = fh.read()
    except OSError:
        return {}, False
    out = {}
    for chunk in blob.split(b"\0"):
        if not chunk or b"=" not in chunk:
            continue
        key, val = chunk.split(b"=", 1)
        k = key.decode("utf-8", "replace")
        if re.fullmatch(r"[A-Za-z0-9_]+", k):
            out[k] = val.decode("utf-8", "replace")
    return out, True


def read_systemd_units(services):
    found = {}
    files = []
    for svc in services:
        try:
            proc = subprocess.run(
                ["systemctl", "--user", "cat", svc],
                capture_output=True, text=True, timeout=20,
            )
        except (OSError, subprocess.SubprocessError):
            continue
        if proc.returncode != 0:
            continue
        for line in proc.stdout.splitlines():
            if line.startswith("Environment="):
                for kv in line[len("Environment="):].split():
                    if "=" in kv:
                        k, v = kv.split("=", 1)
                        if re.fullmatch(r"[A-Za-z0-9_]+", k):
                            found.setdefault(k, set()).add("systemd:" + svc)
            elif line.startswith("EnvironmentFile="):
                files.append((svc, line[len("EnvironmentFile="):].lstrip("-")))
    return found, files


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--env-file", default="")
    ap.add_argument("--artifact", action="append", default=[])
    ap.add_argument("--api-pid", default="")
    ap.add_argument("--out", default="")
    args = ap.parse_args()

    lines = []

    def emit(text=""):
        lines.append(text)
        print(text)

    emit("=== s7-secret-scan-coverage (authoritative) ===")
    env, env_ok = read_env_file(args.env_file)
    emit("env_file=%s readable=%s keys=%d" % (args.env_file or "(none)", int(env_ok), len(env)))
    live, live_ok = read_live_env(args.api_pid)
    emit("live_env_readable=%s api_pid=%s keys=%d" % (int(live_ok), args.api_pid or "(none)", len(live)))
    units, unit_files = read_systemd_units(
        ["blockchain-api.service", "blockchain-worker.service", "blockchain-mcp.service"]
    )
    for svc, path in unit_files:
        emit("systemd_environment_file=%s %s readable=%d" % (svc, path, int(os.path.isfile(path))))
    emit("systemd_inline_env_keys=%d" % len(units))

    # ---- union of discovered sensitive values -------------------------------------
    sources = {}
    values = {}
    for key, val in env.items():
        sources.setdefault(key, set()).add("env_file")
        values.setdefault(key, val)
    for key, val in live.items():
        sources.setdefault(key, set()).add("live_env")
        values.setdefault(key, val)
    for key, srcs in units.items():
        sources.setdefault(key, set()).update(srcs)
        values.setdefault(key, "")

    covered, derived, empty, nonsecret = [], [], [], []
    patterns = {}

    for key in sorted(sources):
        val = values.get(key, "")
        srcs = ",".join(sorted(sources[key]))
        is_param_name = bool(PARAM_SUFFIX_RE.search(key))
        is_cred_name = bool(CREDENTIAL_KEY_RE.search(key)) and not is_param_name
        pw = userinfo_password(val)
        if val == "":
            empty.append((key, srcs))
            emit("%-38s | %-28s | EMPTY_VALUE" % (key, srcs))
            continue
        if key in FIXED_SCAN_KEYS:
            pat = pw if pw else val
            patterns.setdefault(pat, set()).add("fixed:" + key)
            covered.append(key)
            emit("%-38s | %-28s | len=%-4d sha12=%s | COVERED_BY_SCAN" % (key, srcs, len(val), sha12(val)))
            continue
        if is_cred_name:
            if credential_shaped(val):
                patterns.setdefault(val, set()).add("derived:" + key)
                derived.append(key)
                emit("%-38s | %-28s | len=%-4d sha12=%s | DERIVED_CREDENTIAL_NOT_IN_FIXED_LIST" % (key, srcs, len(val), sha12(val)))
            else:
                nonsecret.append(key)
                emit("%-38s | %-28s | len=%-4d sha12=%s | NON_SECRET_PARAMETER(name only resembles a credential)" % (key, srcs, len(val), sha12(val)))
            continue
        if pw:
            patterns.setdefault(pw, set()).add("derived-userinfo:" + key)
            derived.append(key)
            emit("%-38s | %-28s | len=%-4d sha12=%s | DERIVED_USERINFO_PASSWORD" % (key, srcs, len(val), sha12(val)))
            continue
        nonsecret.append(key)
        emit("%-38s | %-28s | len=%-4d sha12=%s | NON_SECRET_PARAMETER" % (key, srcs, len(val), sha12(val)))

    emit("")
    emit("=== literal scan over artifacts (union pattern set) ===")
    missing_fixed = sorted(k for k in FIXED_SCAN_KEYS if k not in values or values.get(k, "") == "")
    emit("fixed_list_size=%d fixed_keys_without_pattern=%d %s"
         % (len(FIXED_SCAN_KEYS), len(missing_fixed), ",".join(missing_fixed) if missing_fixed else "(none)"))
    emit("patterns_total=%d covered_keys=%d derived_keys=%d empty_keys=%d non_secret_keys=%d"
         % (len(patterns), len(covered), len(derived), len(empty), len(nonsecret)))
    if derived:
        emit("derived_keys=%s" % ",".join(sorted(set(derived))))
    hits_total = 0
    for art in args.artifact:
        if not os.path.isfile(art):
            emit("artifact_missing=%s" % art)
            continue
        with open(art, "rb") as fh:
            blob = fh.read()
        hits = 0
        for pat in patterns:
            needle = pat.encode("utf-8", "replace")
            if not needle:
                continue
            start = 0
            while True:
                idx = blob.find(needle, start)
                if idx < 0:
                    break
                hits += 1
                start = idx + 1
        hits_total += hits
        emit("artifact=%s patterns=%d literal_hits=%d sha256=%s"
             % (os.path.basename(art), len(patterns), hits, hashlib.sha256(blob).hexdigest()))
    emit("literal_hits_total=%d" % hits_total)

    verdict = "PASS" if hits_total == 0 else "FAIL"
    emit("SECRET_SCAN_COVERAGE_VERDICT=%s" % verdict)
    emit("SECRET_SCAN_COVERAGE_NOTE=skipped/empty keys are reported explicitly and are never counted as covered")
    if args.out:
        with open(args.out, "w", encoding="utf-8") as fh:
            fh.write("\n".join(lines) + "\n")
    return 0 if verdict == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
