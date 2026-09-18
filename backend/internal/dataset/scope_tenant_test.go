package dataset

import (
	"context"
	"os"
	"strings"
	"testing"
)

const tenantTestAddr = "0x0000000000000000000000000000000000000001"

func tenantReq(org, ws string) EnsureRequest {
	return EnsureRequest{
		Source: "investigation", Chain: "bsc",
		Subjects: []string{tenantTestAddr}, Datasets: []string{"transactions"},
		OrganizationID: org, WorkspaceID: ws,
	}
}

// R-2：租户作用域缺失必须 fail closed —— 不再回退 Store.DefaultOrgWorkspace。
// 该不变量对 HTTP 与非 HTTP（调查运行时 / 增量更新 Confirm）路径同等适用。
func TestEnsureRequiresTenantScope(t *testing.T) {
	s := &Service{}
	cases := []struct {
		name string
		req  EnsureRequest
	}{
		{"both-empty", tenantReq("", "")},
		{"org-only", tenantReq("org-1", "")},
		{"ws-only", tenantReq("", "ws-1")},
		{"blank-org", tenantReq("   ", "ws-1")},
		{"blank-ws", tenantReq("org-1", "\t")},
	}
	for _, tc := range cases {
		_, err := s.Ensure(context.Background(), tc.req)
		if err == nil {
			t.Fatalf("%s: 期望 fail closed，实得 nil（可能已静默回退默认组织）", tc.name)
		}
		if !strings.Contains(err.Error(), "tenant scope required") {
			t.Fatalf("%s: 期望 tenant scope required，实得 %v", tc.name, err)
		}
	}
}

func TestReuseCandidateLocalRequiresTenantScope(t *testing.T) {
	s := &Service{}
	_, err := s.ReuseCandidateLocal(context.Background(), tenantReq("", ""))
	if err == nil || !strings.Contains(err.Error(), "tenant scope required") {
		t.Fatalf("期望 fail closed，实得 %v", err)
	}
}

// 静态回归：dataset 包不得再出现 DefaultOrgWorkspace 调用（注释除外）。
func TestDatasetPackageDoesNotFallBackToDefaultOrg(t *testing.T) {
	b, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("读取 service.go 失败: %v", err)
	}
	for i, ln := range strings.Split(string(b), "\n") {
		code := ln
		if idx := strings.Index(code, "//"); idx >= 0 {
			code = code[:idx]
		}
		if strings.Contains(code, "DefaultOrgWorkspace(") {
			t.Fatalf("service.go:%d 仍存在 DefaultOrgWorkspace( 调用：%s —— R-2 租户不变量被破坏", i+1, strings.TrimSpace(ln))
		}
	}
}
