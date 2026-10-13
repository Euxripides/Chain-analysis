package api

import (
	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransactionAnalysisRoutesRejectBeforeProviderWithoutPermission(t *testing.T) {
	s := &Server{authMode: auth.ModeSession, policy: policy.NewEngine(nil, 0)}
	router := chi.NewRouter()
	s.registerUserV2(router)
	for _, suffix := range []string{"analysis", "bridge", "context"} {
		path := "/explorer/transactions/bsc/0x" + strings.Repeat("1", 64) + "/" + suffix
		for _, p := range []policy.Principal{{}, {Authenticated: true, Audience: "user", Permissions: []string{"dashboard:read"}}} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req = req.WithContext(withPrincipal(req.Context(), p))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			want := 401
			if p.Authenticated {
				want = 403
			}
			if w.Code != want {
				t.Fatalf("%s unauthorized provider route: %d", suffix, w.Code)
			}
		}
	}
}

func TestNewAnalysisRoutesKeepExplorerFeatureFlag(t *testing.T) {
	src := readAPISource(t, "userv2.go")
	for _, suffix := range []string{"analysis", "bridge", "context"} {
		line := routeLine(src, "/explorer/transactions/{chain}/{hash}/"+suffix)
		if !strings.Contains(line, `s.requireFlag("user.explorer_v2"`) {
			t.Fatalf("analysis route lacks deployed feature guard: %s", line)
		}
	}
}
