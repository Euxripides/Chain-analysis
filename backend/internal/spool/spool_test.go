package spool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteScanLoadRemove(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{Dir: dir, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	meta := PageMeta{
		JobID: "job-1", Dataset: "token_transfers", ShardID: "shard-1",
		PageNo: 2, BlockFrom: 100, BlockTo: 200, PageSize: 10000,
		WireBytes: 1234, DecodedBytes: 5678,
		QueryHash: "qh", NextPageTokenHash: "th",
		RequestStartedAt: time.Now().Add(-time.Second), BodyCompletedAt: time.Now(),
	}
	rows := []json.RawMessage{
		json.RawMessage("{\"blockNumber\":\"0x1\",\"txHash\":\"a\"}"),
		json.RawMessage("{\"blockNumber\":\"0x2\",\"txHash\":\"b\"}"),
	}
	durable, err := s.WritePageWithMeta(meta, rows)
	rawPath, metaPath := durable.RawPath, durable.MetaPath
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rawPath); err != nil {
		t.Fatalf("raw missing: %v", err)
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}

	pages, err := s.ScanShard("job-1", "token_transfers", "shard-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].PageNo != 2 {
		t.Fatalf("scan mismatch: %+v", pages)
	}
	if pages[0].Meta.SHA256 == "" || pages[0].Meta.Rows != 2 {
		t.Fatalf("manifest fields missing: %+v", pages[0].Meta)
	}
	if durable.Meta.SHA256 != pages[0].Meta.SHA256 || durable.Meta.Rows != len(rows) || durable.Meta.DecodedBytes != pages[0].Meta.DecodedBytes {
		t.Fatalf("returned metadata differs from durable manifest: %+v", durable.Meta)
	}
	loaded, err := s.LoadRows(pages[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("rows mismatch: %d", len(loaded))
	}

	// checksum 篡改必须被拒绝
	if err := os.WriteFile(pages[0].RawPath, []byte("[{}]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadRows(pages[0]); err == nil {
		t.Fatal("tampered raw must fail checksum validation")
	}

	if err := s.RemoveShard("job-1", "token_transfers", "shard-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(rawPath)); !os.IsNotExist(err) {
		t.Fatal("shard dir must be removed")
	}
}

func TestListShardsAndUsedBytes(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(Config{Dir: dir, MaxBytes: 1 << 30})
	_, _, err := s.WritePage(PageMeta{JobID: "j1", Dataset: "logs", ShardID: "s1", PageNo: 1}, []json.RawMessage{json.RawMessage("{}")})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.WritePage(PageMeta{JobID: "j1", Dataset: "logs", ShardID: "s2", PageNo: 1}, []json.RawMessage{json.RawMessage("{}")})
	if err != nil {
		t.Fatal(err)
	}
	shards, err := s.ListShards()
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 2 {
		t.Fatalf("expected 2 shard dirs, got %d", len(shards))
	}
	used, err := s.UsedBytes()
	if err != nil {
		t.Fatal(err)
	}
	if used <= 0 {
		t.Fatal("used bytes must be > 0")
	}
	if s.UsedRatio() <= 0 {
		t.Fatal("used ratio must be > 0")
	}
}

func TestWritePageOverwriteIdempotent(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(Config{Dir: dir, MaxBytes: 1 << 30})
	meta := PageMeta{JobID: "j", Dataset: "transactions", ShardID: "s", PageNo: 1}
	_, _, err := s.WritePage(meta, []json.RawMessage{json.RawMessage("{\"a\":1}")})
	if err != nil {
		t.Fatal(err)
	}
	// 重放同一页（kill -9 恢复后 Resume 重抓）必须幂等覆盖，不产生重复文件
	_, _, err = s.WritePage(meta, []json.RawMessage{json.RawMessage("{\"a\":1}")})
	if err != nil {
		t.Fatal(err)
	}
	pages, _ := s.ScanShard("j", "transactions", "s")
	if len(pages) != 1 {
		t.Fatalf("overwrite must keep exactly one page, got %d", len(pages))
	}
}
