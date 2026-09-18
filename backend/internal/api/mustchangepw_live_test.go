//go:build live

// Opt-in PostgreSQL proof for identities.must_change_password enforcement.
//
// It drives the REAL session loader (iam.SessionManager.Authenticate -> loadByToken) so the
// claim "the flag is re-read on every request, therefore the SAME session is allowed as soon
// as the row is cleared" is proven against real rows instead of a hand-built session value.
//
// It never touches the real administrator: it refuses to run for admin@local and requires an
// explicitly named fixture identity. It changes no password -- it only flips the fixture's
// must_change_password flag and restores it in a cleanup.
//
// Run on a host that can reach PostgreSQL:
//
//	C1PW_LIVE_DSN=<dsn> C1PW_FIXTURE_EMAIL=<fixture identity email> //   go test -tags live -run TestMustChangePasswordLive ./internal/api/
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func TestMustChangePasswordLiveSameSessionAllowedAfterFlagCleared(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("C1PW_LIVE_DSN"))
	if dsn == "" {
		t.Skip("C1PW_LIVE_DSN not set")
	}
	email := strings.TrimSpace(os.Getenv("C1PW_FIXTURE_EMAIL"))
	if email == "" {
		t.Skip("C1PW_FIXTURE_EMAIL not set")
	}
	if strings.EqualFold(email, "admin@local") {
		t.Fatal("refusing to run: the fixture identity must not be the real administrator")
	}

	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(st.Close)

	repo := iam.NewRepo(st.Pool)
	cfg := iam.DefaultSessionConfig()
	cfg.IdleTTL = time.Hour
	cfg.AbsoluteTTL = 2 * time.Hour
	svc := iam.NewService(repo, iam.NewSessionManager(repo, cfg), iam.DefaultConfig(), nil)

	identity, err := repo.GetIdentityByEmail(ctx, email)
	if err != nil {
		t.Fatalf("fixture identity %s: %v", email, err)
	}
	if !identity.MustChangePassword {
		t.Fatalf("fixture %s already has must_change_password=false; set it to true to run this proof", email)
	}
	var workspaceID string
	if err := st.Pool.QueryRow(ctx,
		"SELECT workspace_id::text FROM workspace_memberships WHERE identity_id=$1::uuid AND status='active' ORDER BY created_at LIMIT 1",
		identity.ID).Scan(&workspaceID); err != nil {
		t.Fatalf("fixture workspace membership: %v", err)
	}
	// Restore the fixture even if the test fails half way.
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), "UPDATE identities SET must_change_password=true WHERE id=$1::uuid", identity.ID)
		_, _ = st.Pool.Exec(context.Background(), "DELETE FROM sessions WHERE identity_id=$1::uuid", identity.ID)
	})

	created, err := svc.Sessions().Create(ctx, iam.SessionRequest{
		Audience: iam.AudienceUser, IdentityID: identity.ID, OrganizationID: identity.OrganizationID,
		WorkspaceID: workspaceID, AuthMethod: "password",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{policy: policy.NewEngine(st.Pool, 0)}
	drive := func() (int, string, int) {
		sess, authErr := svc.Sessions().Authenticate(ctx, created.Token, iam.AudienceUser)
		if authErr != nil {
			t.Fatalf("authenticate the same session token: %v", authErr)
		}
		p := principalFromSession(ctx, sess, s.policy)
		stub := &recordingHandler{}
		handler := s.mustChangePasswordMiddleware(stub)
		req := httptest.NewRequest(http.MethodGet, "/api/v2/cases", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(withPrincipal(req.Context(), p)))
		return rec.Code, rec.Body.String(), stub.calls
	}

	// Request 1: the flag is true in the database -> refused, and no business logic runs.
	code, body, calls := drive()
	if code != http.StatusForbidden || !hasErrorCode(body, ErrorAUTHNPasswordChangeRequired) {
		t.Fatalf("flagged session = %d %s, want 403 %s", code, body, ErrorAUTHNPasswordChangeRequired)
	}
	if calls != 0 {
		t.Fatalf("the business handler ran %d time(s) while the flag was set", calls)
	}

	// Clear the flag exactly the way a successful password change does, WITHOUT touching any
	// credential: the subject of this test is the flag, not the password.
	if _, err := st.Pool.Exec(ctx, "UPDATE identities SET must_change_password=false WHERE id=$1::uuid", identity.ID); err != nil {
		t.Fatalf("clear must_change_password: %v", err)
	}

	// Request 2: the SAME session token must now be allowed (no cached flag anywhere).
	code, body, calls = drive()
	if hasErrorCode(body, ErrorAUTHNPasswordChangeRequired) {
		t.Fatalf("the same session was still refused after the flag was cleared: %d %s", code, body)
	}
	if calls != 1 {
		t.Fatalf("the business handler ran %d time(s) after the flag was cleared, want 1", calls)
	}
}
