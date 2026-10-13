package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/google/uuid"
)

func TestExportListCursorDoesNotSkipRowPostgres(t *testing.T) {
	dsn := os.Getenv("EXPORT_LIST_TEST_DSN")
	if dsn == "" {
		t.Skip("set EXPORT_LIST_TEST_DSN for PostgreSQL export list test")
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	s := &Server{deps: Deps{Store: st}}
	p := policy.Principal{IdentityID: uuid.NewString(), OrganizationID: uuid.NewString(), WorkspaceID: uuid.NewString()}
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	base := time.Now().UTC().Add(-time.Hour)
	for i, id := range ids {
		_, err := st.Pool.Exec(ctx, `INSERT INTO exports(id,organization_id,workspace_id,format,status,created_at,updated_at)
			VALUES($1,$2,$3,'csv','PENDING',$4,$4)`, id, p.OrganizationID, p.WorkspaceID, base.Add(time.Duration(3-i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM exports WHERE organization_id=$1`, p.OrganizationID)
	})
	list := func(cursor string) ([]string, string) {
		t.Helper()
		path := "/api/v2/exports?limit=2"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(withPrincipal(req.Context(), p))
		w := httptest.NewRecorder()
		s.handleExportsV2List(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
		}
		var response struct {
			Data struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			} `json:"data"`
			Meta struct {
				NextCursor string `json:"next_cursor"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		page := make([]string, 0, len(response.Data.Items))
		for _, item := range response.Data.Items {
			page = append(page, item.ID)
		}
		return page, response.Meta.NextCursor
	}
	first, cursor := list("")
	second, finalCursor := list(cursor)
	if len(first) != 2 || first[0] != ids[0] || first[1] != ids[1] || cursor == "" {
		t.Fatalf("first page=%v cursor=%q", first, cursor)
	}
	if len(second) != 1 || second[0] != ids[2] || finalCursor != "" {
		t.Fatalf("second page=%v final_cursor=%q", second, finalCursor)
	}
}
