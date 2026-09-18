package exportmigrate

// 隔离负向校准（单测层）：用内存假件证明迁移引擎在**冲突 key、缺 PG sha、非 404 错误、
// 中断续跑、重复运行、dry-run** 六类场景下的行为，全部不接触真实对象存储。
// 真实 MinIO 侧的同类校准另在 VM 隔离桶执行（见 sprint7 证据目录）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

type fakeStore struct {
	buckets map[string]bool
	objects map[string]map[string]string // bucket -> key -> body
	statErr map[string]error             // "bucket/key" -> forced non-404 error
	shaErr  map[string]error             // "bucket/key" -> forced error on read-back
	copies  []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		buckets: map[string]bool{"src-bucket": true, "dst-bucket": true},
		objects: map[string]map[string]string{"src-bucket": {}, "dst-bucket": {}},
		statErr: map[string]error{}, shaErr: map[string]error{},
	}
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (f *fakeStore) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return f.buckets[bucket], nil
}

func (f *fakeStore) Stat(ctx context.Context, bucket, key string) (int64, bool, error) {
	if err, ok := f.statErr[bucket+"/"+key]; ok {
		return 0, false, err
	}
	body, ok := f.objects[bucket][key]
	if !ok {
		return 0, false, nil
	}
	return int64(len(body)), true, nil
}

func (f *fakeStore) SHA256(ctx context.Context, bucket, key string) (string, int64, error) {
	if err, ok := f.shaErr[bucket+"/"+key]; ok {
		return "", 0, err
	}
	body, ok := f.objects[bucket][key]
	if !ok {
		return "", 0, fmt.Errorf("object %s/%s does not exist", bucket, key)
	}
	return hashOf(body), int64(len(body)), nil
}

func (f *fakeStore) Copy(ctx context.Context, fromBucket, fromKey, toBucket, toKey, contentType string) (int64, error) {
	body, ok := f.objects[fromBucket][fromKey]
	if !ok {
		return 0, fmt.Errorf("source %s/%s missing", fromBucket, fromKey)
	}
	f.copies = append(f.copies, fromBucket+"/"+fromKey+" -> "+toBucket+"/"+toKey)
	f.objects[toBucket][toKey] = body
	return int64(len(body)), nil
}

type memLedger struct {
	rows   map[string]Row
	writes int
}

func newMemLedger() *memLedger { return &memLedger{rows: map[string]Row{}} }

func (l *memLedger) key(exportID, toBucket string) string { return exportID + "|" + toBucket }

func (l *memLedger) Get(ctx context.Context, exportID, toBucket string) (*Row, error) {
	if row, ok := l.rows[l.key(exportID, toBucket)]; ok {
		copied := row
		return &copied, nil
	}
	return nil, nil
}

func (l *memLedger) Upsert(ctx context.Context, row Row) error {
	l.writes++
	if prev, ok := l.rows[l.key(row.ExportID, row.ToBucket)]; ok {
		row.Attempts = prev.Attempts + 1
	} else {
		row.Attempts = 1
	}
	l.rows[l.key(row.ExportID, row.ToBucket)] = row
	return nil
}

func (l *memLedger) List(ctx context.Context, toBucket string) ([]Row, error) {
	var out []Row
	for _, row := range l.rows {
		if row.ToBucket == toBucket {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObjectRef < out[j].ObjectRef })
	return out, nil
}

func newEngine(store *fakeStore, ledger *memLedger) *Engine {
	return &Engine{Store: store, Ledger: ledger, FromBucket: "src-bucket", ToBucket: "dst-bucket"}
}

// seed 使用**按 key 唯一**的 export_id：账本主键是 (export_id, to_bucket)，
// 生产里一个导出记录对应一个对象；测试若复用同一个 id 会把多行压成一行（不真实）。
func seed(store *fakeStore, key, body string) Item {
	store.objects["src-bucket"][key] = body
	sum := sha256.Sum256([]byte(key))
	id := hex.EncodeToString(sum[:16])
	return Item{
		ExportID:  id[0:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:32],
		ObjectRef: key, PGSHA256: hashOf(body),
	}
}

func TestDryRunCopiesNothingAndWritesNothing(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/a.csv", "hello world")
	eng := newEngine(store, ledger)

	rep, err := eng.Run(context.Background(), []Item{item}, Options{Execute: false})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(store.copies) != 0 {
		t.Fatalf("dry-run must not copy, got %v", store.copies)
	}
	if ledger.writes != 0 {
		t.Fatalf("dry-run must not write the ledger, got %d writes", ledger.writes)
	}
	if _, ok := store.objects["dst-bucket"]["exports/a.csv"]; ok {
		t.Fatal("dry-run must not create destination objects")
	}
	if rep.ByAction["planned_copy"] != 1 || rep.ByStatus[StatusPending] != 1 {
		t.Fatalf("unexpected dry-run report: %+v", rep.ByAction)
	}
}

// dry-run 必须对**所有**分支零副作用：本轮真实负向校准发现 CONFLICT / ALREADY_VERIFIED
// 分支曾绕过 PENDING 判断写账本，这里把这三种分支都钉住。
func TestDryRunWritesNothingOnEveryBranch(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	planned := seed(store, "exports/planned.csv", "planned body")
	conflict := seed(store, "exports/conflict.csv", "source body")
	identical := seed(store, "exports/identical.csv", "same body")
	badsha := seed(store, "exports/badsha.csv", "body")
	badsha.PGSHA256 = hashOf("not the body")
	store.objects["dst-bucket"]["exports/conflict.csv"] = "different destination"
	store.objects["dst-bucket"]["exports/identical.csv"] = "same body"
	eng := newEngine(store, ledger)

	rep, err := eng.Run(context.Background(), []Item{planned, conflict, identical, badsha}, Options{Execute: false})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if ledger.writes != 0 || len(ledger.rows) != 0 {
		t.Fatalf("dry-run must not write the ledger, writes=%d rows=%d", ledger.writes, len(ledger.rows))
	}
	if len(store.copies) != 0 {
		t.Fatalf("dry-run must not copy: %v", store.copies)
	}
	if store.objects["dst-bucket"]["exports/conflict.csv"] != "different destination" {
		t.Fatal("dry-run must not modify conflicting destination")
	}
	if rep.ByAction["planned_copy"] != 1 || rep.ByAction["already_verified"] != 1 ||
		rep.ByAction["conflict"] != 1 || rep.ByAction["source_pg_sha_mismatch"] != 1 {
		t.Fatalf("dry-run plan must classify every branch: %+v", rep.ByAction)
	}
}

func TestExecuteCopiesVerifiesAndKeepsSource(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/a.csv", "hello world")
	eng := newEngine(store, ledger)

	rep, err := eng.Run(context.Background(), []Item{item}, Options{Execute: true})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !rep.AllVerified() {
		t.Fatalf("expected all verified, got %+v failures=%v", rep.ByStatus, rep.Failures)
	}
	row := ledger.rows[ledger.key(item.ExportID, "dst-bucket")]
	if row.Status != StatusVerified || row.SourceSHA256 != row.DestinationSHA256 || row.SourceSHA256 != item.PGSHA256 {
		t.Fatalf("unexpected ledger row: %+v", row)
	}
	if _, ok := store.objects["src-bucket"]["exports/a.csv"]; !ok {
		t.Fatal("source object must never be deleted")
	}
	if _, ok := store.objects["dst-bucket"]["exports/a.csv"]; !ok {
		t.Fatal("destination object missing")
	}
}

func TestSourcePGShaMismatchRefusesToCopy(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/a.csv", "hello world")
	item.PGSHA256 = hashOf("different content")
	eng := newEngine(store, ledger)

	rep, err := eng.Run(context.Background(), []Item{item}, Options{Execute: true})
	if len(store.copies) != 0 {
		t.Fatalf("must not copy when PG sha disagrees with source bytes: %v", store.copies)
	}
	if rep.ByStatus[StatusFailed] != 1 || len(rep.Failures) != 1 {
		t.Fatalf("expected one failure, got %+v", rep.ByStatus)
	}
	if !strings.Contains(rep.Failures[0], "sha256") {
		t.Fatalf("failure must mention sha mismatch: %v", rep.Failures)
	}
	if ledger.rows[ledger.key(item.ExportID, "dst-bucket")].Status != StatusFailed {
		t.Fatal("failed row must be recorded")
	}
	_ = err
}

func TestMissingPGShaStillUsesSourceSHA(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/no-shas.csv", "content without pg sha")
	item.PGSHA256 = ""
	eng := newEngine(store, ledger)

	rep, err := eng.Run(context.Background(), []Item{item}, Options{Execute: true})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	row := ledger.rows[ledger.key(item.ExportID, "dst-bucket")]
	if row.Status != StatusVerified {
		t.Fatalf("expected VERIFIED, got %+v", row)
	}
	if row.SourceSHA256 == "" || row.SourceSHA256 != row.DestinationSHA256 || row.SourceSHA256 != hashOf("content without pg sha") {
		t.Fatalf("source sha must be computed and retained: %+v", row)
	}
	if row.PGSHA256 != "" {
		t.Fatalf("must not backfill a PG sha: %+v", row)
	}
	if !rep.AllVerified() {
		t.Fatalf("expected all verified: %+v", rep.ByStatus)
	}
}

func TestDestinationConflictFailsClosedWithoutOverwrite(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/a.csv", "source body")
	store.objects["dst-bucket"]["exports/a.csv"] = "DIFFERENT destination body"
	eng := newEngine(store, ledger)

	rep, err := eng.Run(context.Background(), []Item{item}, Options{Execute: true})
	if len(store.copies) != 0 {
		t.Fatalf("conflict must not trigger a copy: %v", store.copies)
	}
	if store.objects["dst-bucket"]["exports/a.csv"] != "DIFFERENT destination body" {
		t.Fatal("conflicting destination must never be overwritten")
	}
	if rep.ByStatus[StatusConflict] != 1 {
		t.Fatalf("expected CONFLICT, got %+v", rep.ByStatus)
	}
	if ledger.rows[ledger.key(item.ExportID, "dst-bucket")].Status != StatusConflict {
		t.Fatal("conflict must be recorded in the ledger")
	}
	if err == nil && len(rep.Failures) == 0 {
		t.Fatal("conflict must be reported as a failure")
	}
}

func TestIdenticalDestinationIsAlreadyVerifiedWithoutCopy(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/a.csv", "same body")
	store.objects["dst-bucket"]["exports/a.csv"] = "same body"
	eng := newEngine(store, ledger)

	rep, err := eng.Run(context.Background(), []Item{item}, Options{Execute: true})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(store.copies) != 0 {
		t.Fatalf("identical destination must not be re-copied: %v", store.copies)
	}
	if rep.ByStatus[StatusAlreadyVerified] != 1 || !rep.AllVerified() {
		t.Fatalf("expected ALREADY_VERIFIED, got %+v", rep.ByStatus)
	}
	if ledger.rows[ledger.key(item.ExportID, "dst-bucket")].DestinationSHA256 == "" {
		t.Fatal("already-verified row must carry the destination sha")
	}
}

// 非 404（权限/网络）必须 fail closed：既不能当作"对象缺失"，也不能回退复制。
func TestNon404ErrorFailsClosed(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/a.csv", "hello")
	store.statErr["dst-bucket/exports/a.csv"] = errors.New("Access Denied")
	eng := newEngine(store, ledger)

	rep, err := eng.Run(context.Background(), []Item{item}, Options{Execute: true})
	if len(store.copies) != 0 {
		t.Fatalf("non-404 error must not lead to a copy: %v", store.copies)
	}
	if rep.ByStatus[StatusFailed] != 1 {
		t.Fatalf("expected FAILED, got %+v", rep.ByStatus)
	}
	if !strings.Contains(strings.Join(rep.Failures, " "), "Access Denied") {
		t.Fatalf("underlying error must be preserved: %v", rep.Failures)
	}
	if ledger.rows[ledger.key(item.ExportID, "dst-bucket")].Status != StatusFailed {
		t.Fatal("failed row must be recorded")
	}
	_ = err
}

func TestNon404OnSourceReadFailsClosed(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/a.csv", "hello")
	store.shaErr["src-bucket/exports/a.csv"] = errors.New("SignatureDoesNotMatch")
	eng := newEngine(store, ledger)

	rep, _ := eng.Run(context.Background(), []Item{item}, Options{Execute: true})
	if len(store.copies) != 0 || rep.ByStatus[StatusFailed] != 1 {
		t.Fatalf("source read error must fail closed, got %+v copies=%v", rep.ByStatus, store.copies)
	}
	if !strings.Contains(strings.Join(rep.Failures, " "), "SignatureDoesNotMatch") {
		t.Fatalf("underlying error must be preserved: %v", rep.Failures)
	}
}

func TestRepeatedRunIsIdempotent(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/a.csv", "hello")
	eng := newEngine(store, ledger)

	if _, err := eng.Run(context.Background(), []Item{item}, Options{Execute: true}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	firstCopies := len(store.copies)
	rep, err := eng.Run(context.Background(), []Item{item}, Options{Execute: true})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(store.copies) != firstCopies {
		t.Fatalf("second run must not copy again: %v", store.copies)
	}
	if rep.ByAction["skipped_verified"] != 1 {
		t.Fatalf("second run must skip verified objects: %+v", rep.ByAction)
	}
	if len(ledger.rows) != 1 {
		t.Fatalf("ledger must stay at one row per (export_id,to_bucket): %d", len(ledger.rows))
	}
}

// 中断续跑：第一轮在对象 3 上失败并终止；第二轮必须跳过已 VERIFIED 的前两个并完成剩余。
func TestInterruptedRunResumesFromLedger(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	items := []Item{
		seed(store, "exports/1.csv", "one"),
		seed(store, "exports/2.csv", "two"),
		seed(store, "exports/3.csv", "three"),
	}
	eng := newEngine(store, ledger)
	store.objects["dst-bucket"]["exports/3.csv"] = "DIFFERENT"
	items[2].PGSHA256 = ""

	rep, err := eng.Run(context.Background(), items, Options{Execute: true, StopOnError: true})
	if err == nil {
		t.Fatal("expected the third object to abort the run")
	}
	if rep.ByStatus[StatusVerified] != 2 || rep.ByStatus[StatusConflict] != 1 {
		t.Fatalf("unexpected first-run statuses: %+v", rep.ByStatus)
	}
	copiesAfterFirst := len(store.copies)

	// 修复冲突后重跑：已 VERIFIED 的两个必须跳过，只处理第三个。
	store.objects["dst-bucket"]["exports/3.csv"] = "three"
	rep2, err := eng.Run(context.Background(), items, Options{Execute: true})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if rep2.ByAction["skipped_verified"] != 2 {
		t.Fatalf("resume must skip the two verified objects: %+v", rep2.ByAction)
	}
	if len(store.copies) != copiesAfterFirst {
		t.Fatalf("resume must not re-copy verified objects: %v", store.copies)
	}
	if !rep2.AllVerified() {
		t.Fatalf("resume must converge to all verified: %+v failures=%v", rep2.ByStatus, rep2.Failures)
	}
}

// COPIED（已复制未验证）状态在重跑时必须重新校验而不是假设成功。
func TestResumeReverifiesCopiedButUnverifiedRows(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/a.csv", "payload")
	store.objects["dst-bucket"]["exports/a.csv"] = "payload"
	ledger.rows[ledger.key(item.ExportID, "dst-bucket")] = Row{
		ExportID: item.ExportID, ObjectRef: item.ObjectRef,
		FromBucket: "src-bucket", ToBucket: "dst-bucket",
		Bytes: 7, SourceSHA256: hashOf("payload"), Status: StatusCopied,
	}
	eng := newEngine(store, ledger)

	rep, err := eng.Run(context.Background(), []Item{item}, Options{Execute: true})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(store.copies) != 0 {
		t.Fatalf("COPIED row with identical destination must not re-copy: %v", store.copies)
	}
	if rep.ByStatus[StatusAlreadyVerified] != 1 {
		t.Fatalf("COPIED must be re-verified to ALREADY_VERIFIED: %+v", rep.ByStatus)
	}
}

func TestPreflightRejectsMissingBucket(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	item := seed(store, "exports/a.csv", "x")
	store.buckets["dst-bucket"] = false
	eng := newEngine(store, ledger)

	if _, err := eng.Run(context.Background(), []Item{item}, Options{Execute: true}); err == nil {
		t.Fatal("missing destination bucket must abort the run")
	}
	if len(store.copies) != 0 {
		t.Fatalf("no copy may happen when a bucket is missing: %v", store.copies)
	}
}

func TestPreflightRejectsSameBucket(t *testing.T) {
	store, ledger := newFakeStore(), newMemLedger()
	eng := newEngine(store, ledger)
	eng.ToBucket = eng.FromBucket
	if err := eng.Preflight(context.Background()); err == nil {
		t.Fatal("identical source/destination bucket must be rejected")
	}
}

func TestContentTypeFor(t *testing.T) {
	cases := map[string]string{
		"exports/a.csv":       "text/csv",
		"exports/a.zip":       "application/zip",
		"exports/a.xlsx":      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"exports/a.parquet":   "application/vnd.apache.parquet",
		"exports/a.jsonl":     "application/x-ndjson",
		"exports/unknown.bin": "application/octet-stream",
	}
	for key, want := range cases {
		if got := ContentTypeFor(key); got != want {
			t.Fatalf("ContentTypeFor(%q) = %q, want %q", key, got, want)
		}
	}
}
