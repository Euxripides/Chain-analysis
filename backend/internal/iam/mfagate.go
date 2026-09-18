package iam

// 登录第二步（MFA 挑战）在 iam 侧的接口面。
//
// 分层考虑：iam 不 import mfa 包（mfa 是更底层的持久化包，也不该反向依赖 iam），
// 因此这里只声明最小能力接口，由 api 层用 mfa.Store 适配注入（见 api/mfagate.go）。
// gate 为 nil 时登录行为与接入前完全一致 —— 这是"未接线就退回原语义"的显式开关，
// 而不是静默放行：api 层只有在数据库不可用时才不注入。

import (
	"context"
	"time"
)

// MFAChallengeGate 是登录第二步所需的最小能力面。
type MFAChallengeGate interface {
	// Enrolled 返回该身份是否已确认注册 MFA。
	// 返回错误时调用方必须 fail closed（拒绝本次登录），不得假设"未启用"。
	Enrolled(ctx context.Context, identityID string) (bool, error)
	// IssueLoginChallenge 签发一次性挑战；明文 Token 只在返回值里出现一次。
	IssueLoginChallenge(ctx context.Context, in MFAChallengeRequest) (MFAChallenge, error)
}

// MFAChallengeRequest 是签发挑战所需的上下文（服务端从本次登录请求里取，不用客户端自报）。
type MFAChallengeRequest struct {
	IdentityID     string
	Audience       string
	OrganizationID string
	WorkspaceID    string
	ClientIP       string
	PeerIP         string
	ProxyChain     []string
	UserAgent      string
	EntryHost      string
	Scheme         string
	RequestID      string
	TraceID        string
}

// MFAChallenge 是签发给客户端的挑战：Token 即响应里的 challenge_id。
type MFAChallenge struct {
	ID        string
	Token     string
	ExpiresAt time.Time
}

// MFACompletion 是第二步校验通过后建会话所需的上下文。
//
// 全部来自挑战落库时的服务端记录，客户端只能提供 challenge_id 与验证码。
type MFACompletion struct {
	IdentityID     string
	Audience       string
	OrganizationID string
	WorkspaceID    string
	ClientIP       string
	PeerIP         string
	ProxyChain     []string
	UserAgent      string
	EntryHost      string
	Scheme         string
	Method         string
	RequestID      string
	TraceID        string
}
