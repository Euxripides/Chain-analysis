package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
)

// 生产模式（session）下，客户端自报的 X-User-* 头必须完全无效（§28.4 验收）。
func TestIdentityMiddlewareIgnoresClientSuppliedHeaders(t *testing.T) {
	s := &Server{authMode: auth.ModeSession}
	handler := s.identityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.FromContext(r.Context())
		if u.ID != "" || u.Email != "" {
			t.Errorf("client-supplied identity leaked into context: %+v", u)
		}
		if auth.IsAuthenticated(r.Context()) {
			t.Error("request must not be treated as authenticated")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/users", nil)
	req.Header.Set("X-User-Id", "00000000-0000-0000-0000-000000000001")
	req.Header.Set("X-User-Email", "attacker@example.com")
	req.Header.Set("X-User-Role", "owner")
	req.Header.Set("X-Org-Id", "00000000-0000-0000-0000-000000000002")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status %d", rec.Code)
	}
}

// hybrid 模式保留迁移期兼容路径，供测试环境回归对照使用。
func TestIdentityMiddlewareHonoursHeadersInHybridMode(t *testing.T) {
	s := &Server{authMode: auth.ModeHybrid}
	handler := s.identityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.FromContext(r.Context())
		if u.ID != "abc" {
			t.Errorf("expected header identity in hybrid mode, got %+v", u)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("X-User-Id", "abc")
	handler.ServeHTTP(httptest.NewRecorder(), req)
}

func TestAudienceForPath(t *testing.T) {
	cases := map[string]string{
		"/api/admin/v1/users": iam.AudienceAdmin,
		"/api/v2/dashboard":   iam.AudienceUser,
		"/api/v1/me":          iam.AudienceUser,
		"/api/auth/v1/login":  iam.AudienceUser,
	}
	for path, want := range cases {
		if got := audienceForPath(path); got != want {
			t.Errorf("audienceForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestSanitizeReturnToBlocksOpenRedirect(t *testing.T) {
	bad := []string{"https://evil.example/steal", "//evil.example", "http://evil", "/" + string(rune(92)) + "evil", "javascript:alert(1)"}
	for _, v := range bad {
		if got := sanitizeReturnTo(v); got != "/home" {
			t.Errorf("sanitizeReturnTo(%q) = %q, want /home", v, got)
		}
	}
	if got := sanitizeReturnTo("/cases/123"); got != "/cases/123" {
		t.Errorf("legit relative path must be preserved, got %q", got)
	}
	// 挂载前缀必须剥离，否则 react-router basename 会二次拼接。
	if got := sanitizeReturnTo("/admin/overview"); got != "/overview" {
		t.Errorf("admin prefix must be stripped, got %q", got)
	}
	if got := sanitizeReturnTo("/app-v2/cases/9"); got != "/cases/9" {
		t.Errorf("user console prefix must be stripped, got %q", got)
	}
	if got := sanitizeReturnTo("/admin"); got != "/home" {
		t.Errorf("bare prefix must fall back, got %q", got)
	}
	if got := sanitizeReturnToDefault("/admin/overview", "/overview"); got != "/overview" {
		t.Errorf("fallback for admin audience wrong, got %q", got)
	}
}

func TestPermissionDeclaredViaRequireWrapper(t *testing.T) {
	s := &Server{authMode: auth.ModeSession}
	handler := s.require("case:read", func(w http.ResponseWriter, r *http.Request) {
		if declaredPermission(r.Context()) != "case:read" {
			t.Error("declared permission missing from context")
		}
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v2/cases", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	// 未认证必须返回 401，而不是放行。
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request must be rejected, got %d", rec.Code)
	}
}

func TestRequestedAudienceNarrowsResolution(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"admin", iam.AudienceAdmin},
		{"USER", iam.AudienceUser},
		{"admin,user", ""},
		{"", ""},
		{"garbage", ""},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/v1/session", nil)
		if tc.header != "" {
			req.Header.Set("X-Auth-Audience", tc.header)
		}
		if got := requestedAudience(req); got != tc.want {
			t.Errorf("requestedAudience(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}
