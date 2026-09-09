// V1.1 Benchmark Harness（§45-§48）：固定 target_block 的 1/2/4/8 Range 极限性能实测。
// 数据完整性判定（§15）：Raw/parsed/inserted/unique/FINAL 五层核对 + 边界证据 + 重叠检测。
// Sweet Spot 学习（§17-§24）：只有 Integrity PASS 且无基础设施事件的样本参与学习。
package download

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// benchRunState 运行期监控（峰值采样 + 学习排除条件 §22）。
type benchRunState struct {
	excluded   bool
	spoolPeak  int64
	activePeak int
}

// benchmarkLoop 周期监控 RUNNING 的 benchmark，job 终局后回写指标并触发学习。
func (sc *Scheduler) benchmarkLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	states := map[string]*benchRunState{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runs, err := sc.store.ListBenchmarkRunning(ctx, 50)
			if err != nil {
				continue
			}
			for _, b := range runs {
				st := states[b.ID]
				if st == nil {
					st = &benchRunState{}
					states[b.ID] = st
				}
				// 运行期采样（§14：peak_active_shards / spool_peak_bytes；§22 排除条件）
				if sc.spool != nil {
					if used, uerr := sc.spool.UsedBytes(); uerr == nil && used > st.spoolPeak {
						st.spoolPeak = used
					}
				}
				if sc.bp != nil && sc.bp.State() == stateProtected {
					st.excluded = true // Spool Protection 期间样本不参与学习（§22）
				}
				if sc.pipeline != nil && sc.pipeline.agg != nil && !sc.pipeline.agg.Healthy() {
					st.excluded = true // CH 故障期间样本不参与学习（§22）
				}
				if b.JobID != "" {
					if n, aerr := sc.store.CountActiveShardsForJob(ctx, b.JobID); aerr == nil && n > st.activePeak {
						st.activePeak = n
					}
				}
				job, jerr := sc.store.GetJob(ctx, b.JobID)
				if jerr != nil {
					continue
				}
				if isTerminalJob(job.Status) {
					if err := sc.finalizeBenchmark(ctx, b, st); err != nil {
						sc.log.Error("benchmark finalize failed", "bench_id", b.ID, "err", err)
					} else {
						delete(states, b.ID)
					}
				}
			}
		}
	}
}

// finalizeBenchmark 汇总 job 的完整指标并回写（§14 全部字段），随后触发 Sweet Spot 学习。
func (sc *Scheduler) finalizeBenchmark(ctx context.Context, b model.BenchmarkRun, st *benchRunState) error {
	job, err := sc.store.GetJob(ctx, b.JobID)
	if err != nil {
		return err
	}
	b.WallClockMs = job.UpdatedAt.Sub(job.CreatedAt).Milliseconds()
	if b.WallClockMs < 0 {
		b.WallClockMs = 0
	}
	// 用量与百分位
	u, err := sc.store.BenchUsageStats(ctx, b.JobID)
	if err != nil {
		return err
	}
	b.Requests = u.Requests
	b.Attempts = u.Attempts
	b.Credits = u.Credits
	b.Cost = u.Cost
	b.Error429 = u.Error429
	b.ErrorConnLimit = u.ErrorConnLimit
	b.ErrorTimeout = u.ErrorTimeout
	b.ErrorOversized = u.ErrorOversized
	b.P50Ms, b.P95Ms, b.P99Ms = percentiles(u.Durations)
	// V1.2（§55/§56）：Provider Latency Context —— TTFB / Body 拆分 + 归一化输入
	b.ProviderLatencyP50Ms, b.ProviderLatencyP95Ms = b.P50Ms, b.P95Ms
	if len(u.TTFBDurations) > 0 {
		p50, p95, _ := percentiles(u.TTFBDurations)
		b.ProviderTTFBP50Ms, b.ProviderTTFBP95Ms = p50, p95
	}
	if len(u.BodyDurations) > 0 {
		p50, p95, _ := percentiles(u.BodyDurations)
		b.ProviderBodyP50Ms, b.ProviderBodyP95Ms = p50, p95
	}
	total := u.Attempts
	if total > 0 {
		b.RetryRate = float64(u.Retries) / float64(total)
		b.TimeoutRate = float64(u.ErrorTimeout) / float64(total)
		b.RateLimitRate = float64(u.Error429) / float64(total)
	}
	// 页面行数与一致性
	rows, parsed, inserted, gapAvg, gapMax, err := sc.store.BenchPageStats(ctx, b.JobID)
	if err != nil {
		return err
	}
	b.Rows = rows
	b.PageGapAvgMs = gapAvg
	b.PageGapP95Ms = gapMax
	parsedInsertedMismatch := parsed != inserted
	if parsedInsertedMismatch {
		// Defer the verdict until UNIQUE and boundary checks classify a
		// permitted transaction source duplicate.
		b.Error = fmt.Sprintf("parsed(%d) != inserted(%d)", parsed, inserted)
	}
	// 边界证据 + 重叠检测（§15/§16）
	evidence, overlap, missing, firstLastErr := sc.boundaryEvidence(ctx, b)
	if firstLastErr != nil {
		sc.log.Warn("boundary evidence partial", "bench_id", b.ID, "err", firstLastErr)
	}
	b.BoundaryEvidence = evidence
	b.RawOverlapRows = overlap
	b.BoundaryMissing = missing
	if overlap > 0 {
		b.IntegrityStatus = "FAIL"
		b.Error += fmt.Sprintf("; raw_overlap_rows=%d", overlap)
	}
	if missing > 0 {
		b.IntegrityStatus = "FAIL"
		b.Error += fmt.Sprintf("; boundary_missing=%d", missing)
	}
	// UNIQUE（FINAL 去重，§15：不能只看 FINAL，但也必须核对）
	addr := sc.benchmarkAddress(ctx, b)
	unique, uerr := sc.writer.CountFinal(ctx, b.Dataset, b.Chain, addr, 0, uint64(b.TargetBlock))
	if uerr != nil {
		sc.log.Warn("final count failed", "bench_id", b.ID, "err", uerr)
	} else {
		b.UniqueRows = int64(unique)
		if b.BoundaryEvidence == nil {
			b.BoundaryEvidence = map[string]any{}
		}
		b.BoundaryEvidence["raw_rows"] = b.Rows
		b.BoundaryEvidence["canonical_unique_rows"] = b.UniqueRows
		if b.Rows > b.UniqueRows {
			sourceDup := b.Rows - b.UniqueRows
			b.BoundaryEvidence["source_duplicate_rows"] = sourceDup
		}
		if b.IntegrityStatus != "FAIL" && b.UniqueRows != b.Rows {
			allowSource := b.Dataset == "transactions" && b.Rows > b.UniqueRows &&
				sc.cfg.Ankr.AllowSourceDuplicates && b.RawOverlapRows == 0 && b.BoundaryMissing == 0
			if !allowSource {
				b.IntegrityStatus = "FAIL"
				b.Error += fmt.Sprintf("; canonical_unique(%d) != raw_rows(%d)", b.UniqueRows, b.Rows)
			} else {
				b.BoundaryEvidence["duplicate_class"] = "SOURCE_DUPLICATE"
				b.Error += fmt.Sprintf("; source_duplicate_rows=%d (canonical set accepted)", b.Rows-b.UniqueRows)
			}
		} else if parsedInsertedMismatch {
			// If UNIQUE did not decrease, parsed/inserted divergence is not
			// explained by a permitted source duplicate.
			b.IntegrityStatus = "FAIL"
		}
	}
	if b.IntegrityStatus == "" || b.IntegrityStatus == "PENDING" {
		b.IntegrityStatus = "PASS"
	}
	// V1.2（§53/§103-§105）：Provider 延迟窗口归一化 + 异常窗口排除
	baselineAvg, baselineP95 := sc.providerBaseline(ctx, b)
	if baselineAvg > 0 && b.P50Ms > 0 && sc.cfg.Global.LatencyNormalizationEnabled {
		ratio := float64(b.P50Ms) / float64(baselineAvg)
		if ratio > 0.2 {
			if baselineP95 > 0 {
				b.ProviderWindowRatio = float64(b.P95Ms) / float64(baselineP95)
			}
			b.NormalizedWallClockMs = int64(float64(b.WallClockMs) / ratio) // §53：wall / latency_baseline_ratio
		}
	}
	if b.ProviderWindowRatio <= 0 && baselineAvg > 0 && b.P95Ms > 0 {
		b.ProviderWindowRatio = float64(b.P95Ms) / float64(baselineAvg)
	}
	// §105：异常 Provider 窗口（P95 > 历史 P95×3）排除出 Sweet Spot 学习，但仍保留证据
	if baselineP95 > 0 && b.P95Ms > int(baselineP95)*3 {
		b.ExcludedFromLearning = true
		if b.ExcludeReason == "" {
			b.ExcludeReason = "provider_anomaly_window: run p95=" + strconv.Itoa(b.P95Ms) + "ms > 3x baseline " + strconv.FormatInt(baselineP95, 10) + "ms"
		}
	} else if b.P95Ms > 0 && b.P95Ms > 60000 {
		b.ExcludedFromLearning = true
		if b.ExcludeReason == "" {
			b.ExcludeReason = "provider_anomaly_window: extreme p95 latency (no baseline available)"
		}
	}
	// 峰值（§14）
	b.PeakActiveShards = st.activePeak
	b.SpoolPeakBytes = st.spoolPeak
	b.PeakRSS = readPeakRSS()
	if sc.pipeline != nil && sc.pipeline.agg != nil {
		b.CHP95Ms = int(sc.pipeline.agg.InsertP95().Milliseconds())
	}
	b.ExcludedFromLearning = st.excluded || b.IntegrityStatus != "PASS"
	if job.Status != model.JobCompleted {
		// 未完整收敛的样本绝不允许进入 Sweet Spot 学习（§22：Data Integrity PASS 是学习前提）
		b.Status = "FAILED"
		b.IntegrityStatus = "FAIL"
		b.ExcludedFromLearning = true
		if b.Error == "" {
			b.Error = "job " + job.Status
		}
		if strings.Contains(strings.ToLower(job.Error), "range violation") || strings.Contains(strings.ToLower(b.Error), "range violation") {
			b.ExcludeReason = "RANGE_INTEGRITY_FAIL"
		}
	} else {
		b.Status = "COMPLETED"
	}
	if err := sc.store.UpdateBenchmarkRun(ctx, &b); err != nil {
		return err
	}
	// V1.4：Range Supported 证据 + Fetch Concurrency Run 记录（§14/§53；verified 只由显式 Certification 置位）
	sc.recordV14BenchEvidence(ctx, b)
	// Sweet Spot 学习（§22/§23：≥3 个稳定样本才更新 preferred_shard_count）
	if err := sc.learnSweetSpot(ctx, b); err != nil {
		sc.log.Warn("sweet spot learning failed", "err", err)
	}
	return nil
}

// recordV14BenchEvidence V1.4：benchmark 收敛后写入 range_supported 证据与 concurrency run（§14/§53）。
func (sc *Scheduler) recordV14BenchEvidence(ctx context.Context, b model.BenchmarkRun) {
	if b.Dataset == "transactions" && b.Status == "COMPLETED" {
		existing, _ := sc.store.GetRangeCertification(ctx, b.Chain, "transactions")
		verified := false
		ref := b.ID
		ver := 1
		if existing != nil {
			verified = existing.RangeVerified
			ref = existing.ReferenceRun
			ver = existing.VerificationVersion
		}
		cert := &model.RangeCertification{
			Chain: b.Chain, Dataset: "transactions",
			RangeSupported:      true,
			RangeVerified:       verified, // 只允许显式 Certification 置位（§14：禁止默认假定）
			ReferenceRun:        ref,
			Missing:             b.BoundaryMissing,
			RealDuplicates:      b.RawOverlapRows,
			BoundaryViolations:  0,
			UnionRows:           b.UniqueRows,
			VerificationVersion: ver,
		}
		if err := sc.store.UpsertRangeCertification(ctx, cert); err != nil {
			sc.log.Warn("range certification evidence upsert failed", "bench_id", b.ID, "err", err)
		}
	}
	if b.Status != "COMPLETED" || b.PeakActiveShards <= 0 {
		return
	}
	run := &model.FetchConcurrencyRun{
		RunID:             b.ID,
		Chain:             b.Chain,
		DatasetMix:        b.Dataset,
		ConcurrencyTarget: b.PeakActiveShards,
		WallMs:            b.WallClockMs,
		Rows:              b.Rows,
		UniqueRows:        b.UniqueRows,
		Requests:          b.Requests,
		Credits:           b.Credits,
		TTFBP50Ms:         b.ProviderTTFBP50Ms,
		TTFBP95Ms:         b.ProviderTTFBP95Ms,
		BodyP50Ms:         b.ProviderBodyP50Ms,
		BodyP95Ms:         b.ProviderBodyP95Ms,
		R429Count:         int64(b.Error429),
		TimeoutCount:      int64(b.ErrorTimeout),
		MaxMemory:         b.PeakRSS,
		MaxSpool:          b.SpoolPeakBytes,
		Pass:              b.IntegrityStatus == "PASS",
		ExcludeReason:     b.ExcludeReason,
	}
	if b.Attempts > 0 && b.Requests > 0 {
		run.RetryAmp = float64(b.Attempts) / float64(b.Requests)
	}
	if err := sc.store.InsertFetchConcurrencyRun(ctx, run); err != nil {
		sc.log.Warn("fetch concurrency run insert failed", "bench_id", b.ID, "err", err)
	}
}

// learnSweetSpot §17/§18：最佳墙钟 + 10% 边际收益阈值 → 最小够用 shard_count。
func (sc *Scheduler) learnSweetSpot(ctx context.Context, b model.BenchmarkRun) error {
	if b.ExcludedFromLearning || b.IntegrityStatus != "PASS" {
		return nil
	}
	samples, err := sc.store.IntegrityPassRunsForLearning(ctx, b.Chain, b.Dataset, 20)
	if err != nil || len(samples) < 3 {
		return err // 样本不足，不更新（§23）
	}
	best := map[int]int64{} // shard_count -> 最小（归一化）墙钟
	var totalRows, totalPages int64
	var totalWall int64
	var totalProvAvg, totalProvP95 int64
	for _, s := range samples {
		if s.TargetBlock != b.TargetBlock {
			continue // 只聚合同 target 的样本
		}
		wall := s.WallClockMs
		// V1.2（§53/§54/§104）：Sweet Spot 学习使用 Provider-latency 归一化墙钟，
		// 避免"策略相同但 Provider 延迟窗口不同"污染结论（§52）。
		if sc.cfg.Global.LatencyNormalizationEnabled && s.NormalizedWallClockMs > 0 {
			wall = s.NormalizedWallClockMs
		}
		if v, ok := best[s.ShardCount]; !ok || wall < v {
			best[s.ShardCount] = wall
		}
		totalRows += s.Rows
		totalPages += int64(s.ShardCount)
		totalWall += wall
		totalProvAvg += int64(s.ProviderLatencyP50Ms)
		totalProvP95 += int64(s.ProviderLatencyP95Ms)
	}
	if len(best) < 2 {
		return nil
	}
	// 全局最佳
	bestWall := int64(1 << 62)
	for _, w := range best {
		if w < bestWall {
			bestWall = w
		}
	}
	// 10% 边际阈值（§18）：选择墙钟 ≤ best×1.10 的最小 shard_count（跳过 adaptive=0）
	threshold := float64(bestWall) * 1.10
	recommended := 8
	for _, s := range samples {
		if s.TargetBlock != b.TargetBlock || s.ShardCount <= 0 {
			continue
		}
		if float64(best[s.ShardCount]) <= threshold && s.ShardCount < recommended {
			recommended = s.ShardCount
		}
	}
	density := densityClass(totalRows, b.TargetBlock)
	n := len(samples)
	avgWall := totalWall / int64(n)
	provAvg := totalProvAvg / int64(n)
	provP95 := totalProvP95 / int64(n)
	sc.log.Info("sweet spot learned", "chain", b.Chain, "dataset", b.Dataset,
		"density", density, "recommended_shards", recommended, "samples", n,
		"normalized", sc.cfg.Global.LatencyNormalizationEnabled)
	return sc.store.UpsertSweetSpotProfile(ctx, b.Chain, b.Dataset, b.Dataset, density,
		recommended, avgWall, totalRows/int64(n), totalPages/int64(n),
		provAvg, provP95, 0, 0, 0, n)
}

// providerBaseline §55/§104：同 (chain,dataset) 历史 Provider 延迟基线（page profile 优先）。
func (sc *Scheduler) providerBaseline(ctx context.Context, b model.BenchmarkRun) (avgMs, p95Ms int64) {
	if prof, err := sc.store.GetPageProfile(ctx, b.Chain, b.Dataset, b.Dataset); err == nil && prof != nil && prof.PagesObserved > 0 {
		return prof.AvgLatencyMs, prof.P95LatencyMs
	}
	return 0, 0
}

func densityClass(rows, targetBlock int64) string {
	if targetBlock <= 0 {
		return "NORMAL"
	}
	rpb := float64(rows) / float64(targetBlock)
	switch {
	case rpb < 0.001:
		return "COLD"
	case rpb < 0.005:
		return "NORMAL"
	case rpb < 0.05:
		return "HOT"
	default:
		return "VERY_HOT"
	}
}

// boundaryEvidence §15/§16：逐 Shard 边界证据 + 重叠/缺口检测。
func (sc *Scheduler) boundaryEvidence(ctx context.Context, b model.BenchmarkRun) (map[string]any, int64, int64, error) {
	shards, err := sc.store.ShardsByJob(ctx, b.JobID)
	if err != nil {
		return nil, 0, 0, err
	}
	effective := shards[:0]
	for _, sh := range shards {
		if !store.IsSupersededTailSplit(sh.Status, sh.Error) {
			effective = append(effective, sh)
		}
	}
	shards = effective
	sort.Slice(shards, func(i, j int) bool { return shards[i].FromBlock < shards[j].FromBlock })
	addr := sc.benchmarkAddress(ctx, b)
	var overlap int64
	missing := rangeCoverageMissing(shards, b.TargetBlock)
	var rows []map[string]any
	for _, sh := range shards {
		pages, perr := sc.store.PagesByShard(ctx, sh.ID)
		if perr != nil {
			return nil, 0, 0, perr
		}
		var minB, maxB, pageRows int64
		var pagesN int
		for _, p := range pages {
			pageRows += int64(p.RowsFetched)
			pagesN++
			if p.FromBlock > 0 && (minB == 0 || p.FromBlock < minB) {
				minB = p.FromBlock
			}
			if p.ToBlock > maxB {
				maxB = p.ToBlock
			}
			// 重叠检测：页面行块越出 Shard 区间（客户端过滤兜底之外的双保险）
			if sh.FromBlock > 0 && p.FromBlock > 0 && p.FromBlock < sh.FromBlock {
				overlap += int64(p.RowsFetched)
			}
			if sh.ToBlock > 0 && p.ToBlock > sh.ToBlock {
				overlap += int64(p.RowsFetched)
			}
		}
		first, last := "", ""
		if maxB > 0 {
			f, l, ferr := sc.writer.BoundaryHashes(ctx, b.Dataset, b.Chain, addr, uint64(sh.FromBlock), uint64(sh.ToBlock))
			if ferr == nil {
				first, last = f, l
			}
		}
		ev := map[string]any{
			"range_start": sh.FromBlock, "range_end": sh.ToBlock,
			"first_block": minB, "last_block": maxB,
			"first_tx_hash": first, "last_tx_hash": last,
			"rows": pageRows, "pages": pagesN, "status": sh.Status,
		}
		rows = append(rows, ev)
	}
	return map[string]any{"ranges": rows}, overlap, missing, nil
}

// rangeCoverageMissing measures gaps in planned inclusive shard ranges, not
// gaps between observed transaction blocks (addresses are naturally sparse).
func rangeCoverageMissing(shards []model.Shard, targetBlock int64) int64 {
	if targetBlock < 0 {
		return 0
	}
	cursor := int64(0)
	var missing int64
	for _, sh := range shards {
		if sh.ToBlock < cursor {
			continue
		}
		if sh.FromBlock > cursor {
			missing += sh.FromBlock - cursor
		}
		if sh.ToBlock >= cursor {
			cursor = sh.ToBlock + 1
		}
		if cursor > targetBlock {
			break
		}
	}
	if cursor <= targetBlock {
		missing += targetBlock - cursor + 1
	}
	return missing
}

func (sc *Scheduler) benchmarkAddress(ctx context.Context, b model.BenchmarkRun) string {
	subs, err := sc.store.SubjectsByJob(ctx, b.JobID)
	if err != nil || len(subs) == 0 {
		return ""
	}
	return subs[0].Subject
}

// readPeakRSS 读取 Worker 进程 VmHWM（整个进程峰值，多任务共享时为近似值，仅作证据）。
func readPeakRSS() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseInt(fields[1], 10, 64)
				return kb << 10
			}
		}
	}
	return 0
}

func percentiles(durations []int) (p50, p95, p99 int) {
	if len(durations) == 0 {
		return 0, 0, 0
	}
	sorted := append([]int(nil), durations...)
	sort.Ints(sorted)
	p50 = sorted[(len(sorted)*50)/100]
	p95 = sorted[(len(sorted)*95)/100]
	p99 = sorted[(len(sorted)*99)/100]
	return
}
