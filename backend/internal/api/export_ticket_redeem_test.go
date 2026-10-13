package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The test DSN has isolated exports/export_tickets fixtures; it must not point
// to a production database because these rows are intentionally minimal.
func TestRedeemExportTicketPostgres(t *testing.T) {
	dsn := os.Getenv("EXPORT_TICKET_TEST_DSN")
	if dsn == "" {
		t.Skip("set EXPORT_TICKET_TEST_DSN for PostgreSQL ticket test")
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	s := &Server{deps: Deps{Store: st}}
	exportID, ticket := uuid.NewString(), uuid.NewString()
	owner := policy.Principal{IdentityID: uuid.NewString(), OrganizationID: uuid.NewString(), WorkspaceID: uuid.NewString()}
	other := owner
	other.IdentityID = uuid.NewString()
	if _, err := st.Pool.Exec(ctx, `INSERT INTO exports(id,status,expires_at) VALUES($1,'COMPLETED',$2)`, exportID, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM export_tickets WHERE id=$1`, ticket)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM exports WHERE id=$1`, exportID)
	})
	if _, err := st.Pool.Exec(ctx, `INSERT INTO export_tickets(id,export_id,user_id,organization_id,workspace_id,expires_at)
		VALUES($1,$2,$3,$4,$5,now()+interval '60 seconds')`, ticket, exportID, owner.IdentityID, owner.OrganizationID, owner.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	assertRedeem := func(p policy.Principal, method, byteRange string, want bool) {
		t.Helper()
		id, err := s.redeemExportTicket(ctx, ticket, p, method, byteRange)
		if want && (err != nil || id != exportID) {
			t.Fatalf("redeem %s range=%q: id=%q err=%v", method, byteRange, id, err)
		}
		if !want && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("redeem should be denied, got id=%q err=%v", id, err)
		}
	}
	assertRedeem(other, http.MethodHead, "", false)
	assertRedeem(other, http.MethodGet, "bytes=0-9", false)
	assertRedeem(owner, http.MethodHead, "", true)
	var usedAt *time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT used_at FROM export_tickets WHERE id=$1`, ticket).Scan(&usedAt); err != nil || usedAt != nil {
		t.Fatalf("HEAD consumed ticket: used_at=%v err=%v", usedAt, err)
	}
	assertRedeem(owner, http.MethodGet, "bytes=0-9", true)
	assertRedeem(owner, http.MethodGet, "bytes=10-19", true)
	assertRedeem(owner, http.MethodHead, "", true)
	assertRedeem(owner, http.MethodGet, "", false)
	assertRedeem(other, http.MethodGet, "bytes=20-29", false)
	wrongScope := owner
	wrongScope.WorkspaceID = uuid.NewString()
	assertRedeem(wrongScope, http.MethodGet, "bytes=20-29", false)
}
