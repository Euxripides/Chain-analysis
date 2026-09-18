package iam

// 登录事件写入的两条防线（2026-09-16 审计链空洞事故的回归）：
//  1. result 越界必须在**触碰数据库之前**被拒绝，而不是让 INSERT 撞上 login_events_result_check；
//  2. 拒绝必须能被上报（调用方普遍写 `_ = RecordLoginEvent(...)`，否则完全静默）。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type guardStubDB struct{ queryRowCalls int }

func (s *guardStubDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("stub: Exec must not be called")
}

func (s *guardStubDB) QueryRow(context.Context, string, ...any) pgx.Row {
	s.queryRowCalls++
	return guardStubRow{}
}

type guardStubRow struct{}

func (guardStubRow) Scan(...any) error { return errors.New("stub: no database") }

// 允许集必须与迁移里的 login_events_result_check 逐字一致。
func TestLoginEventResultAllowedMatchesConstraint(t *testing.T) {
	for _, ok := range []string{"success", "failure", "denied", "blocked"} {
		if !loginEventResultAllowed(ok) {
			t.Fatalf("result %q 必须被允许（login_events_result_check 接受它）", ok)
		}
	}
	// "challenge" 是本轮真实写进代码、被约束拒绝、最终在链上留下空洞的那个值。
	for _, bad := range []string{"", "challenge", "SUCCESS", "denied ", "ok", "mfa_required"} {
		if loginEventResultAllowed(bad) {
			t.Fatalf("result %q 必须被拒绝（约束不接受它）", bad)
		}
	}
}

func TestRecordLoginEventRejectsInvalidResultBeforeTouchingDatabase(t *testing.T) {
	var reported []error
	SetLoginEventErrorHandler(func(err error) { reported = append(reported, err) })
	t.Cleanup(func() { SetLoginEventErrorHandler(nil) })

	db := &guardStubDB{}
	err := RecordLoginEvent(context.Background(), db, "test", LoginEvent{
		EventType: EventLoginDenied, Result: "challenge", ReasonCode: ReasonMFARequired, IdentityID: "00000000-0000-0000-0000-000000000000",
	})
	if !errors.Is(err, errLoginEventResultInvalid) {
		t.Fatalf("越界 result 必须返回 errLoginEventResultInvalid, got %v", err)
	}
	if db.queryRowCalls != 0 {
		t.Fatalf("越界 result 不得触碰数据库（实际 QueryRow 调用 %d 次）", db.queryRowCalls)
	}
	if len(reported) != 1 {
		t.Fatalf("越界 result 必须被上报一次，实际 %d 次", len(reported))
	}
}

// 合法 result 不得被这道防线挡下：它应当继续走到数据库（这里用一个必然失败的桩来证明）。
func TestRecordLoginEventAllowsValidResultThroughToDatabase(t *testing.T) {
	var reported []error
	SetLoginEventErrorHandler(func(err error) { reported = append(reported, err) })
	t.Cleanup(func() { SetLoginEventErrorHandler(nil) })

	db := &guardStubDB{}
	err := RecordLoginEvent(context.Background(), db, "test", LoginEvent{
		EventType: EventLoginDenied, Result: "denied", ReasonCode: ReasonMFARequired, IdentityID: "00000000-0000-0000-0000-000000000000",
	})
	if err == nil {
		t.Fatal("桩数据库应当让写入失败")
	}
	if errors.Is(err, errLoginEventResultInvalid) {
		t.Fatalf("合法 result 不得被本地校验拒绝: %v", err)
	}
	if db.queryRowCalls == 0 {
		t.Fatal("合法 result 必须继续走到数据库（链头锁）")
	}
	if len(reported) == 0 || !strings.Contains(reported[0].Error(), "stub") {
		t.Fatalf("写失败必须被上报，实际 %v", reported)
	}
}

// 空 result 默认按 failure 记账（既有行为不得回归）。
func TestRecordLoginEventDefaultsEmptyResultToFailure(t *testing.T) {
	db := &guardStubDB{}
	err := RecordLoginEvent(context.Background(), db, "test", LoginEvent{EventType: EventLoginFailure})
	if errors.Is(err, errLoginEventResultInvalid) {
		t.Fatalf("空 result 必须默认成 failure 而不是被拒绝: %v", err)
	}
}