package audit

import (
	"context"
	"log/slog"
	"time"
)

// Reporter 让 audit 包把指标写到 api 包注册的 Prometheus 收集器上，
// 而不反向依赖 api 包（保持 internal/audit → 无上游依赖）。
type Reporter interface {
	SetOutboxStats(pending int64, oldestAgeSeconds float64)
	RecordChainVerify(chain string, valid bool)
	RecordArchiveRun(err error)
	SetChainSeq(chain string, seq int64)
}

var reporter Reporter

// RegisterReporter 由进程入口调用一次。
func RegisterReporter(r Reporter) { reporter = r }

// SchedulerOptions 控制后台任务周期。全部可配置，便于在测试中缩短。
type SchedulerOptions struct {
	// VerifyInterval 审计链校验周期。默认 1 小时，避免在链很长时反复做全量复算。
	VerifyInterval time.Duration
	// ArchiveInterval 检查"是否有需要归档的日期"的周期。默认 1 小时；
	// 真正的归档动作按日期幂等，重复运行不会产生重复锚点。
	ArchiveInterval time.Duration
	// MetricsInterval Outbox 积压指标采集周期。默认 30 秒。
	MetricsInterval time.Duration
	// OnStartVerify 启动时是否立即校验一次。生产建议开启，可在部署后立即发现链损坏。
	OnStartVerify bool
}

func DefaultSchedulerOptions() SchedulerOptions {
	return SchedulerOptions{
		VerifyInterval:  time.Hour,
		ArchiveInterval: time.Hour,
		MetricsInterval: 30 * time.Second,
		OnStartVerify:   true,
	}
}

// RunScheduler 在后台运行校验 / 归档 / 指标三类任务，直到 ctx 结束。
//
// 设计取舍：
//   - 三类任务各自独立 ticker，互不阻塞；单个任务失败只记录日志并计入指标，
//     不会终止整个调度器（否则一次数据库抖动就会永久停掉归档）。
//   - 归档按日期幂等（ON CONFLICT DO UPDATE），因此重启后补跑是安全的。
func RunScheduler(ctx context.Context, archiver *Archiver, log *slog.Logger, opt SchedulerOptions) {
	if archiver == nil || archiver.pool == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	if opt.VerifyInterval <= 0 {
		opt = DefaultSchedulerOptions()
	}

	verify := func() {
		runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		results, err := archiver.VerifyAll(runCtx)
		if err != nil {
			log.Error("audit chain verify failed", "err", err)
			return
		}
		for chain, res := range results {
			if reporter != nil {
				reporter.RecordChainVerify(chain, res.Valid)
				reporter.SetChainSeq(chain, res.ToSeq)
			}
			if !res.Valid {
				log.Error("audit chain is broken",
					"chain", chain, "checked", res.Checked, "reason", res.Reason, "broken_at_seq", res.BrokenAtSeq)
			}
		}
	}

	archive := func() {
		runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		anchors, err := archiver.ArchiveOnce(runCtx, time.Time{})
		if reporter != nil {
			reporter.RecordArchiveRun(err)
		}
		if err != nil {
			log.Error("audit archive failed", "err", err)
			return
		}
		for _, a := range anchors {
			log.Info("audit anchor written",
				"chain", a.Chain, "date", a.AnchorDate, "from", a.FromSeq, "to", a.ToSeq,
				"events", a.EventCount, "object", a.ObjectRef)
		}
	}

	collectMetrics := func() {
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		st, err := ReadOutboxStats(runCtx, archiver.pool)
		if err != nil {
			log.Warn("read outbox stats failed", "err", err)
			return
		}
		if reporter != nil {
			reporter.SetOutboxStats(st.Pending, st.OldestAgeSecond)
		}
		if st.OldestAgeSecond > 300 { // 5 分钟未投递即告警（§28.5 S3-OBS-01）
			log.Warn("outbox backlog is growing",
				"pending", st.Pending, "oldest_age_seconds", int(st.OldestAgeSecond), "retrying", st.FailedTotal)
		}
	}

	if opt.OnStartVerify {
		verify()
	}
	collectMetrics()

	verifyTicker := time.NewTicker(opt.VerifyInterval)
	archiveTicker := time.NewTicker(opt.ArchiveInterval)
	metricsTicker := time.NewTicker(opt.MetricsInterval)
	defer verifyTicker.Stop()
	defer archiveTicker.Stop()
	defer metricsTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-verifyTicker.C:
			verify()
		case <-archiveTicker.C:
			archive()
		case <-metricsTicker.C:
			collectMetrics()
		}
	}
}
