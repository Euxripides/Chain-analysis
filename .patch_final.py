import io, sys
patches = {
  'scripts/backup/backup-all.sh': [
    ('select count(FINAL) from $CLICKHOUSE_DB.$t', 'select count() from $CLICKHOUSE_DB.$t FINAL'),
    ('BACKUP_CH_FINAL_TABLES      需要比对 count(FINAL) 的 CH 表', 'BACKUP_CH_FINAL_TABLES      需要比对 count() ... FINAL 的 CH 表'),
  ],
  'scripts/backup/restore-drill.sh': [
    ('select count(FINAL) from $CLICKHOUSE_DB.$t', 'select count() from $CLICKHOUSE_DB.$t FINAL'),
    ('select count(FINAL) from $CHR.$t', 'select count() from $CHR.$t FINAL'),
    ('#   RESTORE_DRILL_CH_TABLES             需要 count(FINAL) 比对的 CH 表', '#   RESTORE_DRILL_CH_TABLES             需要 count() ... FINAL 比对的 CH 表'),
  ],
}
note = (
  "# 2026-09-16 修正（真实缺陷）：正确写法是 SELECT count() FROM t FINAL；",
  "# 写成 count(FINAL) 在 ClickHouse 26.7 上是语法错误（Unknown expression or function identifier FINAL），",
  "# 会让**每一次**备份都被标记 __INCOMPLETE，并让 restore-drill 的 CH 判定必然失败 —— DR 门禁因此长期结构性不可通过。",
)
for path, subs in patches.items():
    s = io.open(path, encoding="utf-8").read()
    for old, new in subs:
        if old not in s:
            print("MISS in", path, "::", old[:70]); continue
        s = s.replace(old, new)
    if path.endswith("backup-all.sh") and note[0] not in s:
        marker = "# [F6] ReplacingMergeTree 必须用 count(FINAL) 核对"
        i = s.find(marker)
        if i >= 0:
            s = s[:i] + "\n".join(note) + "\n" + s[i:]
            s = s.replace("# [F6] ReplacingMergeTree 必须用 count(FINAL) 核对", "# [F6] ReplacingMergeTree 必须用 FINAL 核对")
    io.open(path, "w", encoding="utf-8", newline="\n").write(s)
    print("patched", path)