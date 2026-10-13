package rpc

import "testing"

func TestBatchUsageSeparatesRequestsFromBillableCalls(t *testing.T) {
	row := buildUsageRow(usageInput{MethodCalls: 100, BatchSize: 100}, DefaultPricing())
	if row.HTTPRequests != 1 || row.LogicalCalls != 100 || row.MethodCalls != 100 || row.Credits != 20000 || row.CostUSD != 0.002 {
		t.Fatalf("100-call batch must be one HTTP request with 100 priced calls: %+v", row)
	}
	shared := buildUsageRow(usageInput{MethodCalls: 1, Singleflight: true}, DefaultPricing())
	if shared.HTTPRequests != 0 || shared.LogicalCalls != 1 || shared.Credits != 0 || shared.CostUSD != 0 {
		t.Fatalf("shared result must not add a network request or cost: %+v", shared)
	}
}
