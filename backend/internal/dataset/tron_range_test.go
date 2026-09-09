package dataset

import (
	"context"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestAnkrTronRangeRequiresBoundedScan(t *testing.T) {
	service := &Service{TronProvider: "ankr"}
	if _, err := service.ResolveRange(context.Background(), "tron", RangeSpec{Type: "full"}); err == nil {
		t.Fatal("Ankr TRON full-history scan must be rejected")
	}
	from, to := int64(100), int64(110)
	rangeValue, err := service.ResolveRange(context.Background(), "tron", RangeSpec{Type: "block", FromBlock: &from, ToBlock: &to})
	if err != nil || rangeValue.FromBlock == nil || *rangeValue.FromBlock != from || rangeValue.ToBlock == nil || *rangeValue.ToBlock != to {
		t.Fatalf("bounded TRON block range rejected: range=%+v err=%v", rangeValue, err)
	}
}

func TestLegacyTronGridStillAllowsFullRange(t *testing.T) {
	service := &Service{TronProvider: "trongrid"}
	rangeValue, err := service.ResolveRange(context.Background(), "tron", RangeSpec{Type: "full"})
	if err != nil || rangeValue.RangeType != "full" {
		t.Fatalf("legacy TronGrid full range compatibility broken: range=%+v err=%v", rangeValue, err)
	}
}

func TestAnkrTronAutoScanAdmissionLimits(t *testing.T) {
	service := &Service{TronProvider: "ankr", TronMaxTxAutoScan: 1000, TronMaxInternalAutoScan: 5000}
	from, txEnd, internalEnd := int64(1000), int64(2000), int64(6000)
	if err := service.validateTronAutoScan("tron", []string{"transactions"}, model.DownloadJobRange{RangeType: "block", FromBlock: &from, ToBlock: &txEnd}); err == nil {
		t.Fatal("1001-block transaction scan must be rejected before job creation")
	}
	if err := service.validateTronAutoScan("tron", []string{"internal_transactions"}, model.DownloadJobRange{RangeType: "block", FromBlock: &from, ToBlock: &internalEnd}); err == nil {
		t.Fatal("5001-block internal scan must be rejected before job creation")
	}
	allowedTxEnd := int64(1999)
	if err := service.validateTronAutoScan("tron", []string{"transactions"}, model.DownloadJobRange{RangeType: "block", FromBlock: &from, ToBlock: &allowedTxEnd}); err != nil {
		t.Fatalf("1000-block transaction scan rejected: %v", err)
	}
	start := time.Unix(0, 0)
	tooLong := start.Add(51 * time.Minute)
	if err := service.validateTronAutoScan("tron", []string{"transactions"}, model.DownloadJobRange{RangeType: "time", FromTimestamp: &start, ToTimestamp: &tooLong}); err == nil {
		t.Fatal("time-range transaction scan above the admission limit must be rejected")
	}
}
