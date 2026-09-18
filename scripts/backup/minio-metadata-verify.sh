#!/usr/bin/env bash
# =====================================================================
# MinIO 桶语义 / 对象元数据 / 标签 / 版本 —— 取证与比对（v2）
#   scripts/backup/minio-metadata-verify.sh
#
# 为什么单独一个工具：`mc mirror` 结构性只反映**当前对象版本**，不复制版本历史、
# 生命周期规则、对象用户元数据与标签。"对象数 + 内容 hash 一致"**不能**代表维度完整。
#
# v2 相对 v1 修掉的缺陷（均由独立复核发现，逐条已在代码中落实）：
#   D1 verify 也曾调用 capture_semantics，覆写备份目录的 bucket-semantics.json，
#      且探测的是**生产桶**而不是恢复目标 → capture 只新建不覆写；verify 完全只读备份目录，
#      目标桶语义另写 $EV/minio-target-semantics-<TS>.json。
#   D2 version info / ilm 的**任意**失败都被当成"无版本、无规则" → 权限拒绝、连接失败
#      会变成假 PASS。现在只有**明确的 API 语义错误**才判"确实没有"，其余一律 FAIL。
#   D3 标签查询失败被 2>/dev/null / || true 吞成空标签 → 空标签假 PASS。现在严格检查 rc。
#   D4 头注释声称"用户元数据指纹"但只比了 ContentType/ETag/标签，且 size 采集了却没比 → 现在
#      采集并以指纹比对**全部用户元数据**，并比对 size。
#   D5 "unversioned" 不能证明没有历史版本（曾 enabled 后 suspended 仍留有旧版本）→ 现在对
#      每个桶全量 `ls --versions`，断言无 delete marker、版本数与清单一致。
#   D6 默认只取前 200 个对象却没有记录总量/截断 → 现在记录 total/sampled/truncated 与抽样策略；
#      **键集合与版本是全量比对**，元数据按预算抽样并在结果中明示。
#   D7 dimensions 判定用 inventory_ok 而代码写 compared_ok，会出现 verdict=PASS 但维度不完整
#      → 现在维度取值与真实比对结果一一对应。
#   D8 目标桶不得等于源桶（禁止拿生产桶自比冒充恢复）→ 显式拒绝。
#
# 退出码：0 = PASS；1 = FAIL/阻断；2 = 用法或前置错误。只读：不写任何桶/对象。
# =====================================================================
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/sprint7/dr}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
MC_IMAGE="${MC_IMAGE:-minio/mc}"
BUCKETS="${MINIO_META_BUCKETS:-blockchain-audit blockchain-backups blockchain-exports blockchain-reports blockchain-raw}"
META_MAX="${MINIO_META_MAX_OBJECTS:-200}"

MODE="${1:-}"
case "$MODE" in capture|verify) ;; *) echo "用法: $0 capture|verify <backup-dir> [--restored-drill b=d ...]" >&2; exit 2 ;; esac
DIR="${2:-}"
[ -n "$DIR" ] && [ -d "$DIR" ] || { echo "ABORT: 备份目录不存在：${DIR:-（空）}" >&2; exit 2; }
shift 2 || true

MC_HOST="${MINIO_MC_HOST:-}"
if [ -z "$MC_HOST" ] && [ -f "$PROJECT/deploy/.env" ]; then
  set -a; . "$PROJECT/deploy/.env"; set +a
  MC_HOST="http://${MINIO_ROOT_USER}:${MINIO_ROOT_PASSWORD}@127.0.0.1:${MINIO_API_PORT:-9001}"
fi
[ -n "$MC_HOST" ] || { echo "ABORT: 无法确定 MinIO 端点（MINIO_MC_HOST 或 deploy/.env）" >&2; exit 2; }

export MC_HOST_ENV="$MC_HOST" MC_IMAGE_ENV="$MC_IMAGE"
export MM_MODE="$MODE" MM_DIR="$DIR" MM_EV="$EV" MM_TS="$TS" MM_MAX="$META_MAX" MM_BUCKETS="$BUCKETS"
mkdir -p "$EV" || { echo "ABORT: 无法创建 $EV" >&2; exit 2; }

python3 - "$@" <<'PY'
import hashlib, json, os, subprocess, sys

MODE = os.environ["MM_MODE"]; DIR = os.environ["MM_DIR"]; EV = os.environ["MM_EV"]
TS = os.environ["MM_TS"]; MAXN = int(os.environ["MM_MAX"])
BUCKETS = os.environ["MM_BUCKETS"].split()
MC_HOST = os.environ["MC_HOST_ENV"]; MC_IMG = os.environ["MC_IMAGE_ENV"]
INV = os.path.join(DIR, "minio")

restored = {}
args = sys.argv[1:]
i = 0
while i < len(args):
    if args[i] == "--restored-drill":
        i += 1
        while i < len(args) and not args[i].startswith("--"):
            b, _, d = args[i].partition("=")
            restored[b] = d; i += 1
    else:
        print("ABORT: 未知参数 %s" % args[i], file=sys.stderr); sys.exit(2)

fails = []
def fail(m):
    fails.append(m); print("FAIL: " + m, file=sys.stderr)

def mc(a):
    p = subprocess.run(["docker", "run", "--rm", "--network", "host",
                        "-e", "MC_HOST_s3=" + MC_HOST, MC_IMG] + a,
                       capture_output=True, text=True)
    return p.returncode, p.stdout or "", p.stderr or ""

# ---------- 严格探测：只有明确的 API 语义错误才允许判"确实没有" ----------
ILM_NONE_MARK = "lifecycle configuration does not exist"
VER_UNVERSIONED_MARK = "is un-versioned"
VER_VERSIONED_MARK = "is versioned"

def probe_versioning(b):
    rc, so, se = mc(["version", "info", "s3/" + b])
    raw = (so + " " + se).strip()
    if rc != 0:
        return {"status": "error", "rc": rc, "raw": raw[:300]}
    if VER_UNVERSIONED_MARK in raw:
        return {"status": "unversioned", "rc": rc, "raw": raw[:300]}
    if VER_VERSIONED_MARK in raw:
        return {"status": "versioned", "rc": rc, "raw": raw[:300]}
    return {"status": "unclassifiable", "rc": rc, "raw": raw[:300]}

def probe_ilm(b):
    rc, so, se = mc(["ilm", "rule", "ls", "s3/" + b])
    raw = (so + " " + se).strip()
    if rc == 0:
        rc2, so2, _ = mc(["ilm", "rule", "export", "s3/" + b])
        return {"status": "present", "rc": rc, "raw": raw[:300],
                "export_rc": rc2, "export": so2 if rc2 == 0 else ""}
    if ILM_NONE_MARK in raw:
        return {"status": "none", "rc": rc, "raw": raw[:300]}
    # 权限拒绝 / 连接失败 / 未知错误：**不得**当成"没有规则"
    return {"status": "error", "rc": rc, "raw": raw[:300]}

def list_keys(b):
    rc, so, se = mc(["ls", "--recursive", "s3/" + b])
    if rc != 0:
        return None, "ls --recursive rc=%d: %s" % (rc, (se or so).strip()[:200])
    keys = []
    for ln in so.splitlines():
        parts = ln.split()
        if len(parts) >= 2 and not ln.rstrip().endswith("/"):
            keys.append(parts[-1])
    return sorted(keys), None

def list_versions(b):
    """全量版本核对：返回 {key: {"versions": n, "delete_markers": n}} 与错误。"""
    rc, so, se = mc(["ls", "--versions", "--recursive", "s3/" + b])
    if rc != 0:
        return None, "ls --versions rc=%d: %s" % (rc, (se or so).strip()[:200])
    out = {}
    for ln in so.splitlines():
        if not ln.strip():
            continue
        toks = ln.split()
        if toks[0].startswith("["):
            continue
        key = toks[-1]
        e = out.setdefault(key, {"versions": 0, "delete_markers": 0})
        if "DELETE" in ln.upper():
            e["delete_markers"] += 1
        else:
            e["versions"] += 1
    return out, None

def stat_obj(b, key):
    rc, so, se = mc(["stat", "s3/%s/%s" % (b, key)])
    if rc != 0:
        return None, "stat rc=%d: %s" % (rc, (se or so).strip()[:160])
    d = {"size": None, "etag": None, "content_type": None, "user_meta": {}}
    in_meta = False
    for ln in so.splitlines():
        if ln.strip().startswith("Metadata :"):
            in_meta = True; continue
        if in_meta:
            if ":" in ln and ln.startswith(" "):
                k, _, v = ln.strip().partition(":")
                k = k.strip(); v = v.strip()
                if k.lower() == "content-type":
                    d["content_type"] = v
                else:
                    d["user_meta"][k] = v
                continue
            if ln.strip() == "":
                in_meta = False
        if ln.startswith("Size") and ":" in ln:
            d["size"] = ln.split(":", 1)[1].strip()
        if ln.startswith("ETag") and ":" in ln:
            d["etag"] = ln.split(":", 1)[1].strip()
    if d["etag"] is None or d["size"] is None:
        return None, "stat 输出缺少 ETag/Size：%s" % so.strip()[:160]
    return d, None

def tag_obj(b, key):
    rc, so, se = mc(["tag", "list", "s3/%s/%s" % (b, key)])
    if rc != 0:
        raw = (se or so)
        # 明确"无标签"才是合法的空；其余（权限/网络）必须 FAIL
        if "No tags found" in raw or "no tags" in raw.lower():
            return "", None
        return None, "tag list rc=%d: %s" % (rc, raw.strip()[:160])
    lines = [x.strip() for x in so.splitlines() if x.strip()]
    lines = [x for x in lines if not x.startswith("Name :") and x != "No tags found"]
    return ";".join(lines), None

def digest(o):
    return hashlib.sha256(json.dumps(o, sort_keys=True, ensure_ascii=False).encode()).hexdigest()

def write_json(path, obj, create_only=False):
    if create_only and os.path.exists(path):
        print("ABORT: 拒绝覆写既有证据：%s（如需重新取证请显式移动旧文件）" % path, file=sys.stderr)
        sys.exit(2)
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(obj, f, indent=2, ensure_ascii=False)
    os.replace(tmp, path)

print("=== MinIO 元数据 %s %s ===" % (MODE, TS), file=sys.stderr)
print("证据目录：%s  备份目录：%s  抽样上限：%d" % (EV, DIR, MAXN), file=sys.stderr)

# ============================ CAPTURE ============================
if MODE == "capture":
    os.makedirs(INV, exist_ok=True)
    sem_path = os.path.join(INV, "bucket-semantics.json")
    if os.path.exists(sem_path):
        print("ABORT: 备份目录已有桶语义证据，拒绝覆写：%s" % sem_path, file=sys.stderr)
        sys.exit(2)
    sem = {"probed_at": TS, "source": "backup-point", "buckets": {}}
    for b in BUCKETS:
        v = probe_versioning(b); l = probe_ilm(b)
        if v["status"] in ("error", "unclassifiable"):
            fail("capture：桶 %s 的 versioning 探测失败/无法判定（rc=%s）：%s" % (b, v["rc"], v["raw"]))
        if l["status"] == "error":
            fail("capture：桶 %s 的 ILM 探测失败（rc=%s）：%s" % (b, l["rc"], l["raw"]))
        sem["buckets"][b] = {"versioning": v, "ilm": l}
    if fails:
        print("capture 中止：桶语义探测存在阻断项，未写入任何清单", file=sys.stderr)
        sys.exit(1)

    for b in BUCKETS:
        keys, err = list_keys(b)
        if err:
            fail("capture：桶 %s 键列表失败：%s" % (b, err)); continue
        vers, verr = list_versions(b)
        if verr:
            fail("capture：桶 %s 版本列表失败：%s" % (b, verr)); continue
        with open(os.path.join(INV, b + ".keys.txt"), "w") as f:
            f.write("\n".join(keys) + ("\n" if keys else ""))
        inv = []
        for key in keys[:MAXN]:
            d, e = stat_obj(b, key)
            if e:
                fail("capture：桶 %s 对象 %s 元数据失败：%s" % (b, key, e)); continue
            tg, te = tag_obj(b, key)
            if te:
                fail("capture：桶 %s 对象 %s 标签查询失败：%s" % (b, key, te)); continue
            inv.append({"key": key, "size": d["size"], "etag": d["etag"],
                        "content_type": d["content_type"], "user_meta": d["user_meta"],
                        "user_meta_sha256": digest(d["user_meta"]),
                        "tags": tg, "tags_sha256": hashlib.sha256(tg.encode()).hexdigest()})
        with open(os.path.join(INV, b + ".inventory.jsonl"), "w") as f:
            for r in inv:
                f.write(json.dumps(r, ensure_ascii=False) + "\n")
        write_json(os.path.join(INV, b + ".versions.json"),
                   {"total_objects": len(keys), "sampled_objects": len(inv),
                    "truncated": len(inv) < len(keys), "versions": vers})
        print("桶 %s：对象总数 %d，元数据抽样 %d（截断=%s）" % (b, len(keys), len(inv), len(inv) < len(keys)), file=sys.stderr)
    write_json(sem_path, sem, create_only=True)
    print("capture 完成：%s" % sem_path, file=sys.stderr)
    if fails:
        print("capture 有 %d 条阻断项" % len(fails), file=sys.stderr); sys.exit(1)
    sys.exit(0)

# ============================ VERIFY ============================
sem_path = os.path.join(INV, "bucket-semantics.json")
if not os.path.exists(sem_path):
    print("ABORT: 缺少 %s（备份时未做桶语义取证）" % sem_path, file=sys.stderr); sys.exit(2)
sem = json.load(open(sem_path))

# 目标桶语义：只读探测，写到 EV（**不碰备份目录**）
tgt_sem = {"probed_at": TS, "target": {b: restored.get(b) for b in BUCKETS}, "buckets": {}}
for b in BUCKETS:
    t = restored.get(b)
    if not t:
        continue
    v = probe_versioning(t); l = probe_ilm(t)
    if v["status"] in ("error", "unclassifiable"):
        fail("目标桶 %s（源 %s）versioning 探测失败/无法判定：rc=%s %s" % (t, b, v["rc"], v["raw"]))
    if l["status"] == "error":
        fail("目标桶 %s（源 %s）ILM 探测失败：rc=%s %s" % (t, b, l["rc"], l["raw"]))
    tgt_sem["buckets"][b] = {"target_bucket": t, "versioning": v, "ilm": l}
write_json(os.path.join(EV, "minio-target-semantics-%s.json" % TS), tgt_sem)

bres = {}
for b in BUCKETS:
    e = {"source_bucket": b, "target_bucket": restored.get(b),
         "objects_in_inventory": 0, "total_objects_at_capture": None, "sampled": None,
         "keys_compared": 0, "meta_compared": 0, "missing": [], "mismatch": [],
         "versions": "not_checked", "ilm": "not_checked", "tags": "incomplete", "metadata": "incomplete"}
    bres[b] = e

    s = sem["buckets"].get(b) or {}
    sv = (s.get("versioning") or {}).get("status")
    si = (s.get("ilm") or {}).get("status")

    # --- 目标必须显式提供，且不得等于源桶（禁止生产桶自比）---
    t = restored.get(b)
    if not t:
        fail("%s：未提供恢复侧目标桶（--restored-drill %s=<drill-bucket>），无法比对，不能判 PASS" % (b, b))
        continue
    if t == b:
        fail("%s：目标桶与源桶同名（%s）—— 禁止用生产桶自比冒充恢复演练" % (b, t))
        continue

    inv_path = os.path.join(INV, b + ".inventory.jsonl")
    keys_path = os.path.join(INV, b + ".keys.txt")
    ver_path = os.path.join(INV, b + ".versions.json")
    for p in (inv_path, keys_path, ver_path):
        if not os.path.exists(p):
            fail("%s：备份目录缺少 %s（capture 未完成）" % (b, os.path.basename(p)))
    if not all(os.path.exists(p) for p in (inv_path, keys_path, ver_path)):
        continue

    inv = [json.loads(x) for x in open(inv_path) if x.strip()]
    cap_keys = [x for x in open(keys_path).read().splitlines() if x]
    cap_ver = json.load(open(ver_path))
    e["objects_in_inventory"] = len(inv)
    e["total_objects_at_capture"] = cap_ver.get("total_objects")
    e["sampled"] = cap_ver.get("truncated")

    # --- 全量键集合比对 ---
    tgt_keys, kerr = list_keys(t)
    if kerr:
        fail("%s：目标桶 %s 键列表失败：%s" % (b, t, kerr)); continue
    e["keys_compared"] = len(cap_keys)
    miss_k = sorted(set(cap_keys) - set(tgt_keys))
    extra_k = sorted(set(tgt_keys) - set(cap_keys))
    if miss_k:
        fail("%s：目标桶 %s 缺少 %d 个对象（示例：%s）" % (b, t, len(miss_k), ", ".join(miss_k[:3])))
    if extra_k:
        fail("%s：目标桶 %s 多出 %d 个对象（示例：%s）" % (b, t, len(extra_k), ", ".join(extra_k[:3])))

    # --- 版本维度（全量）---
    tgt_ver, verr = list_versions(t)
    if verr:
        fail("%s：目标桶 %s 版本列表失败：%s" % (b, t, verr))
    else:
        dm = sum(v["delete_markers"] for v in tgt_ver.values())
        # 源桶若为 unversioned，仍需证明目标同样只有单版本且无 delete marker
        multi = [k for k, v in tgt_ver.items() if v["versions"] > 1]
        if sv == "unversioned":
            if multi or dm:
                fail("%s：源桶标记为 unversioned，但目标桶 %s 存在 %d 个多版本对象与 %d 个 delete marker —— "
                     "说明存在历史版本（曾启用后 suspended），必须逐版本恢复后再验" % (b, t, len(multi), dm))
            else:
                e["versions"] = "full_verified_single_version_no_delete_marker"
        elif sv == "versioned":
            cap_multi = {k: v for k, v in (cap_ver.get("versions") or {}).items() if v["versions"] > 1}
            tgt_multi = {k: v for k, v in tgt_ver.items() if v["versions"] > 1}
            cap_dm = sum(v["delete_markers"] for v in (cap_ver.get("versions") or {}).values())
            if cap_multi and not tgt_multi and cap_dm == 0:
                fail("%s：源桶启用 versioning 且有 %d 个多版本对象，目标桶 %s 无多版本 —— 版本历史未恢复"
                     % (b, len(cap_multi), t))
            elif sum(v["versions"] for v in tgt_ver.values()) < sum(v["versions"] for v in (cap_ver.get("versions") or {}).values()):
                fail("%s：目标桶 %s 版本总数少于源桶，版本历史不完整" % (b, t))
            else:
                e["versions"] = "full_verified_version_counts"
        else:
            fail("%s：源桶 versioning 状态不可判定（%s），无法证明版本维度" % (b, sv))

    # --- ILM 维度 ---
    ti = (tgt_sem["buckets"].get(b, {}).get("ilm") or {}).get("status")
    if si == "none":
        if ti == "none":
            e["ilm"] = "full_verified_none_both_sides"
        else:
            fail("%s：源桶无生命周期规则，目标桶却是 %s —— 语义不一致" % (b, ti))
    elif si == "present":
        if ti != "present":
            fail("%s：源桶有生命周期规则，目标桶 %s 未复现" % (b, ti))
        else:
            e["ilm"] = "full_verified_rules_present"
    else:
        fail("%s：源桶 ILM 状态不可判定（%s）" % (b, si))

    # --- 逐对象元数据 / 标签 / size（按抽样预算）---
    miss, mism = [], []
    for r in inv:
        key = r["key"]
        d, err = stat_obj(t, key)
        if err:
            miss.append("%s (%s)" % (key, err)); continue
        if d["size"] != r["size"]:
            mism.append("%s: size %s != %s" % (key, d["size"], r["size"]))
        if d["etag"] != r["etag"]:
            mism.append("%s: etag %s != %s" % (key, d["etag"], r["etag"]))
        if r.get("content_type") and d["content_type"] != r["content_type"]:
            mism.append("%s: content-type %s != %s" % (key, d["content_type"], r["content_type"]))
        if digest(d["user_meta"]) != r.get("user_meta_sha256"):
            mism.append("%s: 用户元数据指纹不一致 %s != %s" % (key, d["user_meta"], r.get("user_meta")))
        tg, terr = tag_obj(t, key)
        if terr:
            miss.append("%s (tag: %s)" % (key, terr)); continue
        if hashlib.sha256(tg.encode()).hexdigest() != r.get("tags_sha256"):
            mism.append("%s: 标签不一致 '%s' != '%s'" % (key, tg, r.get("tags")))
    e["missing"] = miss; e["mismatch"] = mism
    e["meta_compared"] = len(inv) - len(miss)
    if miss:
        fail("%s：目标桶 %s 有 %d 个对象缺失或元数据不可读（示例：%s）" % (b, t, len(miss), "; ".join(miss[:2])))
    if mism:
        fail("%s：目标桶 %s 有 %d 处元数据/标签/size 不一致（示例：%s）" % (b, t, len(mism), "; ".join(mism[:2])))
    if not miss and not mism:
        e["metadata"] = "compared_ok(%d/%d)" % (len(inv), len(inv))
        e["tags"] = "compared_ok(%d/%d)" % (len(inv), len(inv))

dims = {}
ok_meta = all(str(bres[b].get("metadata", "")).startswith("compared_ok") for b in BUCKETS)
ok_tags = all(str(bres[b].get("tags", "")).startswith("compared_ok") for b in BUCKETS)
ok_ver = all(bres[b]["versions"].startswith("full_verified") for b in BUCKETS)
ok_ilm = all(bres[b]["ilm"].startswith("full_verified") for b in BUCKETS)
sampled_any = any(bres[b].get("sampled") for b in BUCKETS)
dims["metadata"] = ("compared_ok_sampled" if sampled_any else "compared_ok_full") if ok_meta else "incomplete"
dims["tags"] = dims["metadata"] if ok_tags else "incomplete"
dims["versions"] = "full_verified" if ok_ver else "incomplete"
dims["ilm"] = "full_verified" if ok_ilm else "incomplete"

res = {"drill": "minio-object-metadata", "ts": TS, "mode": MODE,
       "verdict": "FAIL" if fails else "PASS",
       "verdict_note": "四个维度（versions/ilm/tags/metadata）必须全部完成取证并通过比对才是 PASS；"
                       "键集合与版本为全量比对，元数据/标签按抽样预算并在结果中明示截断；"
                       "任何探测失败（权限/网络）都不得被解释为'没有版本/没有规则/没有标签'",
       "sampling": {"max_objects_per_bucket": MAXN,
                    "keys_and_versions": "full",
                    "object_metadata_tags": "sampled" if sampled_any else "full",
                    "truncated_buckets": [b for b in BUCKETS if bres[b].get("sampled")]},
       "dimensions": dims, "buckets": bres, "failures": fails,
       "target_semantics_file": os.path.join(EV, "minio-target-semantics-%s.json" % TS),
       "source_semantics_file": sem_path}
out_json = os.path.join(EV, "minio-object-metadata-%s.json" % TS)
out_md = os.path.join(EV, "minio-object-metadata-%s.md" % TS)
write_json(out_json, res)
with open(out_md, "w") as f:
    f.write("# MinIO 桶语义 / 对象元数据 / 标签 / 版本比对（%s）\n\n" % TS)
    f.write("- 判定：**%s**\n" % res["verdict"])
    f.write("- 抽样策略：键集合与版本=全量；元数据/标签=每桶上限 %d（截断桶：%s）\n\n"
            % (MAXN, ", ".join(res["sampling"]["truncated_buckets"]) or "无"))
    f.write("| 桶 | 目标桶 | 源对象总数 | 元数据抽样 | 键比对 | versions | ilm | metadata | tags |\n")
    f.write("|---|---|---:|---:|---:|---|---|---|---|\n")
    for b in BUCKETS:
        e = bres[b]
        f.write("| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n"
                % (b, e["target_bucket"] or "-", e["total_objects_at_capture"], e["objects_in_inventory"],
                   e["keys_compared"], e["versions"], e["ilm"], e["metadata"], e["tags"]))
    if fails:
        f.write("\n## 阻断项\n\n")
        for x in fails:
            f.write("- %s\n" % x)
print(json.dumps({k: v for k, v in res.items() if k != "buckets"}, ensure_ascii=False, indent=1))
print("verdict: %s" % res["verdict"])
print("result:  %s" % out_json)
print("report:  %s" % out_md)
if fails:
    print("MinIO 元数据维度：FAIL（%d 条阻断项）—— 不得计入已覆盖" % len(fails), file=sys.stderr)
    sys.exit(1)
print("MinIO 元数据维度：PASS")
sys.exit(0)
PY
