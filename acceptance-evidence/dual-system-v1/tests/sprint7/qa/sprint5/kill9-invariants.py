import json, os, sys
b = json.loads(os.environ["BEFORE"])
a = json.loads(os.environ["AFTER"])
which = sys.argv[1] if len(sys.argv) > 1 else "all"
bs = {s["id"]: s for s in b["shards"]}
as_ = {s["id"]: s for s in a["shards"]}

def counters():
    bad = []
    for sid, s in bs.items():
        t = as_.get(sid)
        if t is None:
            bad.append({"shard_id": sid, "field": "shard_row", "before": "present", "after": "missing"})
            continue
        for f in ("page_no", "rows", "page_count", "success_pages", "provider_ok"):
            if t[f] < s[f]:
                bad.append({"shard_id": sid, "field": f, "before": s[f], "after": t[f]})
    return bad

def pages():
    bp, ap = set(b["success_page_keys"]), set(a["success_page_keys"])
    return sorted(bp - ap)

def tokens():
    """合法进展 vs cursor 丢失：
       前进（page_no/page_count/成功页任一增加）时 token 变化或末页清空都合法；
       无任何前进却变化/清空 => 记违反。token 只比较 hash，不出明文。"""
    bad, notes = [], []
    for sid, s in bs.items():
        t = as_.get(sid)
        if t is None:
            continue
        if s.get("token_hash") == t.get("token_hash"):
            continue
        progressed = (t["page_no"] > s["page_no"] or t["page_count"] > s["page_count"]
                      or t["success_pages"] > s["success_pages"])
        ev = {"shard_id": sid, "before": "none" if s.get("token_hash") is None else "set",
              "after": "none" if t.get("token_hash") is None else "set",
              "page_no": [s["page_no"], t["page_no"]],
              "page_count": [s["page_count"], t["page_count"]],
              "success_pages": [s["success_pages"], t["success_pages"]]}
        if progressed:
            ev["classification"] = "legitimate_progress"
            notes.append(ev)
        else:
            ev["classification"] = "cursor_changed_without_progress"
            bad.append(ev)
    return bad, notes

def subjects():
    bm = {x["id"]: x for x in b["subjects"]}
    am = {x["id"]: x for x in a["subjects"]}
    missing = sorted(set(bm) - set(am))
    return missing

if which == "counters":
    print("ok" if not counters() else json.dumps(counters(), ensure_ascii=False))
elif which == "pages":
    lost = pages()
    print("ok" if not lost else json.dumps(lost, ensure_ascii=False))
elif which == "tokens":
    bad, _ = tokens()
    print("ok" if not bad else json.dumps(bad, ensure_ascii=False))
elif which == "subjects":
    m = subjects()
    print("ok" if not m else json.dumps(m, ensure_ascii=False))
elif which == "shardset":
    print("ok" if set(bs) == set(as_) else json.dumps(sorted(set(bs) ^ set(as_)), ensure_ascii=False))
