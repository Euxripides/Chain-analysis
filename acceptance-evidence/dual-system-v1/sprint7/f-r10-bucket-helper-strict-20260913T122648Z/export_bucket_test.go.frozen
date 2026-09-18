package config

// F-R10-BUCKET / ADR-019：导出桶配置必须显式合法，非法值一律启动失败（fail closed）。
// 目的：杜绝"以为切了写桶其实还在写旧桶""以为在双读其实只读了一个桶"这类伪证据。

import "testing"

// baseValidExportConfig 用 Load() 取得一份**完整合法**的配置（含 Ankr/RPC 等默认值），
// 再按用例覆盖导出桶相关字段——避免手写不完整结构体造成与被测逻辑无关的失败。
func baseValidExportConfig(t *testing.T) *Config {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://test:test@127.0.0.1:5432/testdb?sslmode=disable")
	t.Setenv("CLICKHOUSE_URL", "clickhouse://test:test@127.0.0.1:9000/testdb")
	t.Setenv("MINIO_ROOT_PASSWORD", "test-only-minio-secret")
	t.Setenv("RAW_BUCKET", "blockchain-raw")
	t.Setenv("EXPORT_BUCKET", "blockchain-exports")
	t.Setenv("EXPORT_WRITE_BUCKET", "raw")
	t.Setenv("EXPORT_READ_ORDER", "export,raw")
	return Load()
}

func TestValidateAcceptsDefaultExportBucketConfig(t *testing.T) {
	if err := baseValidExportConfig(t).Validate(); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
	switched := baseValidExportConfig(t)
	switched.ExportWriteBucket = "export"
	switched.ExportReadOrder = "raw,export"
	if err := switched.Validate(); err != nil {
		t.Fatalf("switched config must be valid: %v", err)
	}
}

func TestValidateRejectsBadExportWriteBucket(t *testing.T) {
	for _, value := range []string{"", "RAW", "exports", "blockchain-exports", "none", " export", "export "} {
		cfg := baseValidExportConfig(t)
		cfg.ExportWriteBucket = value
		if err := cfg.Validate(); err == nil {
			t.Fatalf("EXPORT_WRITE_BUCKET=%q must be rejected", value)
		}
	}
}

func TestValidateRejectsBadExportReadOrder(t *testing.T) {
	for _, value := range []string{
		"",              // 空：无法确定优先级
		"export",        // 缺 raw：迁移/回滚期间旧对象读不到
		"raw",           // 缺 export：切写后新对象读不到
		"export,raw,",   // 末尾空项
		"export,export", // 重复
		"raw,nosuch",    // 未知 token
		"export;raw",    // 分隔符错误
		"export, raw",   // 项内空白
	} {
		cfg := baseValidExportConfig(t)
		cfg.ExportReadOrder = value
		if err := cfg.Validate(); err == nil {
			t.Fatalf("EXPORT_READ_ORDER=%q must be rejected", value)
		}
	}
}

func TestValidateRejectsBucketNameProblems(t *testing.T) {
	empty := baseValidExportConfig(t)
	empty.ExportBucket = "  "
	if err := empty.Validate(); err == nil {
		t.Fatal("empty EXPORT_BUCKET must be rejected")
	}
	emptyRaw := baseValidExportConfig(t)
	emptyRaw.RawBucket = ""
	if err := emptyRaw.Validate(); err == nil {
		t.Fatal("empty RAW_BUCKET must be rejected")
	}
	same := baseValidExportConfig(t)
	same.ExportBucket = same.RawBucket
	if err := same.Validate(); err == nil {
		t.Fatal("RAW_BUCKET == EXPORT_BUCKET must be rejected")
	}
}

func TestLoadDefaultsKeepLegacyBehaviour(t *testing.T) {
	t.Setenv("EXPORT_WRITE_BUCKET", "")
	t.Setenv("EXPORT_READ_ORDER", "")
	cfg := Load()
	if cfg.ExportWriteBucket != "raw" {
		t.Fatalf("default EXPORT_WRITE_BUCKET must stay raw, got %q", cfg.ExportWriteBucket)
	}
	if cfg.ExportReadOrder != "export,raw" {
		t.Fatalf("default EXPORT_READ_ORDER must be export,raw, got %q", cfg.ExportReadOrder)
	}
}
