package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/access"
	"github.com/blockchain-platform/blockchain-platform/internal/casework"
	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/testutil"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Real HTTP handlers, service SQL, PostgreSQL keyset order and envelope. Auth
// middleware is covered separately; this fixture supplies a trusted principal.
func TestReportsPaginationAndFiltersPostgres(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `CREATE TABLE cases(id uuid PRIMARY KEY,case_no text);
        CREATE TABLE investigation_reports(
          id uuid PRIMARY KEY, investigation_id uuid NOT NULL,case_id uuid,organization_id uuid NOT NULL,workspace_id uuid NOT NULL,
          revision bigint NOT NULL DEFAULT 1,format text NOT NULL DEFAULT 'markdown',content_hash text NOT NULL DEFAULT 'sha256',size_bytes bigint NOT NULL DEFAULT 128,
          status text NOT NULL,status_revision bigint NOT NULL DEFAULT 1,metadata jsonb NOT NULL DEFAULT '{}',generated_at timestamptz NOT NULL,
          submitted_at timestamptz,submitted_by uuid,approved_at timestamptz,approved_by uuid,rejected_at timestamptz,reject_reason text NOT NULL DEFAULT '',
          signed_at timestamptz,signature_hash text NOT NULL DEFAULT '',archived_at timestamptz);
        CREATE INDEX investigation_reports_scope_time_idx ON investigation_reports(organization_id,workspace_id,generated_at DESC);`)
	if err != nil {
		t.Fatal(err)
	}
	org, ws, inv, caseID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = pool.Exec(ctx, "INSERT INTO cases VALUES($1,'CASE-001')", caseID)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	insert := func(id, status, rowOrg, rowWS, rowCase, rowInv string, at time.Time) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO investigation_reports(id,investigation_id,case_id,organization_id,workspace_id,status,generated_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, rowInv, rowCase, rowOrg, rowWS, status, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]bool{}
	for i := 0; i < 245; i++ {
		id := uuid.NewString()
		want[id] = true
		insert(id, "approved", org, ws, caseID, inv, base)
	}
	for i := 0; i < 65; i++ {
		insert(uuid.NewString(), "generated", org, ws, caseID, inv, base.Add(time.Hour))
	}
	insert(uuid.NewString(), "approved", org, ws, caseID, inv, base.Add(-time.Microsecond))
	insert(uuid.NewString(), "approved", org, ws, caseID, inv, base.Add(24*time.Hour))
	insert(uuid.NewString(), "approved", org, uuid.NewString(), caseID, inv, base)
	insert(uuid.NewString(), "approved", uuid.NewString(), ws, caseID, inv, base)
	principal := policy.Principal{Authenticated: true, OrganizationID: org, WorkspaceID: ws, IdentityID: uuid.NewString()}
	srv := &Server{casework: casework.New(dbtx.New(pool), nil, nil, nil), guard: fakeGuard{scope: access.Scope{OrganizationID: org, WorkspaceID: ws}}}
	router := chi.NewRouter()
	router.Get("/api/v2/reports", srv.handleUserReports)
	router.Get("/api/v2/cases/{id}/reports", srv.handleUserCaseReports)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principal
		if r.Header.Get("X-Test-Workspace") != "" {
			p.WorkspaceID = r.Header.Get("X-Test-Workspace")
		}
		router.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	}))
	defer httpServer.Close()
	type page struct {
		Data []casework.ReportView `json:"data"`
		Meta struct {
			Next  *string `json:"next_cursor"`
			Total *int    `json:"total"`
		} `json:"meta"`
	}
	get := func(params url.Values, workspace string, status int, paths ...string) page {
		t.Helper()
		path := "/api/v2/reports"
		if len(paths) > 0 {
			path = paths[0]
		}
		req, _ := http.NewRequest(http.MethodGet, httpServer.URL+path+"?"+params.Encode(), nil)
		req.Header.Set("X-Test-Workspace", workspace)
		res, err := httpServer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var raw json.RawMessage
		if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != status {
			t.Fatalf("status %d want %d: %s", res.StatusCode, status, raw)
		}
		var p page
		if status == 200 {
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			if p.Data == nil || p.Meta.Total != nil {
				t.Fatalf("bad list shape or false total: %s", raw)
			}
		}
		return p
	}
	defaultPage := get(url.Values{}, "", 200)
	if len(defaultPage.Data) != 100 || defaultPage.Meta.Next == nil {
		t.Fatal("default page still hides continuation")
	}
	params := url.Values{"status": {"approved"}, "case_id": {caseID}, "investigation_id": {inv}, "generated_from": {base.Format(time.RFC3339Nano)}, "generated_to": {base.Add(24 * time.Hour).Format(time.RFC3339Nano)}, "limit": {"73"}}
	seen := map[string]bool{}
	previous, cursor, pages := "", "", 0
	for {
		params.Set("cursor", cursor)
		p := get(params, "", 200)
		pages++
		for _, r := range p.Data {
			if !want[r.ID] || seen[r.ID] || r.Status != "approved" || !r.Downloadable || r.CaseNo != "CASE-001" || (previous != "" && r.ID >= previous) {
				t.Fatalf("missing filter, duplicate or unstable tie: %+v", r)
			}
			seen[r.ID] = true
			previous = r.ID
		}
		if pages == 1 {
			if p.Meta.Next == nil {
				t.Fatal("first cursor missing")
			}
			boundCursor := *p.Meta.Next
			params.Set("cursor", boundCursor)
			get(params, uuid.NewString(), 400)
			params.Set("status", "generated")
			get(params, "", 400)
			params.Set("status", "approved")
			params.Set("generated_from", "2026-10-01T08:00:00+08:00")
			get(params, "", 200)
			// A newer report inserted during traversal must not duplicate or shift older pages.
			insert(uuid.NewString(), "approved", org, ws, caseID, inv, base.Add(time.Minute))
		}
		if p.Meta.Next == nil {
			break
		}
		cursor = *p.Meta.Next
		if pages > 10 {
			t.Fatal("non-terminating cursor")
		}
	}
	if len(seen) != len(want) || pages != 4 {
		t.Fatalf("got %d reports in %d pages, want 245 in 4", len(seen), pages)
	}
	for _, bad := range []url.Values{{"limit": {"0"}}, {"limit": {"-1"}}, {"limit": {"abc"}}, {"cursor": {"garbage"}}, {"case_id": {"not-uuid"}}, {"status": {"invalid"}}, {"generated_from": {"2026-10-01"}}, {"generated_from": {"2026-10-03T00:00:00Z"}, "generated_to": {"2026-10-02T00:00:00Z"}}} {
		get(bad, "", 400)
	}
	if len(get(url.Values{"limit": {"999"}}, "", 200).Data) != 200 {
		t.Fatal("limit cap wrong")
	}
	if len(get(url.Values{"case_id": {uuid.NewString()}}, "", 200).Data) != 0 {
		t.Fatal("empty case leaked results")
	}
	casePage := get(url.Values{"limit": {"200"}, "status": {"approved"}, "case_id": {uuid.NewString()}}, "", 200, "/api/v2/cases/"+caseID+"/reports")
	if len(casePage.Data) != 200 || casePage.Meta.Next == nil {
		t.Fatal("case route did not use path-scoped pagination")
	}
	// Capture the real PostgreSQL plan without claiming production timings.
	plan, err := pool.Query(ctx, `EXPLAIN (ANALYZE, BUFFERS) SELECT id FROM investigation_reports WHERE organization_id=$1 AND workspace_id=$2 ORDER BY generated_at DESC,id DESC LIMIT 201`, org, ws)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	var lines []string
	for plan.Next() {
		var line string
		if err := plan.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := plan.Err(); err != nil {
		t.Fatal(err)
	}
	t.Log(fmt.Sprintf("245 matching reports, 4 pages, no duplicates or omissions; plan:\n%s", strings.Join(lines, "\n")))
}
