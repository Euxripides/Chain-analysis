package iam

// MFA 登录第二步在 iam 侧的策略与错误映射（纯单元测试，不连数据库）。

import (
	"net/http"
	"testing"
)

// 策略要求后台 MFA 但账号没注册时，必须是 403 语义（能力可用，只是没启用），
// 而不是此前的 501「未实现」。
func TestStatusForMFAEnrollmentRequired(t *testing.T) {
	status, code := StatusForError(ErrMFAEnrollmentRequired)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if code != "mfa_enrollment_required" {
		t.Fatalf("code = %q, want mfa_enrollment_required", code)
	}
	if !IsAuthError(ErrMFAEnrollmentRequired) {
		t.Fatal("ErrMFAEnrollmentRequired must be an auth error (never a 500)")
	}
	// 原有错误语义不得被改动（回归）。
	if status, _ := StatusForError(ErrMFAUnavailable); status != http.StatusNotImplemented {
		t.Fatalf("ErrMFAUnavailable status = %d, want 501", status)
	}
	if status, _ := StatusForError(ErrInvalidCredentials); status != http.StatusUnauthorized {
		t.Fatalf("ErrInvalidCredentials status = %d, want 401", status)
	}
}

func TestSetMFAChallengeGateIsNilSafe(t *testing.T) {
	var svc *Service
	svc.SetMFAChallengeGate(nil) // 不得 panic

	s := &Service{}
	if s.mfaGate != nil {
		t.Fatal("新构造的 Service 不得自带 MFA 门禁（未接线时登录行为必须与接入前一致）")
	}
	s.SetMFAChallengeGate(nil)
	if s.mfaGate != nil {
		t.Fatal("注入 nil 必须是 no-op")
	}
}

func TestMFAResultFor(t *testing.T) {
	if mfaResultFor("") != "" {
		t.Fatal("未走第二步时 MFAResult 必须为空，而不是 success")
	}
	if mfaResultFor("  ") != "" {
		t.Fatal("空白通过方式必须视为未走第二步")
	}
	if mfaResultFor("totp") != "success" || mfaResultFor("recovery_code") != "success" {
		t.Fatal("通过第二步时 MFAResult 必须为 success")
	}
}
