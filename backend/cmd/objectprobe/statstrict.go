package main

// stat-strict: ADR-019 dual-bucket STRICT object existence probe (additive action of the
// existing, already-audited MinIO test tool; every other action is unchanged).
//
// FAILURE THAT FORCED THIS DESIGN (2026-09-13 live calibration, output preserved):
//   Probing a bucket name that did not exist reported "bucket_exists": true and then a
//   fabricated "NoSuchKey" -> the calibration could not distinguish a missing bucket from a
//   missing object. Root cause established from evidence, not assumption: this tool obtained
//   its client from rawstore.New(), which calls EnsureBucket() and CREATES the bucket with
//   MakeBucket() when it is absent (internal/rawstore/raw.go:32-36 and :53-64). The probe
//   therefore created the very bucket it was asked to test
//   (bucket blockchain-raw-absent-calibration-20260913, CreationDate 2026-09-13T13:31:52.878Z,
//   0 objects, still present and deliberately left in place as evidence).
//   An existence probe with a create side effect can never fail closed.
//
// Rules implemented here:
//   1) No bucket-creating helper: the client is built directly with minio-go + static
//      credentials, and this action is dispatched BEFORE any rawstore construction
//      (see main.go), so the probe is strictly read-only.
//   2) Bucket existence is confirmed by ListBuckets -- the authoritative set of buckets these
//      credentials can enumerate -- and the requested name must match EXACTLY.
//   3) Fail-closed mapping:
//        enumeration error (denied / network / TLS / parse) -> BUCKET_CHECK_ERROR, exit 4
//        name absent from the enumeration                    -> BUCKET_MISSING,     exit 4
//        StatObject code NoSuchKey / NoSuchObject            -> OBJECT_MISSING,     exit 0
//        any other StatObject error (AccessDenied, NoSuchBucket, 5xx, transport)
//                                                            -> STORAGE_FAULT,      exit 4
//      Unknown, permission, network, TLS and parse errors are NEVER downgraded to "missing".
//   4) Credential scope is reported (masked access-key id + source + whether ListBuckets was
//      authorised) so evidence states which identity produced it; no secret is ever printed.
//      Application *runtime* logic is a separate gate: it is verified through the product
//      permission model (runtime DB role + object-scope checks), not by this helper.
//
// Exit code: 0 only when bucket existence was positively confirmed by enumeration AND both
// buckets resolved to OBJECT_PRESENT / OBJECT_MISSING; 4 for every fail-closed outcome.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

const (
	probePresent      = "OBJECT_PRESENT"
	probeMissing      = "OBJECT_MISSING"
	probeBucketGone   = "BUCKET_MISSING"
	probeStorageFault = "STORAGE_FAULT"
	probeBucketErr    = "BUCKET_CHECK_ERROR"
)

// classifyProbeError is the pure StatObject classification, unit-tested in statstrict_test.go
// against NoSuchKey / NoSuchBucket / AccessDenied / SignatureDoesNotMatch / generic errors.
func classifyProbeError(err error) string {
	if err == nil {
		return probePresent
	}
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NoSuchObject":
		return probeMissing
	default:
		return probeStorageFault
	}
}

// maskAccessKey reports enough identity to be auditable without leaking the credential.
func maskAccessKey(id string) string {
	if id == "" {
		return "(empty)"
	}
	if len(id) <= 4 {
		return "****"
	}
	return id[:4] + "****"
}

// listVisibleBuckets enumerates the buckets these credentials can see. Any failure is returned
// as an error so the caller can fail closed (never treated as "bucket absent").
func listVisibleBuckets(ctx context.Context, client *minio.Client) (map[string]bool, []string, error) {
	infos, err := client.ListBuckets(ctx)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(infos))
	for _, b := range infos {
		names = append(names, b.Name)
	}
	sort.Strings(names)
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set, names, nil
}

// bucketPresenceVerdict maps an enumeration result onto presence flags.
//
// The two states are deliberately NOT expressed by a single boolean: an absent bucket is a
// bucket check that COMPLETED (absence is authoritative) but it is NOT an existing bucket.
// Reporting bucket_exists_confirmed=true together with classification=BUCKET_MISSING would be
// self-contradictory and could mislead a consumer that only looks at that boolean.
//
// returns (classification, bucketCheckCompleted, bucketExistsConfirmed, bucketAbsenceConfirmed)
// classification is empty when the bucket is present and the object probe must continue.
func bucketPresenceVerdict(visible map[string]bool, bucket string) (string, bool, bool, bool) {
	if visible[bucket] {
		return "", true, true, false
	}
	return probeBucketGone, true, false, true
}

// probeBucketStrict records observed facts for one bucket/key pair. It never mutates anything.
func probeBucketStrict(ctx context.Context, client *minio.Client, visible map[string]bool, role, bucket, key string) map[string]any {
	res := map[string]any{
		"role": role, "bucket": bucket, "key": key,
		"bucket_check_completed":   false,
		"bucket_exists_confirmed":  false,
		"bucket_absence_confirmed": false,
		"classification":           probeBucketErr,
		"object_present":           false, "object_size": int64(0),
	}
	presence, checkCompleted, existsConfirmed, absenceConfirmed := bucketPresenceVerdict(visible, bucket)
	res["bucket_check_completed"] = checkCompleted
	res["bucket_exists_confirmed"] = existsConfirmed
	res["bucket_absence_confirmed"] = absenceConfirmed
	if presence != "" {
		res["classification"] = presence
		return res
	}
	info, statErr := client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if statErr != nil {
		resp := minio.ToErrorResponse(statErr)
		res["classification"] = classifyProbeError(statErr)
		res["error_code"] = resp.Code
		res["error_message"] = resp.Message
		return res
	}
	res["classification"] = probePresent
	res["object_present"] = true
	res["object_size"] = info.Size
	res["etag"] = info.ETag
	return res
}

// runStatStrict probes RAW_BUCKET and EXPORT_BUCKET for one key and returns the process exit code.
//
// The whole probe runs under a bounded context: a read-only probe must never hang forever on a
// network fault, and a timeout is a fail-closed outcome (BUCKET_CHECK_ERROR, exit 4), never
// "object missing".
func runStatStrict(cfg *config.Config, key, out, timeoutSpec string) int {
	raw := strings.TrimSpace(cfg.RawBucket)
	exportBucket := strings.TrimSpace(cfg.ExportBucket)
	if raw == "" || exportBucket == "" {
		fmt.Fprintln(os.Stderr, "RAW_BUCKET and EXPORT_BUCKET must both be configured")
		return 4
	}
	if raw == exportBucket {
		fmt.Fprintf(os.Stderr, "RAW_BUCKET and EXPORT_BUCKET must differ (both %q)\n", raw)
		return 4
	}
	timeout, terr := time.ParseDuration(timeoutSpec)
	if terr != nil || timeout <= 0 {
		fmt.Fprintf(os.Stderr, "timeout must be a positive duration (got %q)\n", timeoutSpec)
		return 4
	}
	client, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: cfg.MinioSecure,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "minio client: %v\n", err)
		return 4
	}
	payload := map[string]any{
		"key": key, "raw_bucket": raw, "export_bucket": exportBucket,
		"endpoint":                   cfg.MinioEndpoint,
		"timeout":                    timeout.String(),
		"export_write_bucket_config": cfg.ExportWriteBucket,
		"export_read_order_config":   cfg.ExportReadOrder,
		"credential_scope": map[string]any{
			"access_key_id_masked":   maskAccessKey(cfg.MinioAccessKey),
			"source":                 "MINIO_ROOT_USER/MINIO_ROOT_PASSWORD via config.Load (controlled MinIO admin identity)",
			"purpose":                "acceptance helper only; product runtime authorisation is verified separately by the product permission model",
			"no_secret_material_out": true,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	visible, names, lerr := listVisibleBuckets(ctx, client)
	if lerr != nil {
		resp := minio.ToErrorResponse(lerr)
		payload["list_buckets_authorized"] = false
		payload["list_buckets_error_code"] = resp.Code
		payload["list_buckets_error_message"] = resp.Message
		payload["list_buckets_error_text"] = lerr.Error()
		if resp.Code == "" {
			// transport/TLS/timeout/parse failure: no S3 error code is available at all.
			payload["list_buckets_error_kind"] = "transport_or_timeout"
		} else {
			payload["list_buckets_error_kind"] = "s3_error_response"
		}
		payload["visible_buckets"] = []string{}
		payload["results"] = []map[string]any{}
		payload["verdict"] = probeBucketErr
		return emitProbe(payload, out, 4)
	}
	payload["list_buckets_authorized"] = true
	payload["visible_buckets"] = names
	results := []map[string]any{
		probeBucketStrict(ctx, client, visible, "raw", raw, key),
		probeBucketStrict(ctx, client, visible, "export", exportBucket, key),
	}
	worst := 0
	for _, res := range results {
		switch res["classification"] {
		case probePresent, probeMissing:
		default:
			worst = 4
		}
	}
	verdict := "CONFIRMED"
	if worst != 0 {
		verdict = "FAIL_CLOSED"
	}
	payload["results"] = results
	payload["verdict"] = verdict
	return emitProbe(payload, out, worst)
}

// emitProbe writes the observed facts and returns the exit code.
func emitProbe(payload map[string]any, out string, code int) int {
	blob, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		return 4
	}
	fmt.Println(string(blob))
	if out != "" {
		if err := os.WriteFile(out, append(blob, '\n'), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write out: %v\n", err)
			return 4
		}
	}
	return code
}
