// V1.4：Global Ready Queue / 阶段 inflight / Lease Epoch / Retry Budget 的下载侧接线（§8/§9/§23/§27/§28/§33/§52/§56）。
package download

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// fetchV14State 运行态。
type fetchV14State struct {
	phaseMu    sync.Mutex
	phaseRatio float64 // TTFB/(TTFB+Body) 最近观测比（缺省 0.15）；waiting≈active*ratio（§23 phase-aware）
	ratioAt    time.Time
}

func (sc *Scheduler) v14Init() {
	if sc.v14 == nil {
		sc.v14 = &fetchV14State{phaseRatio: 0.15}
	}
}

// leaseMode 当前 Ready Queue 模式（V1.4 §55：默认 fifo = V1.1-V1.3 旧路径，一键可回退）。
func (sc *Scheduler) leaseMode() string {
	if !sc.cfg.Ankr.V14Enabled || !sc.cfg.Ankr.GlobalReadyQueueEnabled {
		return "fifo"
	}
	switch sc.cfg.Ankr.ReadyQueueMode {
	case "lpt", "weighted":
		return sc.cfg.Ankr.ReadyQueueMode
	default:
		return "fifo"
	}
}

// v14DispatchMode 返回 (ordering, needShadow)。
func (sc *Scheduler) v14DispatchMode() (string, bool) {
	if sc.cfg.Ankr.V14Enabled && sc.cfg.Ankr.GlobalReadyQueueEnabled {
		return sc.leaseMode(), false
	}
	if sc.cfg.Ankr.V14Enabled && sc.cfg.Ankr.GlobalReadyQueueShadow {
		return "fifo", true
	}
	return "fifo", false
}

// queueShadowSample §56：实际(fifo)领取顺序 vs V1.4(lpt) 预测顺序 → diff 落库（不改真实顺序）。
func (sc *Scheduler) queueShadowSample(ctx context.Context, actual []model.Shard) {
	if len(actual) == 0 {
		return
	}
	preview, err := sc.store.PreviewLeaseOrder(ctx, len(actual)*2, "lpt")
	if err != nil || len(preview) == 0 {
		return
	}
	mismatch := false
	rank := -1
	for i := 0; i < len(actual) && i < len(preview); i++ {
		if actual[i].ID != preview[i] {
			mismatch = true
			rank = i
			break
		}
	}
	if mismatch {
		observability.AnkrFetchQueueShadowMismatchTotal.Inc()
	}
	d := &model.FetchQueueDiff{
		SampledReady:     len(preview),
		ActualFirstShard: actual[0].ID,
		V14FirstShard:    preview[0],
		OrderMismatch:    mismatch,
		MismatchRank:     rank,
	}
	go func() {
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := sc.store.InsertFetchQueueDiff(cctx, d); err != nil {
			sc.log.Warn("queue shadow diff insert failed", "err", err)
		}
	}()
}

// v14RuntimeTick：safe target / ready+retry queue / phase-aware inflight / retry amp gauges（progressLoop 调用，§23/§28/§52）。
func (sc *Scheduler) v14RuntimeTick(ctx context.Context) {
	sc.v14Init()
	active := float64(atomic.LoadInt64(&sc.active))
	activeNonzero := float64(atomic.LoadInt64(&sc.activeNonzero))
	target := float64(sc.currentConcurrency())
	observability.AnkrFetchSafeTarget.Set(float64(sc.currentConcurrency()))
	observability.AnkrActiveFetch.Set(active)
	observability.AnkrActiveNonzeroFetch.Set(activeNonzero)
	if target > 0 {
		observability.AnkrEffectiveConcurrencyRatio.Set(activeNonzero / target)
	} else {
		observability.AnkrEffectiveConcurrencyRatio.Set(0)
	}
	if sc.store != nil {
		if pending, retryReady, _, err := sc.store.ReadyQueueStats(ctx); err == nil {
			observability.AnkrFetchReadyQueueDepth.Set(float64(pending + retryReady))
			observability.AnkrFetchRetryQueueDepth.Set(float64(retryReady))
		}
		// phase ratio：最近 5 分钟 provider_usage 的 TTFB/(TTFB+Body)（30s 缓存）
		// 修复（S7-DEF）：provider_usage 的时间列是 request_time，没有 created_at。
		// 原查询每次执行都报 "column created_at does not exist"，错误被 `_ =` 吞掉，
		// 导致本函数下方两项观测**永久不更新**（phaseRatio 停留在初值、AnkrRetryAmplification 从不写入），
		// 并以约 1 次/秒刷 PostgreSQL 错误日志。列名已更正为 request_time。
		now := time.Now()
		sc.v14.phaseMu.Lock()
		if now.Sub(sc.v14.ratioAt) > 30*time.Second {
			var ratio *float64
			if qerr := sc.store.Pool.QueryRow(ctx,
				"SELECT AVG(ttfb_ms::float8 / NULLIF(ttfb_ms + body_ms, 0)) FROM provider_usage "+
					"WHERE provider='ankr' AND ttfb_ms > 0 AND body_ms > 0 AND request_time > now() - interval '5 minutes'").Scan(&ratio); qerr != nil {
				// 失败绝不静默：旧值会被下游当作实时相位比使用，必须显式留痕
				slog.Warn("phase ratio 查询失败，phaseRatio 保持上一次取值（可能是陈旧值）", "err", qerr)
			}
			if ratio != nil && *ratio > 0.01 && *ratio < 0.99 {
				sc.v14.phaseRatio = *ratio
			}
			sc.v14.ratioAt = now
		}
		ratio := sc.v14.phaseRatio
		sc.v14.phaseMu.Unlock()
		observability.AnkrFetchWaitingTTFB.Set(active * ratio)
		observability.AnkrFetchBodyInflight.Set(active * (1 - ratio))
		// retry amplification（§28：外部 Provider 尝试 / 外部 Provider 成功，5 分钟窗口）
		//
		// 语义边界（必须与 progress.go 的失败判据保持一致）：
		//   * 成功状态集合是 ('ok','success') —— 只认 'ok' 会把 success 行漏算成失败。
		//     实测近 1 小时 ok=96 / success=25，漏算约 21%，会让 amplification 虚高。
		//   * cache_hit / singleflight_hit **不是外部 Provider 调用**，分子与分母都必须排除；
		//     否则缓存命中会同时抬高 attempts 与"未成功"计数，得到虚高的 amplification。
		//   * http_status >= 400 即使 status 写成成功也不计为成功。
		var attempts, okPages int64
		if qerr := sc.store.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FILTER (WHERE NOT COALESCE(cache_hit,false) AND NOT COALESCE(singleflight_hit,false) "+
				"AND COALESCE(status,'') NOT IN ('cache_hit','singleflight_hit')), "+
				"COUNT(*) FILTER (WHERE NOT COALESCE(cache_hit,false) AND NOT COALESCE(singleflight_hit,false) "+
				"AND COALESCE(status,'') IN ('ok','success') AND COALESCE(http_status,0) < 400) "+
				"FROM provider_usage WHERE provider='ankr' AND request_time > now() - interval '5 minutes'").Scan(&attempts, &okPages); qerr != nil {
			// 失败绝不静默：否则外部的 attempts/成功 会以 0 参与分母或保留旧值而被当作实时结果
			slog.Warn("retry amplification 查询失败，该指标保持上一次取值（可能是陈旧值）", "err", qerr)
		} else if okPages > 0 && attempts > 0 {
			observability.AnkrRetryAmplification.Set(float64(attempts) / float64(okPages))
		} else if okPages == 0 {
			// 5 分钟窗口内没有成功的外部调用：指标语义未定义，明确留痕而不是给一个看起来正常的旧值
			slog.Debug("retry amplification 窗口内无成功外部调用，指标未更新", "attempts", attempts, "ok", okPages)
		}
	}
}

// leaseEpochMismatch 判断 CAS epoch 失败（§33 split-brain 防护）。
func leaseEpochMismatch(err error) bool { return store.IsLeaseEpochMismatch(err) }

// v14WorkScore §9 LPT：Range 期望工作量 ≈ 期望页数（span/pageSize；profile 可后续细化）。
func v14WorkScore(from, to int64, pageSize int64) float64 {
	if pageSize <= 0 {
		pageSize = 10000
	}
	span := to - from + 1
	if span <= 0 {
		return 0
	}
	return math.Round(float64(span)/float64(pageSize)*1000) / 1000
}

// retryBudgetOK §27：次数 + 时间双预算。
func retryBudgetOK(attempt int, started time.Time, maxAttempts int, budget time.Duration) bool {
	if maxAttempts > 0 && attempt >= maxAttempts {
		return false
	}
	if budget > 0 && time.Since(started) >= budget {
		return false
	}
	return true
}
