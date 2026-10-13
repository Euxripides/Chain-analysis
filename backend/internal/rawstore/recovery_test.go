package rawstore

import (
	"context"
	"errors"
	"fmt"
	"github.com/minio/minio-go/v7"
	"testing"
)

func TestRecoveryArchiveErrorClassification(t *testing.T) {
	for _, err := range []error{minio.ErrorResponse{Code: "NoSuchKey", StatusCode: 404}, fmt.Errorf("restore: %w", ErrCorruptRawObject)} {
		if !UnrecoverableObject(err) {
			t.Fatalf("missing/corrupt object endlessly retried: %v", err)
		}
	}
	for _, err := range []error{minio.ErrorResponse{Code: "ServiceUnavailable", StatusCode: 503}, minio.ErrorResponse{Code: "AccessDenied", StatusCode: 403}, context.DeadlineExceeded, errors.New("transport unavailable")} {
		if UnrecoverableObject(err) {
			t.Fatalf("temporary outage treated as data loss: %v", err)
		}
	}
}
