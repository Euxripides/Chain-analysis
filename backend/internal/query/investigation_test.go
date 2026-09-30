package query

import (
	"strings"
	"testing"
	"time"
)

func TestAggregateSQLPagesAfterFullDedupGroupAndBindsValues(t *testing.T) {
	r := AggregateRequest{ActionFilter: ActionFilter{Chain: "bsc", Address: "x' OR 1=1 --", TokenContract: "contract", PageSize: 500}, GroupBy: []string{"counterparty", "direction"}, OrderBy: "sum_amount", Offset: 8000, Cutoff: 123, IngestedBefore: time.Now()}
	sql, args, e := aggregateSQL(r)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(sql, r.Address) || !strings.Contains(sql, "GROUP BY tx_hash,log_index,token_address") || !strings.HasSuffix(sql, "LIMIT ? OFFSET ?") || args[len(args)-1] != int64(8000) || args[len(args)-2] != 501 {
		t.Fatal(sql, args)
	}
	r.GroupBy = []string{"counterparty); DROP TABLE x"}
	if _, _, e = aggregateSQL(r); e == nil {
		t.Fatal("unsafe grouping accepted")
	}
	r.GroupBy = []string{"counterparty"}
	r.OrderBy = "sum_amount; DROP"
	if _, _, e = aggregateSQL(r); e == nil {
		t.Fatal("unsafe order accepted")
	}
}
