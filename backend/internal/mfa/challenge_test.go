package mfa

// 登录第二步（challenge）的纯单元测试：不连数据库，只钉死契约常量与 fail-closed 行为。
//
// 端到端的真实数据库证据在 internal/api/mfa_second_step_live_test.go（-tags live）。

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestChallengeTokenIsStoredAsDigestOnly(t *testing.T) {
	token := "3vT1sZm0Qk9Wc2pR7yXb4nH8dL5aE6uJ1oI0gF2hK9s"
	digest := HashChallengeToken(token)
	if len(digest) != 64 {
		t.Fatalf("digest length = %d, want 64 hex chars", len(digest))
	}
	if strings.Contains(digest, token) || digest == token {
		t.Fatal("digest must not contain the plaintext token")
	}
	if HashChallengeToken(token) != digest {
		t.Fatal("digest must be stable for the same token")
	}
	if HashChallengeToken(" "+token+" ") != digest {
		t.Fatal("digest must trim surrounding whitespace (clients may echo it back)")
	}
	if HashChallengeToken("") != "" || HashChallengeToken("   ") != "" {
		t.Fatal("empty token must hash to empty string, not to a real digest")
	}
	if HashChallengeToken(token+"x") == digest {
		t.Fatal("different tokens must not collide")
	}
}

// 契约常量：§21.4「challenge_id 仅可使用一次且 5 分钟过期」。
func TestChallengeContractConstants(t *testing.T) {
	if ChallengeTTL != 5*time.Minute {
		t.Fatalf("ChallengeTTL = %s, want 5m (契约)", ChallengeTTL)
	}
	if ChallengeMaxAttempts != 5 {
		t.Fatalf("ChallengeMaxAttempts = %d, want 5", ChallengeMaxAttempts)
	}
	if challengeTokenBytes < 32 {
		t.Fatalf("challengeTokenBytes = %d, want >= 32 (不可猜测)", challengeTokenBytes)
	}
	if ChallengeMethodTOTP == ChallengeMethodRecoveryCode {
		t.Fatal("两种通过方式必须可区分")
	}
}

// store 不可用（未接线数据库）时必须显式报错，绝不能返回"校验通过"。
func TestChallengeStoreUnavailableFailsClosed(t *testing.T) {
	var s *Store
	ctx := context.Background()
	if _, err := s.IssueLoginChallenge(ctx, ChallengeRequest{IdentityID: "x"}); err == nil {
		t.Fatal("IssueLoginChallenge on nil store must fail")
	}
	state, method, err := s.ConsumeLoginChallenge(ctx, "token", "123456")
	if err == nil {
		t.Fatal("ConsumeLoginChallenge on nil store must fail")
	}
	if state.IdentityID != "" || method != "" {
		t.Fatal("nil store must not report a verified challenge")
	}
	if _, err := s.IssueLoginChallenge(ctx, ChallengeRequest{}); err == nil {
		t.Fatal("IssueLoginChallenge without identity must fail")
	}
}

func TestChallengeHelpers(t *testing.T) {
	if audienceOrDefault("") != "user" || audienceOrDefault("  ") != "user" {
		t.Fatal("empty audience must default to user")
	}
	if audienceOrDefault(" admin ") != "admin" {
		t.Fatal("audience must be trimmed")
	}
	if nullUUID("") != nil || nullUUID("   ") != nil {
		t.Fatal("empty uuid must map to NULL, not to an empty string")
	}
	if got := nullUUID(" 6e192e52-d0aa-4e4a-9497-4953a332cfd3 "); got != "6e192e52-d0aa-4e4a-9497-4953a332cfd3" {
		t.Fatalf("nullUUID trimmed = %v", got)
	}
	if got := truncateText("abcdef", 3); got != "abc" {
		t.Fatalf("truncateText = %q", got)
	}
	if got := truncateText("abc", 3); got != "abc" {
		t.Fatalf("truncateText must keep exact-length strings, got %q", got)
	}
}
