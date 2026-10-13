// Package spool 实现 Local Durable Spool（文档 §37-§43）：
// Provider Response → 本地 SSD 落盘（raw + manifest + fsync）→ 立即下一页。
// 对象存储（MinIO/R2）上传与 ClickHouse Ingest 全部异步，不阻塞 Fetch Critical Path。
package spool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config Spool 配置（§38/§72）。
type Config struct {
	Dir      string
	MaxBytes int64
}

// PageMeta 页面级 Manifest（§39）。
type PageMeta struct {
	JobID             string    `json:"job_id"`
	Dataset           string    `json:"dataset"`
	ShardID           string    `json:"shard_id"`
	PageNo            int       `json:"page_no"`
	BlockFrom         int64     `json:"block_from"`
	BlockTo           int64     `json:"block_to"`
	PageSize          int       `json:"page_size"`
	WireBytes         int64     `json:"wire_bytes"`
	DecodedBytes      int64     `json:"decoded_bytes"`
	SHA256            string    `json:"sha256"`
	QueryHash         string    `json:"query_hash"`
	NextPageTokenHash string    `json:"next_page_token_hash"`
	Rows              int       `json:"rows"`
	RequestStartedAt  time.Time `json:"request_started_at"`
	BodyCompletedAt   time.Time `json:"body_completed_at"`
	ReceivedAt        time.Time `json:"received_at"`
}

type QuarantineMeta struct {
	JobID             string    `json:"job_id"`
	ShardID           string    `json:"shard_id"`
	Dataset           string    `json:"dataset"`
	PageNo            int       `json:"page_no"`
	Attempt           int       `json:"attempt"`
	RequestID         string    `json:"request_id"`
	LeaseEpoch        int64     `json:"lease_epoch"`
	RangeStart        uint64    `json:"range_start"`
	RangeEndExclusive uint64    `json:"range_end_exclusive"`
	RequestFromBlock  uint64    `json:"request_from_block"`
	RequestToBlock    uint64    `json:"request_to_block_inclusive"`
	RequestPageSize   int       `json:"request_page_size"`
	PageTokenHash     string    `json:"page_token_hash"`
	ResponseRows      int       `json:"response_rows"`
	ResponseMinBlock  uint64    `json:"response_min_block"`
	ResponseMaxBlock  uint64    `json:"response_max_block"`
	OutOfRangeRows    int       `json:"out_of_range_rows"`
	ResponseSHA256    string    `json:"response_sha256"`
	StartedAt         time.Time `json:"started_at"`
	BodyCompleteAt    time.Time `json:"body_complete_at"`
	QuarantinedAt     time.Time `json:"quarantined_at"`
}

// WriteQuarantine stores an integrity-violating response outside the normal
// durable page tree so recovery can never ingest it.
func (s *Store) WriteQuarantine(meta QuarantineMeta, raw []byte) (string, error) {
	dir := filepath.Join(s.cfg.Dir, "quarantine", meta.JobID, meta.ShardID, fmt.Sprintf("page-%06d", meta.PageNo), fmt.Sprintf("attempt-%03d", meta.Attempt))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	meta.ResponseSHA256 = hex.EncodeToString(sum[:])
	meta.QuarantinedAt = time.Now()
	if err := writeFileDurable(filepath.Join(dir, "response.json"), raw); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeFileDurable(filepath.Join(dir, "provenance.json"), b); err != nil {
		return "", err
	}
	if err := fsyncDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// PageFile 扫描到的页面（恢复用）。
type PageFile struct {
	PageNo   int
	RawPath  string
	MetaPath string
	Meta     PageMeta
}

// ShardDir 扫描到的 Shard 目录（恢复用）。
type ShardDir struct {
	JobID   string
	Dataset string
	ShardID string
	Path    string
}

type Store struct {
	cfg Config
}

func New(cfg Config) (*Store, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("spool dir must be set")
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create spool dir: %w", err)
	}
	return &Store{cfg: cfg}, nil
}

func (s *Store) Dir() string { return s.cfg.Dir }

func (s *Store) shardDir(jobID, dataset, shardID string) string {
	return filepath.Join(s.cfg.Dir, "jobs", jobID, dataset, shardID)
}

func rawName(pageNo int) string  { return fmt.Sprintf("page-%06d.raw", pageNo) }
func metaName(pageNo int) string { return fmt.Sprintf("page-%06d.manifest.json", pageNo) }

// WritePage 写 raw + manifest 并 fsync（§40：只有全部完成才是 DURABLE）。
// 返回 raw 与 manifest 的绝对路径。
func (s *Store) WritePage(meta PageMeta, rows []json.RawMessage) (rawPath, metaPath string, err error) {
	page, err := s.WritePageWithMeta(meta, rows)
	return page.RawPath, page.MetaPath, err
}

// WritePageWithMeta returns the exact manifest persisted with the durable bytes.
// Callers must use this SHA and row count for checkpoint and archive fencing.
func (s *Store) WritePageWithMeta(meta PageMeta, rows []json.RawMessage) (PageFile, error) {
	if meta.PageNo < 1 {
		return PageFile{}, fmt.Errorf("page_no must be >= 1")
	}
	dir := s.shardDir(meta.JobID, meta.Dataset, meta.ShardID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return PageFile{}, err
	}
	if meta.ReceivedAt.IsZero() {
		meta.ReceivedAt = time.Now()
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return PageFile{}, err
	}
	sum := sha256.Sum256(raw)
	meta.SHA256 = hex.EncodeToString(sum[:])
	meta.Rows = len(rows)
	meta.DecodedBytes = int64(len(raw))

	rawPath := filepath.Join(dir, rawName(meta.PageNo))
	metaPath := filepath.Join(dir, metaName(meta.PageNo))
	if err := writeFileDurable(rawPath, raw); err != nil {
		return PageFile{}, fmt.Errorf("write raw page: %w", err)
	}
	mb, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return PageFile{}, err
	}
	if err := writeFileDurable(metaPath, mb); err != nil {
		return PageFile{}, fmt.Errorf("write page manifest: %w", err)
	}
	if err := fsyncDir(dir); err != nil {
		return PageFile{}, fmt.Errorf("fsync shard dir: %w", err)
	}
	return PageFile{PageNo: meta.PageNo, RawPath: rawPath, MetaPath: metaPath, Meta: meta}, nil
}

// writeFileDurable tmp + fsync + rename（崩溃安全：绝不留下半页）。
func writeFileDurable(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func fsyncDir(dir string) error {
	// Windows does not support syncing a directory handle and returns access
	// denied even after both files were individually fsynced and atomically
	// renamed. Linux keeps the directory fsync durability guarantee.
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// LoadRows 读取页面 raw JSON 行（校验 checksum 不一致即报错）。
func (s *Store) LoadRows(pf PageFile) ([]json.RawMessage, error) {
	data, err := os.ReadFile(pf.RawPath)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if pf.Meta.SHA256 != "" && hex.EncodeToString(sum[:]) != pf.Meta.SHA256 {
		return nil, fmt.Errorf("page %d checksum mismatch: manifest %s actual %s",
			pf.PageNo, pf.Meta.SHA256, hex.EncodeToString(sum[:]))
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("page %d raw corrupt: %w", pf.PageNo, err)
	}
	return rows, nil
}

// ScanShard 列出某 Shard 的全部页面（按 page_no 升序）。
func (s *Store) ScanShard(jobID, dataset, shardID string) ([]PageFile, error) {
	dir := s.shardDir(jobID, dataset, shardID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []PageFile
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "page-") || !strings.HasSuffix(name, ".raw") {
			continue
		}
		numStr := strings.TrimSuffix(strings.TrimPrefix(name, "page-"), ".raw")
		n, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		pf := PageFile{PageNo: n, RawPath: filepath.Join(dir, name), MetaPath: filepath.Join(dir, metaName(n))}
		if mb, err := os.ReadFile(pf.MetaPath); err == nil {
			_ = json.Unmarshal(mb, &pf.Meta)
		}
		out = append(out, pf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PageNo < out[j].PageNo })
	return out, nil
}

// ListShards 扫描全部 Shard 目录（kill -9 恢复用，§43）。
func (s *Store) ListShards() ([]ShardDir, error) {
	root := filepath.Join(s.cfg.Dir, "jobs")
	var out []ShardDir
	jobDirs, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, jd := range jobDirs {
		if !jd.IsDir() {
			continue
		}
		dsDirs, err := os.ReadDir(filepath.Join(root, jd.Name()))
		if err != nil {
			continue
		}
		for _, dd := range dsDirs {
			if !dd.IsDir() {
				continue
			}
			shDirs, err := os.ReadDir(filepath.Join(root, jd.Name(), dd.Name()))
			if err != nil {
				continue
			}
			for _, sd := range shDirs {
				if !sd.IsDir() {
					continue
				}
				out = append(out, ShardDir{
					JobID: jd.Name(), Dataset: dd.Name(), ShardID: sd.Name(),
					Path: filepath.Join(root, jd.Name(), dd.Name(), sd.Name()),
				})
			}
		}
	}
	return out, nil
}

// RemoveShard 删除整个 Shard 目录（Ingest + Upload 确认后清理）。
func (s *Store) RemoveShard(jobID, dataset, shardID string) error {
	return os.RemoveAll(s.shardDir(jobID, dataset, shardID))
}

// RemovePage 删除单页（Ingest 后即可释放，水位只统计未清理部分）。
func (s *Store) RemovePage(rawPath, metaPath string) {
	_ = os.Remove(rawPath)
	_ = os.Remove(metaPath)
}

// UsedBytes 统计当前占用（§72 水位计算）。
func (s *Store) UsedBytes() (int64, error) {
	var total int64
	err := filepath.Walk(s.cfg.Dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// UsedRatio 使用比例（0~1）。
func (s *Store) UsedRatio() float64 {
	used, err := s.UsedBytes()
	if err != nil || s.cfg.MaxBytes <= 0 {
		return 0
	}
	return float64(used) / float64(s.cfg.MaxBytes)
}
