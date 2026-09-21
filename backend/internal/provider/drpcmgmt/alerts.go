package drpcmgmt

import (
	"context"
	"fmt"
	"strings"
)

// 告警指纹固定，便于 ON CONFLICT 幂等更新与"条件消失即 RESOLVED"。
const (
	alertFingerprintSync    = "drpc-management-sync"
	alertFingerprintQuota   = "drpc-cu-day-quota"
	alertFingerprintBalance = "drpc-account-balance"
)

// managedAlertFingerprints 是本包负责的告警集合：写告警前只查这几个指纹，
// 不去动 api_control_alerts 里其它来源的行（API 控制状态、SLO 等）。
var managedAlertFingerprints = []string{alertFingerprintSync, alertFingerprintQuota, alertFingerprintBalance}

const (
	alertSeverityWarning  = "WARNING"
	alertSeverityCritical = "CRITICAL"

	// 规范 §1.5 的 Options 没有额度告警阈值字段（阈值属于 config.DRPCConfig 与 drpc.Options），
	// 这里沿用 config 默认值，只用于 api_control_alerts 的分级，不参与数据面限流。
	alertQuotaWarnPercent     = 80
	alertQuotaCriticalPercent = 95
)

// alertDecision 是一次同步算出的"当前应存在的告警"。
type alertDecision struct {
	Fingerprint string
	Severity    string
	Title       string
	Message     string
}

// alertDecisions 是纯函数：只根据快照算告警，便于单测覆盖阈值边界。
//
// 余额只对"确定的耗尽"（<= 0）告警：DRPC_BALANCE_WARN 不在本包 Options 内，
// 没有阈值就不编造阈值。额度只在 cu_day_limit > 0 时告警（未设置日上限不是异常）。
func alertDecisions(snap Snapshot) []alertDecision {
	decisions := make([]alertDecision, 0, 3)
	switch snap.SyncStatus {
	case SyncStatusError:
		decisions = append(decisions, alertDecision{
			Fingerprint: alertFingerprintSync,
			Severity:    alertSeverityCritical,
			Title:       "dRPC 管理同步失败",
			Message:     "同步状态为 error，错误分类 " + orNone(snap.SyncErrorClass) + "；余额与当日额度可能已过期。",
		})
	case SyncStatusNoToken:
		decisions = append(decisions, alertDecision{
			Fingerprint: alertFingerprintSync,
			Severity:    alertSeverityWarning,
			Title:       "dRPC 未配置管理 Token",
			Message:     "缺少 statistics_api_token / keys_api_token，无法获取账户余额与当日 CU 上限；数据面按本地档位运行。",
		})
	default:
		if gaps := syncGapClass(snap.SyncErrorClass); gaps != "" {
			decisions = append(decisions, alertDecision{
				Fingerprint: alertFingerprintSync,
				Severity:    alertSeverityWarning,
				Title:       "dRPC 管理数据不完整",
				Message:     "同步成功但存在数据缺口：" + gaps + "。",
			})
		}
	}

	if snap.Balance != nil && snap.Balance.Sign() <= 0 {
		decisions = append(decisions, alertDecision{
			Fingerprint: alertFingerprintBalance,
			Severity:    alertSeverityCritical,
			Title:       "dRPC 账户余额为 0",
			Message:     "管理接口返回的余额已为 0 或负数；请充值，否则数据面会因额度闸门停止请求。",
		})
	}

	if snap.CUDayLimit != nil && *snap.CUDayLimit > 0 {
		limit := *snap.CUDayLimit
		used := int64(0)
		if snap.CUSpentToday != nil {
			used = *snap.CUSpentToday
		}
		switch ratio := float64(used) / float64(limit) * 100; {
		case used >= limit:
			decisions = append(decisions, alertDecision{
				Fingerprint: alertFingerprintQuota,
				Severity:    alertSeverityCritical,
				Title:       "dRPC 当日 CU 额度已用尽",
				Message:     fmt.Sprintf("当日已用 %d CU，日上限 %d CU；数据面将拒绝新请求。", used, limit),
			})
		case ratio >= alertQuotaCriticalPercent:
			decisions = append(decisions, alertDecision{
				Fingerprint: alertFingerprintQuota,
				Severity:    alertSeverityWarning,
				Title:       "dRPC 当日 CU 额度接近上限",
				Message:     fmt.Sprintf("当日已用 %d/%d CU（%.1f%%）。", used, limit, ratio),
			})
		case ratio >= alertQuotaWarnPercent:
			decisions = append(decisions, alertDecision{
				Fingerprint: alertFingerprintQuota,
				Severity:    alertSeverityWarning,
				Title:       "dRPC 当日 CU 额度达到告警线",
				Message:     fmt.Sprintf("当日已用 %d/%d CU（%.1f%%）。", used, limit, ratio),
			})
		}
	}
	return decisions
}

// syncGapClass 提取"数据缺口"类错误分类（不是同步失败，但会误导后台展示）。
func syncGapClass(class string) string {
	var gaps []string
	for _, part := range strings.Split(class, ",") {
		switch trimmed := strings.TrimSpace(part); trimmed {
		case ErrClassNoToken, ErrClassUnexpectedShape:
			gaps = appendUnique(gaps, trimmed)
		}
	}
	return strings.Join(gaps, ",")
}

func orNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

const openAlertsSQL = `SELECT fingerprint, severity FROM api_control_alerts
	WHERE status = 'OPEN' AND fingerprint = ANY($1::text[])`

const upsertAlertSQL = `INSERT INTO api_control_alerts (severity, title, message, fingerprint, status)
	VALUES ($1, $2, $3, $4, 'OPEN')
	ON CONFLICT (fingerprint) DO UPDATE
	SET severity = EXCLUDED.severity,
	    title = EXCLUDED.title,
	    message = EXCLUDED.message,
	    status = 'OPEN',
	    last_seen_at = now(),
	    resolved_at = NULL`

const resolveAlertSQL = `UPDATE api_control_alerts
	SET status = 'RESOLVED', resolved_at = now(), last_seen_at = now()
	WHERE fingerprint = $1 AND status = 'OPEN'`

// writeAlerts 只写"跨阈值/状态变化"的告警：指纹已存在且 severity 相同就完全不碰数据库；
// 条件消失的告警置为 RESOLVED。表结构对齐 0011_api_control.sql（后续迁移新增的列都有默认值）。
func (s *Service) writeAlerts(ctx context.Context, snap Snapshot) {
	if s.pool == nil {
		return
	}
	desired := alertDecisions(snap)
	existing, err := s.openAlerts(ctx)
	if err != nil {
		s.log.Warn("drpc management: read open alerts failed", "error", err)
		return
	}
	wanted := make(map[string]bool, len(desired))
	for _, decision := range desired {
		wanted[decision.Fingerprint] = true
		if severity, ok := existing[decision.Fingerprint]; ok && severity == decision.Severity {
			continue // 状态未变化：不写数据库（工作流 D 要求）
		}
		if _, err := s.pool.Exec(ctx, upsertAlertSQL,
			decision.Severity, decision.Title, decision.Message, decision.Fingerprint); err != nil {
			s.log.Warn("drpc management: write alert failed",
				"fingerprint", decision.Fingerprint, "error", err)
		}
	}
	for fingerprint := range existing {
		if wanted[fingerprint] {
			continue
		}
		if _, err := s.pool.Exec(ctx, resolveAlertSQL, fingerprint); err != nil {
			s.log.Warn("drpc management: resolve alert failed", "fingerprint", fingerprint, "error", err)
		}
	}
}

func (s *Service) openAlerts(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, openAlertsSQL, managedAlertFingerprints)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	open := map[string]string{}
	for rows.Next() {
		var fingerprint, severity string
		if err := rows.Scan(&fingerprint, &severity); err != nil {
			return nil, err
		}
		open[fingerprint] = severity
	}
	return open, rows.Err()
}
