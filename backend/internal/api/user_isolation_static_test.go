package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件是"公网多用户 + 用户级数据隔离"的静态护栏
//（计划 docs/multiuser-login-and-isolation-plan.md §3.3 / §3.5.1 / §5 A3）。
//
// 这些都是**行为回归护栏**：它们不测某个函数的返回值，而是测"某个已被判定为缺陷的写法
// 不会再出现"。每条红灯都对应一个真实发生过的成因。

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", name, err)
	}
	return string(b)
}

// stripComment 去掉行内注释，避免"注释里提到旧写法"造成误报
//（沿用 internal/dataset/scope_tenant_test.go 的做法）。
func stripComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return line[:idx]
	}
	return line
}

// 1) 建用户不得再回退到「组织内最早创建的工作区」。
//
// 成因：旧实现 workspace_id 为空时执行
//
//	SELECT id::text FROM workspaces WHERE organization_id=$1 ORDER BY created_at LIMIT 1
//
// 实测把每个新用户塞进线上 default 工作区（30 名成员、26 个案件），于是"所有用户共用"。
func TestCreateUserDoesNotFallBackToFirstWorkspace(t *testing.T) {
	src := readSource(t, "sprint6_identity.go")
	for i, ln := range strings.Split(src, "\n") {
		code := stripComment(ln)
		if strings.Contains(code, "ORDER BY created_at LIMIT 1") {
			t.Fatalf("sprint6_identity.go:%d 仍回退到组织内最早创建的工作区：%s —— 个人空间不变量被破坏",
				i+1, strings.TrimSpace(ln))
		}
	}
	// 正向断言：必须真的建个人空间（kind='personal' + owner_identity_id）。
	if !strings.Contains(src, "'personal'") || !strings.Contains(src, "owner_identity_id") {
		t.Fatal("sprint6_identity.go 未创建个人空间（kind='personal' / owner_identity_id 缺失）")
	}
}

// 2) 个人空间的默认角色必须是 basic_user，且建用户路径不得绑 owner。
//
// 成因：owner 在权限目录里持有 178 个权限、其中 79 个是 admin:*（2026-09-18 实测）。
// 把它绑给每个新用户，等于把后台控制面一起给出去，与决策 D4「用户就基础操作」冲突。
func TestPersonalWorkspaceBindsBasicUserNotOwner(t *testing.T) {
	src := readSource(t, "sprint6_identity.go")
	if !strings.Contains(src, "'basic_user'") {
		t.Fatal("sprint6_identity.go 未绑定系统角色 basic_user（迁移 0068）")
	}
	for i, ln := range strings.Split(src, "\n") {
		code := stripComment(ln)
		if strings.Contains(code, "'owner'") {
			t.Fatalf("sprint6_identity.go:%d 在创建用户时引用了 owner 角色：%s —— 会把 admin:* 权限一并授予普通用户",
				i+1, strings.TrimSpace(ln))
		}
	}
}

// 3) 删除（离职）必须是软删：identities 不得被物理删除。
//
// 成因：audit_events_v2 与 login_events 通过外键引用 identities(id)，物理删除会破坏
// append-only 审计的可回溯性（AD-005 / ADR-018）。
func TestDeleteUserIsSoftDeleteAndArchives(t *testing.T) {
	src := readSource(t, "admin_delete_user.go")
	if strings.Contains(strings.ToUpper(src), "DELETE FROM IDENTITIES") {
		t.Fatal("admin_delete_user.go 物理删除 identities —— 必须软删 + 匿名化")
	}
	for _, want := range []string{"status = 'deleted'", "deleted_at = now()", "archived_at = now()", "ForceLogout", "revoked_at = now()"} {
		if !strings.Contains(src, want) {
			t.Fatalf("admin_delete_user.go 缺少 %q —— 删除语义不完整（软删/匿名化/归档/下线/撤权）", want)
		}
	}
	// 路由必须以 admin:user:delete 声明权限，且是 DELETE 方法。
	routes := readSource(t, "adminv1.go")
	if !strings.Contains(routes, `r.Delete("/users/{id}", s.require("admin:user:delete"`) {
		t.Fatal("adminv1.go 未以 admin:user:delete 声明 DELETE /users/{id}")
	}
}

// 4) 无自助注册面：任何地方都不得注册 register / signup 路由。
func TestNoSelfRegistrationRoutes(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("列目录失败: %v", err)
	}
	bad := []string{"/register", "/signup", "/sign-up", "handleAuthRegister", "handleRegister"}
	for _, f := range entries {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src := readSource(t, f)
		for i, ln := range strings.Split(src, "\n") {
			code := strings.ToLower(stripComment(ln))
			if !strings.Contains(code, "post(") && !strings.Contains(code, "get(") {
				continue
			}
			for _, b := range bad {
				if strings.Contains(code, b) {
					t.Fatalf("%s:%d 注册了自助注册路由：%s —— 需求明确禁止用户自行注册", f, i+1, strings.TrimSpace(ln))
				}
			}
		}
	}
}

// 5) 已删除账号必须在**每一条**登录路径上被拒绝。
//
// 成因：登录状态机原先只显式处理 disabled / locked，'deleted' 会从 switch 漏过去 —— 软删
// 之后账号仍能登录。这里要求 iam/service.go 至少两处（密码登录与 MFA 第二步）显式拒绝。
func TestDeletedIdentityIsRejectedOnEveryLoginPath(t *testing.T) {
	src := readSource(t, filepath.Join("..", "iam", "service.go"))
	if got := strings.Count(src, "StatusDeleted"); got < 2 {
		t.Fatalf("iam/service.go 中 StatusDeleted 出现 %d 次，期望 >= 2（密码登录 + MFA 第二步）", got)
	}
	identity := readSource(t, filepath.Join("..", "iam", "identity.go"))
	if !strings.Contains(identity, `StatusDeleted = "deleted"`) {
		t.Fatal("iam/identity.go 缺少 StatusDeleted 常量")
	}
}
