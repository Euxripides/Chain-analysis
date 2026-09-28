package query

import (
	"strings"
	"testing"
)

/*
 * 键集分页的纯函数部分必须自己站得住：SQL 形状与游标类型一旦写错，
 * 表现是"导出少了几万行但状态成功"——正是这次要根除的故障形态。
 * 因此这里不依赖任何外部服务，把不变量钉死在单测里。
 */

func TestExportPageSQLShape(t *testing.T) {
	for _, dataset := range []string{"transactions", "token_transfers", "logs", "nft_transfers", "internal_transactions"} {
		cur, ok := ExportCursorFor(dataset)
		if !ok {
			t.Fatalf("%s: no cursor", dataset)
		}
		if len(cur.SelectExprs) != len(cur.KeyExprs) || len(cur.Kinds) != len(cur.KeyExprs) {
			t.Fatalf("%s: cursor column arity mismatch: select=%d key=%d kinds=%d",
				dataset, len(cur.SelectExprs), len(cur.KeyExprs), len(cur.Kinds))
		}
		q := ExportKeysetQuery{Dataset: dataset, Table: "t", Where: "chain=? AND address=?", Args: []any{"bsc", "0x1"}}

		// 首页：无游标条件，投影里必须有游标列，且必须带 LIMIT。
		first, firstArgs := exportPageSQL(q, cur, nil, 1234)
		if strings.Contains(first, ") < (") {
			t.Fatalf("%s: first page must not carry a cursor predicate: %s", dataset, first)
		}
		if !strings.Contains(first, "LIMIT ?") {
			t.Fatalf("%s: page SQL without LIMIT: %s", dataset, first)
		}
		for _, expr := range cur.SelectExprs {
			if !strings.Contains(first, expr) {
				t.Fatalf("%s: page SQL is missing cursor projection %q: %s", dataset, expr, first)
			}
		}
		if got, want := strings.Count(first, "?"), len(firstArgs); got != want {
			t.Fatalf("%s: first page placeholders=%d args=%d: %s", dataset, got, want, first)
		}
		if firstArgs[len(firstArgs)-1] != 1234 {
			t.Fatalf("%s: page size must be the last argument, got %v", dataset, firstArgs[len(firstArgs)-1])
		}

		// 续页：元组比较必须逐个用上全部游标列，参数顺序与列顺序一致。
		cursor := make([]any, len(cur.KeyExprs))
		for i, k := range cur.Kinds {
			if k == CursorInt64 {
				cursor[i] = int64(1700000000000 + i)
			} else {
				cursor[i] = "0xabc"
			}
		}
		next, nextArgs := exportPageSQL(q, cur, cursor, 10)
		wantPredicate := " AND (" + strings.Join(cur.KeyExprs, ", ") + ") < (" +
			placeholderList(len(cursor)) + ")"
		if !strings.Contains(next, wantPredicate) {
			t.Fatalf("%s: continuation predicate mismatch\n got: %s\nwant: %s", dataset, next, wantPredicate)
		}
		if got, want := strings.Count(next, "?"), len(nextArgs); got != want {
			t.Fatalf("%s: continuation placeholders=%d args=%d: %s", dataset, got, want, next)
		}
		for i := range cursor {
			if nextArgs[len(q.Args)+i] != cursor[i] {
				t.Fatalf("%s: cursor arg %d = %v, want %v", dataset, i, nextArgs[len(q.Args)+i], cursor[i])
			}
		}
	}
}

// TestExportCursorCoversDedupeKey 钉住"排序键覆盖版本键"这条不变量：
// ReplacingMergeTree 的版本键必须全部出现在游标列里，否则同一版本键的两行
// 可能排在页边界两侧、跨页各写一次（跨页 seen 只能拦住完全相同的键，
// 拦不住"同一版本键但游标列不同"的两行）。
func TestExportCursorCoversDedupeKey(t *testing.T) {
	for _, dataset := range []string{"transactions", "token_transfers", "logs", "nft_transfers", "internal_transactions"} {
		cur, ok := ExportCursorFor(dataset)
		if !ok {
			t.Fatalf("%s: no cursor", dataset)
		}
		joined := strings.Join(cur.KeyExprs, ",")
		for _, col := range exportDedupeColumns(dataset) {
			if !strings.Contains(joined, col) {
				t.Fatalf("%s: dedupe key column %q is not covered by cursor key %q", dataset, col, joined)
			}
		}
	}
}

// TestExportCursorOrdersDescending 钉住 DESC + 严格小于的配对关系：
// 排序方向与游标比较方向必须一致，反了会让第二页从"最新"重新开始（无限重复）。
func TestExportCursorOrdersDescending(t *testing.T) {
	for _, dataset := range []string{"transactions", "token_transfers", "logs", "nft_transfers", "internal_transactions"} {
		cur, _ := ExportCursorFor(dataset)
		parts := strings.Split(cur.OrderBy, ",")
		if len(parts) != len(cur.KeyExprs) {
			t.Fatalf("%s: ORDER BY has %d terms for %d key columns: %s", dataset, len(parts), len(cur.KeyExprs), cur.OrderBy)
		}
		for _, part := range parts {
			if !strings.HasSuffix(strings.TrimSpace(part), "DESC") {
				t.Fatalf("%s: ORDER BY term %q is not DESC (cursor uses strict <)", dataset, part)
			}
		}
	}
}

func TestSameCursor(t *testing.T) {
	if !sameCursor([]any{int64(1), "a"}, []any{int64(1), "a"}) {
		t.Fatal("identical cursors must compare equal")
	}
	if sameCursor([]any{int64(1)}, []any{int64(1), "a"}) {
		t.Fatal("different arity must not compare equal")
	}
	if sameCursor([]any{int64(1)}, []any{int64(2)}) {
		t.Fatal("different values must not compare equal")
	}
}

// TestDerefCursorKeepsNumericType 钉住"游标数值列必须以整数回传"：
// 字符串化的毫秒时间戳会在元组比较里退化成字典序（"999" > "1000"），
// 实测会丢键。
func TestDerefCursorKeepsNumericType(t *testing.T) {
	i := int64(1780000000000)
	if got := derefCursor(CursorInt64, &i); got != i {
		t.Fatalf("int64 cursor = %#v, want %d", got, i)
	}
	u := uint32(7)
	if got := derefCursor(CursorInt64, &u); got != int64(7) {
		t.Fatalf("uint32 cursor = %#v, want int64(7)", got)
	}
	s := "0xabc"
	if got := derefCursor(CursorString, &s); got != s {
		t.Fatalf("string cursor = %#v, want %q", got, s)
	}
}
