package dataset

import (
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestDownloadRequestFingerprintNormalizesSetOrder(t *testing.T) {
	rng := model.DownloadJobRange{RangeType: "full"}
	a := downloadRequestFingerprint("tron", []string{"TB", "TA"}, []string{"transactions", "token_transfers"}, rng)
	b := downloadRequestFingerprint("tron", []string{"TA", "TB"}, []string{"token_transfers", "transactions"}, rng)
	if a != b {
		t.Fatalf("same request sets produced different fingerprints: %s != %s", a, b)
	}
}

func TestDownloadRequestFingerprintSeparatesRangeAndDataset(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	to := from.Add(time.Hour)
	base := downloadRequestFingerprint("tron", []string{"TA"}, []string{"transactions"}, model.DownloadJobRange{RangeType: "full"})
	timed := downloadRequestFingerprint("tron", []string{"TA"}, []string{"transactions"}, model.DownloadJobRange{RangeType: "time", FromTimestamp: &from, ToTimestamp: &to})
	otherDataset := downloadRequestFingerprint("tron", []string{"TA"}, []string{"token_transfers"}, model.DownloadJobRange{RangeType: "full"})
	if base == timed || base == otherDataset || timed == otherDataset {
		t.Fatal("different download requests must not share a fingerprint")
	}
}
