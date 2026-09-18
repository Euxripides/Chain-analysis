package api

// §7.3 / §16.7：功能开关必须在**真实请求路径**上求值。
//
// 这份文件把 feature_flags 接到真实路由上：`s.requireFlag(key, permission, handler)`
// 在放行之前用与后台「试算」完全相同的算法（internal/featureflag.Evaluate）算一次，并按结果：
//
//   允许 → 继续执行 handler；
//   拒绝 → 503 FEATURE_DISABLED（稳定 error code + flag key + reason + revision），
//          并写一条拒绝审计与一条评估记录；
//   读库失败或开关不存在 → 同样拒绝（fail closed），理由 evaluation_error / flag_not_found。
//
// 为什么评估记录要落库：`feature_flag_evaluations` 原先只有后台试算能写，
// 于是"开关是否真的在请求路径上被求值"没有可查询的证据。现在真实流量也会留下记录
// （拒绝时同步写、放行时异步写并按 subject/flag/reason 去重），运维可以直接查
// "线上到底按哪个 revision、什么 reason 放行或拒绝了谁"。
//
// 边界：
//   - **安全与恢复路径不得挂开关**（Kill Switch 执行/恢复、审批、登出、会话撤销）。
//     功能开关是"能力面"的灰度工具；把它接到恢复路径上等于给出一个"悄悄关掉恢复能力"的入口。
//     这条边界由 internal/api/featureflag_route_contract_test.go 静态断言。
//   - 评估失败与开关不存在都按拒绝处理：多一个"配置漏了就默认开放"的入口，比拒绝更危险。

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/featureflag"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// ErrorFeatureDisabled 是"能力面被功能开关关闭"的稳定错误码（§21.3 错误码前缀约定）。
const ErrorFeatureDisabled = "FEATURE_DISABLED"

// errFeatureFlagStore 表示开关存储未接线。
var errFeatureFlagStore = errors.New("feature flag store is not wired")

// requireFlag 把一条路由挂在功能开关之下。permission 是这条路由声明的权限，
// 只用于拒绝审计里的归因（审计要能回答"哪个能力面被关掉了"）。
func (s *Server) requireFlag(key, permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := PrincipalFrom(r.Context())
		decision, err := s.evaluateFlag(r.Context(), key)
		if err != nil {
			observability.FeatureFlagLookupFailed.WithLabelValues(key).Inc()
			s.auditDenyCodeDetail(r, p, permission, "feature_flag_error", ErrorFeatureDisabled,
				http.StatusServiceUnavailable, map[string]any{"flag": key, "reason": "evaluation_error", "error": err.Error()})
			writeErrorCode(w, r, http.StatusServiceUnavailable, ErrorFeatureDisabled,
				"该能力暂时不可用（功能开关读取失败）", map[string]any{"flag": key, "reason": "evaluation_error"})
			return
		}
		observability.FeatureFlagEvaluated.WithLabelValues(key, resultLabel(decision.Allowed), decision.Reason).Inc()
		if !decision.Allowed {
			// 拒绝是"真实路径求值"最关键的证据：同步写，保证响应返回时记录已可查询。
			s.recordFlagEvaluation(r, p, key, decision, true)
			s.auditDenyCodeDetail(r, p, permission, "feature_flag_disabled", ErrorFeatureDisabled,
				http.StatusServiceUnavailable, map[string]any{
					"flag": key, "reason": decision.Reason, "revision": decision.Revision, "bucket": decision.Bucket,
				})
			writeErrorCode(w, r, http.StatusServiceUnavailable, ErrorFeatureDisabled, "该能力当前未开放",
				map[string]any{"flag": key, "reason": decision.Reason, "revision": decision.Revision})
			return
		}
		// 放行走异步（去重后同一 subject/flag/reason 每分钟最多一条），不给热路径加一次写库。
		s.recordFlagEvaluation(r, p, key, decision, false)
		next(w, r)
	}
}

func resultLabel(allowed bool) string {
	if allowed {
		return "allowed"
	}
	return "denied"
}

// evaluateFlag 在真实请求上下文中求值：主体取当前 principal，环境取服务端环境。
func (s *Server) evaluateFlag(ctx context.Context, key string) (featureflag.Decision, error) {
	if s.flags == nil {
		return featureflag.Decision{}, errFeatureFlagStore
	}
	p := PrincipalFrom(ctx)
	flag, ok, err := s.flags.Lookup(ctx, key)
	if err != nil {
		if s.deps.Log != nil {
			s.deps.Log.Error("feature flag lookup failed; refusing the capability", "flag", key, "err", err)
		}
		return featureflag.Decision{}, err
	}
	if !ok {
		if s.deps.Log != nil {
			s.deps.Log.Warn("feature flag referenced by a route does not exist; refusing the capability", "flag", key)
		}
		return featureflag.Decision{Key: key, Allowed: false, Reason: "flag_not_found"}, nil
	}
	return featureflag.Evaluate(flag, p.IdentityID, p.OrganizationID, p.WorkspaceID, s.envName()), nil
}

func (s *Server) envName() string {
	if s.cfg == nil {
		return ""
	}
	return s.cfg.Env
}

// invalidateFlagCache 在开关写入后立即失效读取缓存：
// 否则运维把开关关掉之后，最长 10 秒内真实请求路径仍按旧值放行（"我明明关了"）。
func (s *Server) invalidateFlagCache() {
	if s == nil || s.flags == nil {
		return
	}
	s.flags.Invalidate()
}

// flagEvalDedup 是放行侧评估记录的去重窗口。
var (
	flagEvalDedupMu sync.Mutex
	flagEvalDedup   = map[string]time.Time{}
)

const flagEvalDedupWindow = time.Minute

// recordFlagEvaluation 把一次**真实请求**的求值写入 feature_flag_evaluations。
func (s *Server) recordFlagEvaluation(r *http.Request, p policy.Principal, key string, decision featureflag.Decision, syncWrite bool) {
	if s.deps.Store == nil {
		return
	}
	subjectType := "anonymous"
	if p.IdentityID != "" {
		subjectType = "user"
	}
	org, ws := p.OrganizationID, p.WorkspaceID
	if ws != "" && org == "" {
		// 0056 的作用域自洽约束：工作区不能脱离组织单独记录。
		ws = ""
	}
	requestID := metaFrom(r.Context()).RequestID
	dedupeKey := key + "|" + p.IdentityID + "|" + decision.Reason
	flagEvalDedupMu.Lock()
	if last, seen := flagEvalDedup[dedupeKey]; seen && time.Since(last) < flagEvalDedupWindow {
		flagEvalDedupMu.Unlock()
		return
	}
	flagEvalDedup[dedupeKey] = time.Now()
	flagEvalDedupMu.Unlock()

	write := func(ctx context.Context) {
		// 评估记录属于控制面证据，不加入被审计的业务事务：
		// 否则一次回滚会连带抹掉"这个请求被开关拒绝了"的记录。
		ctx = dbtx.WithoutTx(ctx)
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if _, err := s.deps.Store.Pool.Exec(ctx, `
			INSERT INTO feature_flag_evaluations (flag_key, flag_revision, environment, subject_type, subject_id,
			    organization_id, workspace_id, result, reason, bucket)
			VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,$8,$9,$10)`,
			key, decision.Revision, s.envName(), subjectType, p.IdentityID,
			org, ws, decision.Allowed, decision.Reason, decision.Bucket); err != nil {
			if s.deps.Log != nil {
				s.deps.Log.Warn("feature flag evaluation was not recorded", "flag", key, "request_id", requestID, "err", err)
			}
		}
	}
	if syncWrite {
		write(r.Context())
		return
	}
	go write(context.WithoutCancel(r.Context()))
}
