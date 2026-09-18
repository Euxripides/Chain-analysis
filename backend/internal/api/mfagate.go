package api

// MFA 登录第二步的适配层：把 mfa.Store 适配成 iam.MFAChallengeGate。
//
// 放在 api 包是为了让 iam 与 mfa 互不依赖：iam 只声明接口（它不该认识持久化包），
// mfa 只提供持久化（它不该认识会话与登录编排）。适配发生在同时认识两者的这一层。

import (
	"context"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/mfa"
)

type mfaChallengeGate struct{ store *mfa.Store }

// newMFAChallengeGate 返回接口类型（而不是具体指针）：store 为 nil 时必须返回
// nil 接口，否则 iam 侧的 gate != nil 会为真、进而在调用时 panic。
func newMFAChallengeGate(store *mfa.Store) iam.MFAChallengeGate {
	if store == nil {
		return nil
	}
	return &mfaChallengeGate{store: store}
}

func (g *mfaChallengeGate) Enrolled(ctx context.Context, identityID string) (bool, error) {
	return g.store.Status(ctx, identityID)
}

func (g *mfaChallengeGate) IssueLoginChallenge(ctx context.Context, in iam.MFAChallengeRequest) (iam.MFAChallenge, error) {
	ticket, err := g.store.IssueLoginChallenge(ctx, mfa.ChallengeRequest{
		IdentityID:     in.IdentityID,
		Audience:       in.Audience,
		OrganizationID: in.OrganizationID,
		WorkspaceID:    in.WorkspaceID,
		ClientIP:       in.ClientIP,
		PeerIP:         in.PeerIP,
		ProxyChain:     in.ProxyChain,
		UserAgent:      in.UserAgent,
		EntryHost:      in.EntryHost,
		Scheme:         in.Scheme,
		RequestID:      in.RequestID,
		TraceID:        in.TraceID,
	})
	if err != nil {
		return iam.MFAChallenge{}, err
	}
	return iam.MFAChallenge{ID: ticket.ID, Token: ticket.Token, ExpiresAt: ticket.ExpiresAt}, nil
}
