package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/go-chi/chi/v5"
)

func TestDatasetPrefetchRouteRequiresCreatePermissionAndSkipsScan(t *testing.T) {
	s := &Server{authMode: auth.ModeSession, policy: policy.NewEngine(nil, 0)}
	router := chi.NewRouter()
	s.registerUserV2(router)
	for _, tc := range []struct {
		p      policy.Principal
		status int
	}{
		{policy.Principal{}, http.StatusUnauthorized},
		{policy.Principal{Authenticated: true, Audience: "user", OrganizationID: "org", Permissions: []string{"export:list"}}, http.StatusForbidden},
		{policy.Principal{Authenticated: true, Audience: "user", OrganizationID: "org", WorkspaceID: "ws", Permissions: []string{"export:create"}}, http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodPost, "/exports/prefetch", strings.NewReader(`{"file_layout":"dataset","datasets":["nft_transfers","balances"]}`))
		req = req.WithContext(withPrincipal(req.Context(), tc.p))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
		}
		if w.Code == http.StatusOK && !strings.Contains(w.Body.String(), `"prefetched":false`) {
			t.Fatal(w.Body.String())
		}
	}
}
