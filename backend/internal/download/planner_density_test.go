package download

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/google/uuid"
)

func TestLocalDensityFanoutRespectsWorkAndCost(t *testing.T) {
	for _, tc := range []struct {
		name string
		base int
		rows uint64
		size int
		mode string
		want int
	}{
		{"sparse does not pay extra probes", 2, 1000, 10000, "fast", 2},
		{"heavy fast uses four streams", 2, 166710, 2000, "fast", 4},
		{"safe preserves two stream cap", 2, 166710, 2000, "safe", 2},
		{"turbo stays bounded", 2, 500000, 2000, "turbo", 8},
		{"transfer pages amortize overhead", 2, 132277, 10000, "fast", 2},
		{"existing tuned plan retained", 8, 10, 10000, "safe", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := localDensityShardCount(tc.base, tc.rows, tc.size, tc.mode); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFreshDownloadDensityPlanningUsesValidatedMetadata(t *testing.T) {
	dsn := os.Getenv("DOWNLOAD_RECOVERY_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	admin, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "newflow_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Pool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	fixture, err := os.ReadFile("../store/testdata/download_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ReplaceAll(string(fixture), "public.", schema+".")
	sql = strings.ReplaceAll(sql, "SELECT pg_catalog.set_config('search_path', '', false);", "")
	if _, err := admin.Pool.Exec(ctx, "SET search_path TO "+schema+";"+sql); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	st, err := store.New(ctx, dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	const address = "0x1111111111111111111111111111111111111111"
	makeJob := func(subject, status string) (*model.DownloadJob, model.Subject) {
		org, workspace := uuid.NewString(), uuid.NewString()
		job := &model.DownloadJob{JobNo: uuid.NewString(), Status: status, Chain: "bsc", RangeType: "full", HeadBlock: 1000000, Datasets: []string{"token_transfers"}, Source: "user_download", ReusePolicy: "refresh", EffectiveMode: model.PerformanceFast, OrganizationID: &org, WorkspaceID: &workspace}
		subjects, err := st.CreateJobWithSubjects(ctx, job, []string{subject})
		if err != nil {
			t.Fatal(err)
		}
		return job, subjects[0]
	}
	old, oldSub := makeJob(address, model.JobCompleted)
	datasets, err := st.CreateDatasetsForSubject(ctx, old.ID, oldSub.ID, old.Datasets)
	if err != nil {
		t.Fatal(err)
	}
	oldShard := model.Shard{ID: uuid.NewString(), JobID: old.ID, SubjectID: oldSub.ID, DatasetID: datasets[0].ID, Dataset: "token_transfers", FromBlock: 0, ToBlock: 1000000, Status: model.ShardCompleted, PageState: map[string]any{}}
	if err := st.CreateShards(ctx, []model.Shard{oldShard}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateDatasetStatus(ctx, datasets[0].ID, model.ItemCompleted); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		_, err := st.Pool.Exec(ctx, `INSERT INTO download_pages(id,job_id,shard_id,dataset,page_no,status,rows_fetched,from_block,to_block) VALUES($1,$2,$3,'token_transfers',$4,'VALIDATED',2000,$5,$6)`, uuid.NewString(), old.ID, oldShard.ID, i+1, 900000+i*20, 900019+i*20)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertPageProfile(ctx, model.PageProfile{Chain: "bsc", Dataset: "token_transfers", QueryShape: "token_transfers", PreferredPageSize: 2000, AvgRows: 2000, PagesObserved: 40}); err != nil {
		t.Fatal(err)
	}
	planner := NewPlanner(st, nil, slog.Default(), false)
	fresh, _ := makeJob(address, model.JobCreated)
	if err := planner.PlanJob(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	shards, err := st.ShardsByJob(ctx, fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 5 {
		t.Fatalf("want prefix plus four useful ranges, got %d", len(shards))
	}
	sort.Slice(shards, func(i, j int) bool { return shards[i].FromBlock < shards[j].FromBlock })
	var next int64
	for _, shard := range shards {
		if shard.FromBlock != next || shard.Status != model.ShardPending {
			t.Fatalf("gap, overlap or skipped prefix: %+v (next=%d)", shard, next)
		}
		next = shard.ToBlock + 1
	}
	if next != fresh.HeadBlock+1 {
		t.Fatalf("tail missing: %d", next)
	}
	// The actual planner has nil provider/oracle/CH; an external call cannot
	// occur. A never-seen address keeps its original two-shard cold fallback.
	cold, _ := makeJob("0x2222222222222222222222222222222222222222", model.JobCreated)
	if err := planner.PlanJob(ctx, cold); err != nil {
		t.Fatal(err)
	}
	coldShards, err := st.ShardsByJob(ctx, cold.ID)
	if err != nil || len(coldShards) != 2 {
		t.Fatal(fmt.Sprintf("cold fallback changed: %d %v", len(coldShards), err))
	}
	for _, shard := range coldShards {
		if shard.Status != model.ShardPending {
			t.Fatal("unknown local rows must not skip download")
		}
	}
}
