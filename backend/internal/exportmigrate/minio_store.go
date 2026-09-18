package exportmigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
)

// MinIOStore 是 ObjectStore 的真实实现。
//
// 关键语义（与 internal/api/export_object_store.go 同一口径）：
//   - 只有对象级错误码 NoSuchKey/NoSuchObject 才表示"对象不存在"；
//   - NoSuchBucket/AccessDenied/网络/5xx 一律返回 error（fail closed）；
//   - 内容 SHA-256 **必须**流式读取对象计算（ETag 对 multipart 语义不同，不能作为字节证明）。
type MinIOStore struct {
	client *minio.Client
}

func NewMinIOStore(client *minio.Client) *MinIOStore { return &MinIOStore{client: client} }

// isObjectMissingCode 只接受对象级缺失码。
func isObjectMissingCode(err error) bool {
	if err == nil {
		return false
	}
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NoSuchObject":
		return true
	default:
		return false
	}
}

func (m *MinIOStore) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return m.client.BucketExists(ctx, bucket)
}

func (m *MinIOStore) Stat(ctx context.Context, bucket, key string) (int64, bool, error) {
	info, err := m.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return info.Size, true, nil
	}
	if isObjectMissingCode(err) {
		// HEAD 语义下"桶不存在"会被库兜底成 NoSuchKey，因此这里再确认一次桶存在，
		// 否则会把部署故障误判成"对象缺失"并跳过迁移。
		exists, berr := m.client.BucketExists(ctx, bucket)
		if berr != nil {
			return 0, false, fmt.Errorf("stat %s/%s: %w (and bucket check failed: %v)", bucket, key, err, berr)
		}
		if !exists {
			return 0, false, fmt.Errorf("bucket %s does not exist while stating %s", bucket, key)
		}
		return 0, false, nil
	}
	return 0, false, fmt.Errorf("stat %s/%s: %w", bucket, key, err)
}

// SHA256 流式复算对象内容哈希：先 Stat 暴露惰性错误，再整块读出并校验字节数一致。
func (m *MinIOStore) SHA256(ctx context.Context, bucket, key string) (string, int64, error) {
	obj, err := m.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil && !isObjectMissingCode(err) {
		return "", 0, fmt.Errorf("get %s/%s: %w", bucket, key, err)
	}
	if err != nil {
		return "", 0, fmt.Errorf("object %s/%s does not exist", bucket, key)
	}
	defer obj.Close()
	info, err := obj.Stat()
	if err != nil {
		if isObjectMissingCode(err) {
			return "", 0, fmt.Errorf("object %s/%s does not exist", bucket, key)
		}
		return "", 0, fmt.Errorf("stat %s/%s: %w", bucket, key, err)
	}
	h := sha256.New()
	buf := make([]byte, 1<<20)
	n, err := io.CopyBuffer(h, obj, buf)
	if err != nil {
		return "", 0, fmt.Errorf("read %s/%s: %w", bucket, key, err)
	}
	if n != info.Size {
		return "", 0, fmt.Errorf("short read %s/%s: got %d bytes, object size %d", bucket, key, n, info.Size)
	}
	return hex.EncodeToString(h.Sum(nil)), info.Size, nil
}

// Copy 流式复制（读源 → 写目标），返回写入字节数；不做任何删除或移动。
func (m *MinIOStore) Copy(ctx context.Context, fromBucket, fromKey, toBucket, toKey, contentType string) (int64, error) {
	src, err := m.client.GetObject(ctx, fromBucket, fromKey, minio.GetObjectOptions{})
	if err != nil && !isObjectMissingCode(err) {
		return 0, fmt.Errorf("get source %s/%s: %w", fromBucket, fromKey, err)
	}
	if err != nil {
		return 0, fmt.Errorf("source object %s/%s does not exist", fromBucket, fromKey)
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		if isObjectMissingCode(err) {
			return 0, fmt.Errorf("source object %s/%s does not exist", fromBucket, fromKey)
		}
		return 0, fmt.Errorf("stat source %s/%s: %w", fromBucket, fromKey, err)
	}
	counted := &countingReader{r: src}
	if _, err := m.client.PutObject(ctx, toBucket, toKey, counted, info.Size, minio.PutObjectOptions{ContentType: contentType}); err != nil {
		return 0, fmt.Errorf("put destination %s/%s: %w", toBucket, toKey, err)
	}
	if counted.n != info.Size {
		return 0, fmt.Errorf("copy size mismatch %s/%s: wrote %d, source size %d", toBucket, toKey, counted.n, info.Size)
	}
	return counted.n, nil
}

// countingReader 统计真正从源读走的字节数（SDK 返回的 size 不一定是对象字节数）。
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
