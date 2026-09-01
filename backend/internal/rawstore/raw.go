// Package rawstore：Ankr 原始 JSON 落盘层（MinIO，JSON + gzip，永久保留）。
package rawstore

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var ErrInvalidRange = errors.New("invalid object range")

type Store struct {
	client *minio.Client
	bucket string
}

func New(ctx context.Context, endpoint, accessKey, secretKey string, secure bool, bucket string) (*Store, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	s := &Store{client: client, bucket: bucket}
	if err := s.EnsureBucket(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// EnsureNamedBucket 确保任意命名 bucket 存在（导出/备份等）。
func (s *Store) EnsureNamedBucket(ctx context.Context, name string) error {
	exists, err := s.client.BucketExists(ctx, name)
	if err != nil {
		return fmt.Errorf("bucket exists: %w", err)
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, name, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("make bucket %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("bucket exists: %w", err)
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("make bucket %s: %w", s.bucket, err)
		}
	}
	return nil
}

func (s *Store) Bucket() string { return s.bucket }

func (s *Store) Put(ctx context.Context, key string, data []byte, contentType string) (int64, error) {
	info, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return 0, fmt.Errorf("put %s: %w", key, err)
	}
	return info.Size, nil
}

// PutGzip JSON + gzip（不要长期保存裸 JSON）。
func (s *Store) PutGzip(ctx context.Context, key string, data []byte) (int64, error) {
	var buf bytes.Buffer
	gw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if _, err := gw.Write(data); err != nil {
		return 0, err
	}
	if err := gw.Close(); err != nil {
		return 0, err
	}
	return s.Put(ctx, key, buf.Bytes(), "application/gzip")
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(obj); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// OpenRange 按字节区间流式读取（HTTP Range 断点续传，§57）。
func (s *Store) OpenRange(ctx context.Context, key string, start, end int64) (io.ReadCloser, int64, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, 0, err
	}
	if start < 0 || start >= info.Size {
		obj.Close()
		return nil, info.Size, fmt.Errorf("%w: start %d outside object size %d", ErrInvalidRange, start, info.Size)
	}
	if end < 0 || end >= info.Size {
		end = info.Size - 1
	}
	if end < start {
		obj.Close()
		return nil, info.Size, fmt.Errorf("%w: end %d before start %d", ErrInvalidRange, end, start)
	}
	if _, err := obj.Seek(start, io.SeekStart); err != nil {
		obj.Close()
		return nil, info.Size, err
	}
	return &limitedReadCloser{
		Reader: io.LimitReader(obj, end-start+1),
		Closer: obj,
	}, info.Size, nil
}

type limitedReadCloser struct {
	io.Reader
	io.Closer
}

// Open 流式读取对象（导出下载用，不整块进内存）。
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, 0, err
	}
	return obj, info.Size, nil
}

func (s *Store) Stat(ctx context.Context, key string) (int64, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, err
	}
	return info.Size, nil
}

func (s *Store) PresignedGet(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, expiry, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *Store) Client() *minio.Client { return s.client }
