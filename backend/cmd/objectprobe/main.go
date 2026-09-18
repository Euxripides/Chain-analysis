// objectprobe：MinIO 对象故障注入/恢复工具（仅测试环境使用）。
//
// 用于 §27.5 DATA-008：先 backup，再 delete，验证系统不宣称完成且有可操作线索，
// 最后 restore 恢复对象。默认配置从环境变量读取（与 API 相同）。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/minio/minio-go/v7"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
)

func main() {
	action := flag.String("action", "stat", "stat|stat-strict|backup|delete|restore")
	bucket := flag.String("bucket", "", "bucket（默认 config RawBucket）")
	key := flag.String("key", "", "object key（必填）")
	file := flag.String("file", "", "backup/restore 本地文件")
	out := flag.String("out", "", "stat-strict: 把 JSON 实测结果同时写入该文件")
	timeoutSpec := flag.String("timeout", "30s", "stat-strict: 整个只读探测的有界超时（如 30s/2s）")
	flag.Parse()
	if *key == "" {
		fmt.Fprintln(os.Stderr, "key required")
		os.Exit(2)
	}
	cfg := config.Load()
	// stat-strict 必须**完全只读**：rawstore.New 会 EnsureBucket->MakeBucket，用它做存在性
	// 探针会在桶名写错时**创建**桶并掩盖部署/权限故障（2026-09-13 实测事故，见 statstrict.go）。
	// 因此在构造任何会建桶的存储对象之前就分派。
	if *action == "stat-strict" {
		os.Exit(runStatStrict(cfg, *key, *out, *timeoutSpec))
	}
	b := *bucket
	if b == "" {
		b = cfg.RawBucket
	}
	ctx := context.Background()
	store, err := rawstore.New(ctx, cfg.MinioEndpoint, cfg.MinioAccessKey, cfg.MinioSecretKey, cfg.MinioSecure, b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "minio init: %v\n", err)
		os.Exit(1)
	}
	client := store.Client()
	switch *action {
	case "stat":
		info, err := client.StatObject(ctx, b, *key, minio.StatObjectOptions{})
		if err != nil {
			fmt.Println("exists=false")
			os.Exit(3)
		}
		fmt.Printf("exists=true size=%d etag=%s\n", info.Size, info.ETag)
	case "backup":
		if *file == "" {
			fmt.Fprintln(os.Stderr, "file required")
			os.Exit(2)
		}
		obj, err := client.GetObject(ctx, b, *key, minio.GetObjectOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "get: %v\n", err)
			os.Exit(1)
		}
		defer obj.Close()
		out, err := os.Create(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create: %v\n", err)
			os.Exit(1)
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(out, h), obj)
		out.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "copy: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("backup=%s size=%d sha256=%s\n", *file, n, hex.EncodeToString(h.Sum(nil)))
	case "delete":
		if err := client.RemoveObject(ctx, b, *key, minio.RemoveObjectOptions{}); err != nil {
			fmt.Fprintf(os.Stderr, "remove: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("deleted=true")
	case "restore":
		if *file == "" {
			fmt.Fprintln(os.Stderr, "file required")
			os.Exit(2)
		}
		info, err := client.FPutObject(ctx, b, *key, *file, minio.PutObjectOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "put: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("restored=true size=%d\n", info.Size)
	default:
		fmt.Fprintln(os.Stderr, "unknown action")
		os.Exit(2)
	}
}
