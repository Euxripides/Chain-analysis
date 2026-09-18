package main

// Calibration tests for the strict dual-bucket probe classification (stat-strict).
//
// Requirement (root, 2026-09-13): the CLI must be calibrated for NoSuchKey / NoSuchBucket /
// AccessDenied -- "it compiles" is explicitly NOT accepted as calibration. These tests are the
// executable form of that requirement: they pin the exact product-side contract implemented by
// internal/api/export_object_store.go:isObjectNotFoundCode (only object-level codes may be
// reported as a missing object; bucket-level and permission errors must fail closed).
//
// The live negative calibration against real MinIO (missing bucket / wrong credentials) is run
// separately by scripts/s7/b2-raw-only-ui-sample.sh using this same binary, so a fabricated
// NoSuchKey from HEAD on a non-existent bucket cannot pass unnoticed.

import (
	"errors"
	"testing"

	"github.com/minio/minio-go/v7"
)

// TestBucketPresenceVerdict pins the flag semantics that a consumer must be able to trust:
// an absent bucket is a COMPLETED check with absence confirmed, and it is explicitly NOT an
// existing bucket -- reporting bucket_exists_confirmed=true together with BUCKET_MISSING was a
// real self-contradiction found by review on 2026-09-13 and is fixed here.
func TestBucketPresenceVerdict(t *testing.T) {
	visible := map[string]bool{"blockchain-raw": true, "blockchain-exports": true}

	cls, completed, exists, absent := bucketPresenceVerdict(visible, "blockchain-raw")
	if cls != "" || !completed || !exists || absent {
		t.Fatalf("present bucket: got cls=%q completed=%v exists=%v absence=%v; want \"\" true true false", cls, completed, exists, absent)
	}

	cls, completed, exists, absent = bucketPresenceVerdict(visible, "blockchain-absent-probe")
	if cls != probeBucketGone {
		t.Fatalf("absent bucket: classification=%q want %q", cls, probeBucketGone)
	}
	if !completed {
		t.Fatalf("absent bucket: bucket_check_completed must be true (enumeration did complete)")
	}
	if exists {
		t.Fatalf("absent bucket: bucket_exists_confirmed must be FALSE (it contradicts BUCKET_MISSING)")
	}
	if !absent {
		t.Fatalf("absent bucket: bucket_absence_confirmed must be true")
	}
}

func TestClassifyProbeError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil error is a present object", nil, probePresent},
		{"NoSuchKey is object-level missing", minio.ErrorResponse{Code: "NoSuchKey", Message: "The specified key does not exist."}, probeMissing},
		{"NoSuchObject is object-level missing", minio.ErrorResponse{Code: "NoSuchObject", Message: "Object does not exist"}, probeMissing},
		{"NoSuchBucket must NOT be reported as a missing object", minio.ErrorResponse{Code: "NoSuchBucket", Message: "The specified bucket does not exist"}, probeStorageFault},
		{"AccessDenied must NOT be reported as a missing object", minio.ErrorResponse{Code: "AccessDenied", Message: "Access Denied."}, probeStorageFault},
		{"SignatureDoesNotMatch must NOT be reported as a missing object", minio.ErrorResponse{Code: "SignatureDoesNotMatch", Message: "The request signature we calculated does not match"}, probeStorageFault},
		{"unknown/generic error must fail closed", minio.ErrorResponse{Code: "InternalError", Message: "We encountered an internal error"}, probeStorageFault},
		{"non-typed error must fail closed", errors.New("connection reset by peer"), probeStorageFault},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		got := classifyProbeError(tc.err)
		if got != tc.want {
			t.Fatalf("%s: classifyProbeError=%q want %q", tc.name, got, tc.want)
		}
		seen[got] = true
	}
	// The calibration itself must exercise all three distinguishable outcomes, otherwise a
	// "constant" implementation could pass the table above by accident.
	for _, must := range []string{probePresent, probeMissing, probeStorageFault} {
		if !seen[must] {
			t.Fatalf("calibration did not exercise outcome %q", must)
		}
	}
}
