package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/access"
	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/capability"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// ---------------------------------------------------------------------------
// S7-SEC-LEGACY-V1 P0 回归：无凭证（无 Cookie / 无 Authorization / 无 API Key /
// 无 X-User-* 头）请求 /api/v1 读接口，必须拿不到任何租户业务数据。
//
// 这些测试刻意走**真实路由表**（s.buildRouter），而不是直接调 handler：
// P0 的根因之一就是"路由没声明权限"，只测 handler 会漏掉整类缺陷。
// ---------------------------------------------------------------------------

// fakeGuard 让对象级授权可以在没有数据库的情况下被验证。
type fakeGuard struct {
	scope access.Scope
	err   error
}

func (f fakeGuard) Require(context.Context, policy.Principal, string, string) (access.Scope, error) {
	return f.scope, f.err
}
func (f fakeGuard) Reason(context.Context, policy.Principal, string, string) string {
	return access.ReasonCrossOrg
}
func (f fakeGuard) Invalidate(string) {}

// newLegacyV1TestHandler 组装一个最小可用的 Server 并返回可注入主体/守卫的 handler。
//
// 说明：Deps.Store 故意只放零值 *store.Store（Pool=nil）。租户/敏感路由在
// require 或对象授权阶段就会被拦下，根本走不到 SQL，因此不需要真实数据库；
// 反过来，如果哪条路由"漏了 require"，测试会看到 500（handler 里 nil Pool panic
// 被 Recoverer 兜住）而不是期望的 401，同样会失败 —— 这正是我们要的回归信号。
func newLegacyV1TestHandler(t *testing.T, principal *policy.Principal, guard objectScoper) http.Handler {
	t.Helper()
	s := &Server{
		deps:         Deps{Store: &store.Store{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		authMode:     auth.ModeSession,
		cfg:          &config.Config{},
		policy:       policy.NewEngine(nil, 0),
		guard:        guard,
		capabilities: capability.NewRegistry(),
	}
	s.buildRouter()
	inner := s.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal != nil {
			r = r.WithContext(withPrincipal(r.Context(), *principal))
		}
		inner.ServeHTTP(w, r)
	})
}

func doLegacyV1(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestLegacyV1ReadRoutesRejectUnauthenticated 覆盖监控者实测确认的 P0 清单，
// 并顺势覆盖同一批次被修的所有租户/敏感读路由。
func TestLegacyV1ReadRoutesRejectUnauthenticated(t *testing.T) {
	h := newLegacyV1TestHandler(t, nil, nil)
	const id = "00000000-0000-0000-0000-000000000001"
	cases := []string{
		// 监控者实测返回 200 的 route（9234B / 28831B / 25305B / 40620B / 374399B / 57619B）
		"/api/v1/cases",
		"/api/v1/investigations",
		"/api/v1/download-jobs?limit=1",
		"/api/v1/api-control/overview",
		"/api/v1/api-control/provider-accounts",
		"/api/v1/api-control/requests",
		"/api/v1/admin/planning-decisions",
		"/api/v1/admin/ankr/benchmarks",
		"/api/v1/data-explorer/addresses",
		// 同批次的其余租户/敏感读路由
		"/api/v1/tags",
		"/api/v1/exports/" + id,
		"/api/v1/exports/" + id + "/download",
		"/api/v1/coverage?chain=bsc&subject=0xabc&dataset=transactions",
		"/api/v1/coverage/stats",
		"/api/v1/datasets/transactions",
		"/api/v1/addresses/0xabc/activity",
		"/api/v1/canvases/" + id,
		"/api/v1/canvases/" + id + "/snapshots",
		"/api/v1/cases/" + id + "/canvases",
		"/api/v1/investigations/" + id,
		"/api/v1/investigations/" + id + "/runtime",
		"/api/v1/investigations/" + id + "/artifacts",
		"/api/v1/investigations/" + id + "/reports",
		"/api/v1/investigations/" + id + "/subjects",
		"/api/v1/reports/" + id + "/download",
		"/api/v1/download-jobs/" + id,
		"/api/v1/download-jobs/" + id + "/guard",
		"/api/v1/download-jobs/" + id + "/events",
		"/api/v1/providers/ankr/usage",
		"/api/v1/providers/ankr/status",
		"/api/v1/providers/ankr/global-concurrency",
		"/api/v1/providers/ankr/fetch-runtime",
		"/api/v1/providers/ankr/production-profile",
		"/api/v1/providers/ankr/fetch-profile",
		"/api/v1/providers/ankr/ready-queue",
		"/api/v1/count-oracle/status",
		"/api/v1/count-oracle/providers",
		"/api/v1/count-oracle/resolutions",
		"/api/v1/count-oracle/observations",
		"/api/v1/count-oracle/calibration",
		"/api/v1/admin/ankr/range-certifications",
		"/api/v1/api-control/control/states",
		"/api/v1/api-control/control/policies",
		"/api/v1/api-control/control/actions",
		"/api/v1/api-control/errors",
		"/api/v1/api-control/costs",
		"/api/v1/api-control/operations",
		"/api/v1/api-control/providers",
		"/api/v1/api-control/usage-types",
		"/api/v1/api-control/budgets",
		"/api/v1/api-control/alerts",
		"/api/v1/admin/token-metadata/backfill",
		"/api/v1/admin/methods/backfill",
		"/api/v1/admin/methods/backfill/" + id,
		"/api/v1/tokens/enrichment",
		"/api/v1/methods/enrichment",
		"/api/v1/addresses/bsc/0xabc/runtime",
		"/api/v1/transactions/bsc/0xabc/internal-calls",
		"/api/v1/analysis/addresses/bsc/0xabc/flow-profile",
		"/api/v1/analysis/addresses/bsc/0xabc/semantic-events",
		"/api/v1/analysis/addresses/bsc/0xabc/advanced",
	}
	for _, target := range cases {
		rec := doLegacyV1(t, h, http.MethodGet, target)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401 (unauthenticated legacy v1 read)", target, rec.Code)
			continue
		}
		// 未认证响应绝不能带业务数据体。
		if body := rec.Body.String(); strings.Contains(body, "organization_id") || strings.Contains(body, "workspace_id") {
			t.Errorf("GET %s leaked tenant fields in the unauthenticated body", target)
		}
	}
}

// TestLegacyV1AuthenticatedViewerForbidden 覆盖"已认证但权限不足 -> 403"。
//
// 关键点：修复不能退化成"登录即可读"——viewer 只拿到 case:read / explorer:read，
// 高权限读（案件列表、coverage、api-control、任务列表）必须 403。
func TestLegacyV1AuthenticatedViewerForbidden(t *testing.T) {
	viewer := policy.Principal{
		IdentityID: "11111111-1111-1111-1111-111111111111", Email: "viewer@example.com",
		OrganizationID: "22222222-2222-2222-2222-222222222222",
		WorkspaceID:    "33333333-3333-3333-3333-333333333333",
		Audience:       "user", Roles: []string{"viewer"},
		// viewer 只有查询本地数据的权限，没有任何租户读权限。
		Permissions:   []string{"explorer:read"},
		Authenticated: true,
	}
	h := newLegacyV1TestHandler(t, &viewer, nil)
	for _, target := range []string{
		"/api/v1/cases",                                        // case:list
		"/api/v1/download-jobs?limit=1",                        // job:list:workspace
		"/api/v1/api-control/overview",                         // admin:dashboard:read
		"/api/v1/investigations",                               // investigation:list
		"/api/v1/tags",                                         // case:read
		"/api/v1/admin/planning-decisions",                     // admin:job:read
		"/api/v1/exports/00000000-0000-0000-0000-000000000001", // export:read
	} {
		rec := doLegacyV1(t, h, http.MethodGet, target)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s as viewer = %d, want 403", target, rec.Code)
		}
	}

	// 缺少 explorer:read 的主体连 coverage 也读不到（同域读键必须显式授予）。
	noExplorer := viewer
	noExplorer.Permissions = []string{"case:read"}
	h2 := newLegacyV1TestHandler(t, &noExplorer, nil)
	if rec := doLegacyV1(t, h2, http.MethodGet, "/api/v1/coverage?chain=bsc&subject=0xabc&dataset=transactions"); rec.Code != http.StatusForbidden {
		t.Errorf("GET /api/v1/coverage without explorer:read = %d, want 403", rec.Code)
	}
}

// TestLegacyV1CrossTenantObjectReadIs404 覆盖"已认证但对象属于别的组织 -> 404"。
//
// 越界必须与"不存在"不可区分（§27 RBAC-002），否则可以用状态码枚举他人对象。
func TestLegacyV1CrossTenantObjectReadIs404(t *testing.T) {
	principal := policy.Principal{
		IdentityID: "11111111-1111-1111-1111-111111111111", Email: "other-org@example.com",
		OrganizationID: "99999999-9999-9999-9999-999999999999",
		WorkspaceID:    "88888888-8888-8888-8888-888888888888",
		Audience:       "user",
		Roles:          []string{"investigator"},
		Permissions: []string{
			"job:read", "canvas:list", "canvas:read", "canvas:snapshot", "case:read",
			"case:list", "export:read", "export:download", "investigation:read", "report:download",
		},
		Authenticated: true,
	}
	// 守卫返回 ErrNotFound 正是 access.Guard 在"跨组织"时的行为。
	h := newLegacyV1TestHandler(t, &principal, fakeGuard{err: access.ErrNotFound})
	const id = "00000000-0000-0000-0000-000000000042"
	for _, target := range []string{
		"/api/v1/download-jobs/" + id,
		"/api/v1/download-jobs/" + id + "/guard",
		"/api/v1/cases/" + id + "/canvases",
		"/api/v1/canvases/" + id,
		"/api/v1/exports/" + id,
		"/api/v1/investigations/" + id,
		"/api/v1/reports/" + id + "/download",
	} {
		rec := doLegacyV1(t, h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s cross-tenant = %d, want 404 (must not reveal existence)", target, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, "RESOURCE_NOT_FOUND") {
			t.Errorf("GET %s = %s, want RESOURCE_NOT_FOUND envelope", target, body)
		}
		if strings.Contains(body, id) {
			t.Errorf("GET %s echoed the object id back; existence information leaked", target)
		}
	}
}

// TestLegacyV1PublicWhitelistStaysOpen 钉住公开白名单：这些元数据/链上公共查询
// 在无凭证时仍必须 200，否则登录页与支持链列表无法渲染。
func TestLegacyV1PublicWhitelistStaysOpen(t *testing.T) {
	h := newLegacyV1TestHandler(t, nil, nil)
	for _, target := range []string{
		"/health/live",
		"/api/v1/me",
		"/api/v1/capabilities",
	} {
		rec := doLegacyV1(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Errorf("public route GET %s = %d, want 200", target, rec.Code)
		}
	}
}

// TestTenantScopeNeverFallsBackToDefaultWorkspace 直接钉住 P0 的第二层根因：
// 作用域只能来自主体，未认证必须失败，绝不允许回退到默认组织/工作区。
func TestTenantScopeNeverFallsBackToDefaultWorkspace(t *testing.T) {
	s := &Server{deps: Deps{Store: &store.Store{}}}

	if _, err := s.scopeFromPrincipal(policy.Principal{}); err == nil {
		t.Fatal("unauthenticated principal must not resolve a tenant scope")
	}
	if _, err := s.scopeFromPrincipal(policy.Principal{Authenticated: true}); err == nil {
		t.Fatal("authenticated principal without organization must not fall back to the default org")
	}
	got, err := s.scopeFromPrincipal(policy.Principal{
		Authenticated: true, OrganizationID: "org-b", WorkspaceID: "ws-b",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.OrganizationID != "org-b" || got.WorkspaceID != "ws-b" {
		t.Fatalf("scope = %+v, want the principal's own org/workspace", got)
	}
}

// TestTenantScopeOr401WritesUnauthorized 保证 handler 层的第二道防线可用。
func TestTenantScopeOr401WritesUnauthorized(t *testing.T) {
	s := &Server{deps: Deps{Store: &store.Store{}}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cases", nil)
	if _, ok := s.tenantScopeOr401(rec, req); ok {
		t.Fatal("tenantScopeOr401 must fail for an unauthenticated request")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestWritableWorkspaceFailsClosedWithoutStore 保证写路径不会静默创建到空工作区。
func TestWritableWorkspaceFailsClosedWithoutStore(t *testing.T) {
	s := &Server{deps: Deps{Store: &store.Store{}}}
	if _, err := s.writableWorkspace(context.Background(), tenantScope{OrganizationID: "org"}); err == nil {
		t.Fatal("writableWorkspace must fail closed when no workspace can be resolved")
	}
	if ws, err := s.writableWorkspace(context.Background(), tenantScope{OrganizationID: "org", WorkspaceID: "ws"}); err != nil || ws != "ws" {
		t.Fatalf("writableWorkspace = (%q, %v), want (ws, nil)", ws, err)
	}
}

// TestDecideWritableWorkspaceIsMembershipAware 编码 P1-20260913-MEMBERSHIP 的核心不变量：
// 写路径解析工作区时，**有身份的请求只能走成员资格**，绝不能回退到"组织内第一个工作区"。
//
// 这条不变量正是"用组织默认绕过成员资格"的直接反面：旧实现
// (SELECT id FROM workspaces WHERE organization_id=$1 ORDER BY created_at LIMIT 1)
// 会让只属于 wsB 的身份把数据写到 wsA。
func TestDecideWritableWorkspaceIsMembershipAware(t *testing.T) {
	ctx := context.Background()

	t.Run("session_workspace_wins_without_any_lookup", func(t *testing.T) {
		memberCalls, orgCalls := 0, 0
		got, err := decideWritableWorkspace(ctx,
			tenantScope{OrganizationID: "org", WorkspaceID: "ws-session"},
			policy.Principal{IdentityID: "id-1"},
			func(context.Context, string, string) (string, error) { memberCalls++; return "ws-other", nil },
			func(context.Context, string) (string, error) { orgCalls++; return "ws-org", nil })
		if err != nil || got != "ws-session" {
			t.Fatalf("got (%q, %v), want (ws-session, nil)", got, err)
		}
		if memberCalls != 0 || orgCalls != 0 {
			t.Fatalf("resolvers must not run when the session already carries a workspace (member=%d org=%d)", memberCalls, orgCalls)
		}
	})

	t.Run("identity_uses_membership_only", func(t *testing.T) {
		orgCalls := 0
		got, err := decideWritableWorkspace(ctx,
			tenantScope{OrganizationID: "org"},
			policy.Principal{IdentityID: "id-only-wsB"},
			func(context.Context, string, string) (string, error) { return "ws-B", nil },
			func(context.Context, string) (string, error) { orgCalls++; return "ws-A", nil })
		if err != nil || got != "ws-B" {
			t.Fatalf("got (%q, %v), want (ws-B, nil)", got, err)
		}
		if orgCalls != 0 {
			t.Fatal("must never fall back to the organization's first workspace for an identified principal")
		}
	})

	t.Run("identity_without_membership_fails_closed", func(t *testing.T) {
		orgCalls := 0
		got, err := decideWritableWorkspace(ctx,
			tenantScope{OrganizationID: "org"},
			policy.Principal{IdentityID: "id-nomem"},
			func(context.Context, string, string) (string, error) { return "", errNoTenantScope },
			func(context.Context, string) (string, error) { orgCalls++; return "ws-A", nil })
		if err == nil || got != "" {
			t.Fatalf("got (%q, %v), want fail closed", got, err)
		}
		if orgCalls != 0 {
			t.Fatal("no membership must not fall back to an organization-wide default")
		}
	})

	t.Run("identity_with_multiple_memberships_fails_closed", func(t *testing.T) {
		orgCalls := 0
		got, err := decideWritableWorkspace(ctx,
			tenantScope{OrganizationID: "org"},
			policy.Principal{IdentityID: "id-multi"},
			func(context.Context, string, string) (string, error) {
				return "", errors.New("workspace selection required")
			},
			func(context.Context, string) (string, error) { orgCalls++; return "ws-A", nil })
		if err == nil || got != "" {
			t.Fatalf("got (%q, %v), want fail closed when the membership is ambiguous", got, err)
		}
		if orgCalls != 0 {
			t.Fatal("an ambiguous membership must not be resolved by picking an organization default")
		}
	})

	t.Run("system_principal_uses_org_fallback", func(t *testing.T) {
		got, err := decideWritableWorkspace(ctx,
			tenantScope{OrganizationID: "org", System: true},
			policy.Principal{System: true},
			func(context.Context, string, string) (string, error) { return "ws-must-not-be-used", nil },
			func(context.Context, string) (string, error) { return "ws-org-first", nil })
		if err != nil || got != "ws-org-first" {
			t.Fatalf("got (%q, %v), want (ws-org-first, nil)", got, err)
		}
	})

	t.Run("missing_org_first_resolver_fails_closed", func(t *testing.T) {
		if _, err := decideWritableWorkspace(ctx,
			tenantScope{OrganizationID: "org", System: true},
			policy.Principal{System: true}, nil, nil); err == nil {
			t.Fatal("a nil resolver must fail closed, never silently succeed")
		}
	})
}

// ---------------------------------------------------------------------------
// 路由表静态回归：/api/v1 下每条路由要么显式声明权限，要么在文档化的公开白名单里。
//
// 这直接编码了 P0 的修复策略（"新路由必须二选一并写明理由"），
// 任何新增的裸路由都会让本测试失败，从而无法再次悄悄回归。
// ---------------------------------------------------------------------------

// legacyV1PublicWhitelist 是 backend/internal/api/LEGACY-V1-P0-FIX.md 中记录的公开白名单。
// 键为 "METHOD path"。
var legacyV1PublicWhitelist = map[string]string{
	"GET /me":                                                "前端探测登录态，只回显当前主体",
	"GET /system/architecture":                               "架构基线元数据",
	"GET /capabilities":                                      "能力注册表描述符",
	"GET /system/chains/{chain}/capabilities":                "RPC 网关能力位（链级静态元数据）",
	"GET /chains/capabilities":                               "链能力矩阵（链级静态元数据）",
	"GET /chains/{chain}/head":                               "链上公共 head",
	"GET /transactions/{chain}/{tx_hash}/receipt":            "链上公共回执",
	"GET /transactions/{chain}/{tx_hash}":                    "链上公共交易详情/解码",
	"GET /tokens/search":                                     "链上公共 Token 查询",
	"GET /tokens/{chain}/{contract}":                         "链上公共 Token 元数据",
	"GET /tokens/{chain}/{contract}/logo":                    "链上公共 Token 图标",
	"GET /methods/search":                                    "链上公共方法签名搜索",
	"GET /methods/{selector}":                                "链上公共 ABI/签名查询",
	"GET /api-control/provider-accounts/ankr/oauth/callback": "OAuth 回调：授权证明是服务端签发的一次性 PKCE state，不是会话",
}

func TestLegacyV1RouteTableRequiresDeclaredPermission(t *testing.T) {
	raw, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	source := string(raw)
	start := strings.Index(source, `r.Route("/api/v1"`)
	if start < 0 {
		t.Fatal("cannot locate the /api/v1 route block in server.go")
	}
	block := source[start:]
	if end := strings.Index(block, "// §21.4 认证 API"); end > 0 {
		block = block[:end]
	}

	seen := map[string]bool{}
	// 用字节值构造引号/换行，避免在测试源码里出现难以阅读的多重转义。
	dq := string(rune(34))
	nl := string(rune(10))
	for _, line := range strings.Split(block, nl) {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "api.") {
			continue
		}
		open := strings.Index(trimmed, "("+dq)
		if open < 0 {
			continue
		}
		rest := trimmed[open+2:]
		closeQuote := strings.Index(rest, dq)
		if closeQuote < 0 {
			continue
		}
		path := rest[:closeQuote]
		method := strings.ToUpper(strings.TrimSuffix(strings.TrimPrefix(trimmed[:open], "api."), ""))
		remainder := strings.TrimSpace(rest[closeQuote+1:])
		remainder = strings.TrimPrefix(remainder, ",")
		remainder = strings.TrimSpace(remainder)

		key := method + " " + path
		seen[key] = true
		if strings.HasPrefix(remainder, "s.require(") {
			continue
		}
		if _, ok := legacyV1PublicWhitelist[key]; ok {
			continue
		}
		t.Errorf("route %s is registered without s.require(...) and is not in the documented public whitelist; "+
			"either declare a permission or add it to the whitelist with a written justification", key)
	}
	if len(seen) == 0 {
		t.Fatal("no /api/v1 routes parsed; the route-table regression test is not actually guarding anything")
	}
	for key := range legacyV1PublicWhitelist {
		if !seen[key] {
			t.Errorf("public whitelist entry %q no longer exists in server.go; remove it from the whitelist", key)
		}
	}
}
