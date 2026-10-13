package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JOB_LIST_TEST_DSN points to an isolated PostgreSQL instance. The test owns
// only its UUID-named schema and never changes application tables.
func TestReleasedJobSearchPaginationAndScopePostgres(t *testing.T) {
	dsn := os.Getenv("JOB_LIST_TEST_DSN")
	if dsn == "" {
		t.Skip("set JOB_LIST_TEST_DSN for PostgreSQL list test")
	}
	ctx := context.Background()
	schema := "qa_jobs_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE") }()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, ddl := range []string{
		`CREATE TABLE identities(id uuid, legacy_user_id uuid)`,
		`CREATE TABLE download_jobs(
			id uuid PRIMARY KEY, job_no text NOT NULL, status text NOT NULL, priority int NOT NULL DEFAULT 100,
			source text NOT NULL DEFAULT 'user_download', mode text NOT NULL DEFAULT 'balanced',
			requested_mode text NOT NULL DEFAULT 'fast', effective_mode text NOT NULL DEFAULT 'fast',
			mode_reason text NOT NULL DEFAULT '', mode_changed_at timestamptz NOT NULL DEFAULT now(),
			chain text NOT NULL DEFAULT 'bsc', range_type text NOT NULL DEFAULT 'block',
			from_block bigint, to_block bigint, from_timestamp timestamptz, to_timestamp timestamptz,
			datasets text[] NOT NULL DEFAULT ARRAY['transactions'], reuse_policy text NOT NULL DEFAULT 'prefer_local',
			request_fingerprint text, head_block bigint NOT NULL DEFAULT 10, created_by uuid,
			error text NOT NULL DEFAULT '', stats jsonb NOT NULL DEFAULT '{}',
			organization_id uuid NOT NULL, workspace_id uuid NOT NULL, project_id uuid NOT NULL,
			resolved_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL, created_by_identity uuid NOT NULL)`,
		`CREATE TABLE download_subjects(job_id uuid, status text)`,
		`CREATE TABLE download_shards(job_id uuid, status text, error text, downloaded_rows bigint, downloaded_bytes bigint)`,
		`CREATE TABLE checkpoints(job_id uuid, last_success_at timestamptz)`,
		`CREATE TABLE job_event_log(id bigserial PRIMARY KEY, job_id uuid, event_type text, payload jsonb)`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	org, ws, identity, project := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	insert := func(id, state string, at time.Time, workspace string) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO download_jobs(id,job_no,status,organization_id,workspace_id,project_id,created_by_identity,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, "DL-"+id[:8], state, org, workspace, project, identity, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	for i := 0; i < 205; i++ {
		insert(uuid.NewString(), "COMPLETED", now.Add(time.Duration(i)*time.Second), ws)
	}
	first, second := uuid.NewString(), uuid.NewString()
	insert(first, "RUNNING", now.Add(-time.Hour), ws)
	insert(second, "RUNNING", now.Add(-2*time.Hour), ws)
	otherWorkspace := uuid.NewString()
	insert(otherWorkspace, "RUNNING", now, uuid.NewString())
	for _, shard := range []struct {
		state, reason string
		rows          int64
	}{
		{"COMPLETED", "", 10}, {"RUNNING", "", 2}, {"CANCELLED", store.TailSplitSupersededError, 1000},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO download_shards(job_id,status,error,downloaded_rows,downloaded_bytes)
			VALUES($1,$2,$3,$4,$4)`, first, shard.state, shard.reason, shard.rows); err != nil {
			t.Fatal(err)
		}
	}
	st := &store.Store{Pool: dbtx.New(pool)}
	srv := &Server{deps: Deps{Store: st}}
	principal := policy.Principal{IdentityID: identity, OrganizationID: org, WorkspaceID: ws, Authenticated: true}
	read := func(cursor string, filters ...string) struct {
		Data struct {
			Items []struct {
				ID     string `json:"id"`
				Shards struct {
					Total int64 `json:"total"`
				} `json:"shards"`
				DownloadedRows int64 `json:"downloaded_rows"`
			} `json:"items"`
			StatusCounts map[string]int64 `json:"status_counts"`
		} `json:"data"`
		Meta struct {
			NextCursor string `json:"next_cursor"`
		} `json:"meta"`
	} {
		t.Helper()
		path := "/api/v2/jobs?status=ACTIVE&limit=1"
		for _, filter := range filters {
			if filter == "status=" {
				path = strings.Replace(path, "status=ACTIVE", "status=", 1)
			} else {
				path += "&" + filter
			}
		}
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(withPrincipal(req.Context(), principal))
		w := httptest.NewRecorder()
		srv.handleUserJobsV2(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var result struct {
			Data struct {
				Items []struct {
					ID     string `json:"id"`
					Shards struct {
						Total int64 `json:"total"`
					} `json:"shards"`
					DownloadedRows int64 `json:"downloaded_rows"`
				} `json:"items"`
				StatusCounts map[string]int64 `json:"status_counts"`
			} `json:"data"`
			Meta struct {
				NextCursor string `json:"next_cursor"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	page1 := read("")
	if len(page1.Data.Items) != 1 || page1.Data.Items[0].ID != first || page1.Meta.NextCursor == "" {
		t.Fatalf("first active page = %+v", page1)
	}
	if page1.Data.StatusCounts["RUNNING"] != 2 || page1.Data.StatusCounts["COMPLETED"] != 205 {
		t.Fatalf("scope counts = %+v", page1.Data.StatusCounts)
	}
	page2 := read(page1.Meta.NextCursor)
	if len(page2.Data.Items) != 1 || page2.Data.Items[0].ID != second || page2.Meta.NextCursor != "" {
		t.Fatalf("second active page = %+v", page2)
	}
	// 搜索目标位于 205 个已完成任务之后，不能靠客户端筛选第一页得到。
	matched := read("", "status=", "q="+url.QueryEscape(first))
	if len(matched.Data.Items) != 1 || matched.Data.Items[0].ID != first || matched.Data.StatusCounts["RUNNING"] != 1 || matched.Data.StatusCounts["COMPLETED"] != 0 {
		t.Fatalf("search/counts did not match across pages: %+v", matched)
	}
	boundary := now.Add(-48 * time.Hour).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `UPDATE download_jobs SET created_at=$2,job_no='DL-literal_%' WHERE id=$1`, first, boundary); err != nil {
		t.Fatal(err)
	}
	timeMatched := read("", "created_from="+url.QueryEscape(boundary.In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339Nano)), "created_to="+url.QueryEscape(boundary.Format(time.RFC3339Nano)))
	if len(timeMatched.Data.Items) != 1 || timeMatched.Data.Items[0].ID != first || timeMatched.Data.StatusCounts["RUNNING"] != 1 {
		t.Fatalf("inclusive timezone boundary: %+v", timeMatched)
	}
	for _, literal := range []string{"%", "_", "literal_%"} {
		result := read("", "q="+url.QueryEscape(literal))
		if len(result.Data.Items) != 1 || result.Data.Items[0].ID != first {
			t.Fatalf("wildcard was not literal %q: %+v", literal, result)
		}
	}
	for _, filter := range []string{"q=" + url.QueryEscape(otherWorkspace), "q=" + url.QueryEscape("' OR 1=1 --")} {
		result := read("", filter)
		if len(result.Data.Items) != 0 || len(result.Data.StatusCounts) != 0 {
			t.Fatalf("out-of-scope or unmatched query leaked data: %+v", result)
		}
	}
	foreignIdentityJob := uuid.NewString()
	insert(foreignIdentityJob, "RUNNING", now, ws)
	if _, err := pool.Exec(ctx, `UPDATE download_jobs SET created_by_identity=$2 WHERE id=$1`, foreignIdentityJob, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	denied := read("", "scope=workspace", "q="+foreignIdentityJob)
	if len(denied.Data.Items) != 0 || len(denied.Data.StatusCounts) != 0 {
		t.Fatalf("workspace scope bypassed identity permission: %+v", denied)
	}
	for _, invalid := range []string{"created_from=bad", "created_from=2026-10-03T00:00:00Z&created_to=2026-10-02T00:00:00Z", "q=" + strings.Repeat("a", 201)} {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/jobs?"+invalid, nil)
		req = req.WithContext(withPrincipal(req.Context(), principal))
		w := httptest.NewRecorder()
		srv.handleUserJobsV2(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid filter status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
