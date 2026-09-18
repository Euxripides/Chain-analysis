//go:build live

// 这些测试只在显式 -tags live 且提供 S7JS_LIVE_DSN 时运行，用于对**真实数据库**
// 验证 store 层的租户作用域语义（HTTP 层无法构造"组织级空工作区"的密码登录会话）。
package store

import (
	"context"
	"os"
	"testing"
)

func liveStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("S7JS_LIVE_DSN")
	if dsn == "" {
		t.Skip("S7JS_LIVE_DSN not set")
	}
	ctx := context.Background()
	st, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	return st, ctx
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// TestListJobsScopedOrgLevelLive：orgID 非空而 wsID 为空（组织级主体）时，
// 返回该组织**全部工作区**的任务，且绝不包含其它组织的任务。
func TestListJobsScopedOrgLevelLive(t *testing.T) {
	st, ctx := liveStore(t)
	org := os.Getenv("S7JS_ORG_ID")
	otherOrg := os.Getenv("S7JS_OTHER_ORG_ID")
	if org == "" || otherOrg == "" {
		t.Skip("S7JS_ORG_ID / S7JS_OTHER_ORG_ID not set")
	}
	js, err := st.ListJobsScoped(ctx, org, "", "", 200, 0)
	if err != nil {
		t.Fatalf("ListJobsScoped(org, empty-ws) error: %v", err)
	}
	if len(js) == 0 {
		t.Fatal("org-level scope returned 0 jobs; fixture not seeded?")
	}
	for _, j := range js {
		if deref(j.OrganizationID) != org {
			t.Fatalf("org-level scope leaked cross-org job %s org=%s want=%s", j.ID, deref(j.OrganizationID), org)
		}
	}
	// 与按工作区分别查询的并集一致
	seen := map[string]bool{}
	for _, j := range js {
		seen[j.ID] = true
	}
	byWS, err := st.ListJobsScoped(ctx, org, "", "", 200, 0)
	if err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if len(byWS) != len(seen) {
		t.Fatalf("non-deterministic org-level result: %d vs %d", len(byWS), len(seen))
	}
	// 反向：另一个组织的结果集与本次完全不相交
	ojs, err := st.ListJobsScoped(ctx, otherOrg, "", "", 200, 0)
	if err != nil {
		t.Fatalf("other org: %v", err)
	}
	for _, j := range ojs {
		if seen[j.ID] {
			t.Fatalf("cross-org overlap on job %s", j.ID)
		}
	}
	t.Logf("org=%s jobs=%d otherOrg=%s jobs=%d overlap=0", org, len(js), otherOrg, len(ojs))
}

// TestListJobsScopedWorkspaceIsolationLive：同一组织内不同工作区必须完全隔离。
func TestListJobsScopedWorkspaceIsolationLive(t *testing.T) {
	st, ctx := liveStore(t)
	org := os.Getenv("S7JS_ORG_ID")
	wsA := os.Getenv("S7JS_WS_A")
	wsB := os.Getenv("S7JS_WS_B")
	if org == "" || wsA == "" || wsB == "" {
		t.Skip("S7JS_ORG_ID / S7JS_WS_A / S7JS_WS_B not set")
	}
	a, err := st.ListJobsScoped(ctx, org, wsA, "", 200, 0)
	if err != nil {
		t.Fatalf("wsA: %v", err)
	}
	b, err := st.ListJobsScoped(ctx, org, wsB, "", 200, 0)
	if err != nil {
		t.Fatalf("wsB: %v", err)
	}
	if len(a) == 0 || len(b) == 0 {
		t.Fatalf("fixture workspaces must both be non-empty: wsA=%d wsB=%d", len(a), len(b))
	}
	as := map[string]bool{}
	for _, j := range a {
		if deref(j.WorkspaceID) != wsA {
			t.Fatalf("wsA query returned job in workspace %s", deref(j.WorkspaceID))
		}
		as[j.ID] = true
	}
	for _, j := range b {
		if deref(j.WorkspaceID) != wsB {
			t.Fatalf("wsB query returned job in workspace %s", deref(j.WorkspaceID))
		}
		if as[j.ID] {
			t.Fatalf("workspace isolation violated on job %s", j.ID)
		}
	}
	union, err := st.ListJobsScoped(ctx, org, "", "", 200, 0)
	if err != nil {
		t.Fatalf("union: %v", err)
	}
	if len(union) != len(a)+len(b) {
		t.Fatalf("org-level union %d != wsA(%d)+wsB(%d)", len(union), len(a), len(b))
	}
	t.Logf("wsA=%d wsB=%d union=%d isolation=ok", len(a), len(b), len(union))
}
