package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestIngestPipelineSubmitDeduplicatesPageReplay(t *testing.T) {
	p := &ingestPipeline{queue: make(chan pageRef, 4)}
	page := pageRef{pageID: "page-1"}

	if !p.submit(context.Background(), page) {
		t.Fatal("first submit rejected")
	}
	if !p.submit(context.Background(), page) {
		t.Fatal("duplicate submit should be treated as already scheduled")
	}
	if got := len(p.queue); got != 1 {
		t.Fatalf("duplicate replay queued %d copies, want 1", got)
	}

	// Completion/failure clears the guard and permits an intentional retry.
	p.scheduled.Delete(page.pageID)
	if !p.submit(context.Background(), page) {
		t.Fatal("retry submit rejected")
	}
	if got := len(p.queue); got != 2 {
		t.Fatalf("retry was not queued: queue length=%d, want 2", got)
	}
}

func TestDecodeRawRowsValidatesDurableObject(t *testing.T) {
	raw := []byte(`[{"id":1},{"id":2}]`)
	sum := sha256.Sum256(raw)
	rows, err := decodeRawRows(raw, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("decode valid raw rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("decoded %d rows, want 2", len(rows))
	}
	if _, err := decodeRawRows(raw, "bad-checksum"); err == nil {
		t.Fatal("checksum mismatch must fail")
	}
}
