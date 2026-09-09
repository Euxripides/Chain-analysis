// V1.2 Count-aware Planning（§28-§31/§76/§77/§26/§44-§47）。
// 原则：
//
//	Profile HIT → 优先 existing preferred_shard_count（§28/§30/§96），Count 只用于 ETA/排序；
//	Profile MISS → 用 Count Estimate 给出 Initial Shard Recommendation（§28/§31/§95）；
//	Count=0 → 1 个 Ankr verification 确认后才允许 zero-row Coverage（§26/§109）；
//	Count 永不参与停止条件 / Coverage 真值（§27/§25）。
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// subjectHash 与 Benchmark/观测一致：只落 hash，不落明文地址（§87）。
func subjectHash(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:])[:16]
}

// v12Estimate 一次 (subject,dataset) 的 Plan 决策（§76/§77 Planner Card 内容）。
type v12Estimate struct {
	Source         string // chainbase | cache | profile | fallback
	PredictedRows  int64  // adjusted（-1 = 未知）
	PredictedPages int64  // -1 = 未知
	ProfileHit     bool
	Preferred      int // Profile HIT 的 preferred_shard_count / 模式默认
	Initial        int // 最终采用 initial shards
	V11Initial     int // V1.3：Shadow Diff 的 old（V1.1/V1.2 现状决策）基准
	Reason         string
	ZeroEmpty      bool // 已 Ankr verify 确认空 → zero-row Coverage 并跳过下载
	RawCount       int64
}

// multiRangeEligibleV12 数据集是否允许多 Range 分片（V1.1 实测 token_transfers/logs；
// V1.2 的 transactions 默认关闭，需显式 ANKR_TRANSACTIONS_RANGE_SHARDING_ENABLED=true；
// V1.4 §14：transactions 额外要求 Range Certification（ankr_range_certifications.range_verified=true））。
func (p *Planner) multiRangeEligibleV12(chain, ds string) bool {
	// TronGrid address-index pagination is one strictly ordered fingerprint
	// stream per address. Splitting it by resolved block ranges would replay the
	// same history in every shard. Ankr fallback windows are internal to the
	// hybrid provider and do not require planner-level fan-out.
	if chain == "tron" && ds == "token_transfers" {
		return false
	}
	switch ds {
	case "token_transfers", "logs":
		return true
	case "transactions":
		if !p.transactionsRange {
			return false
		}
		return p.txRangeCertified(chain)
	}
	return false
}

// multiRangeEligibleForJob permits an explicit fixed-shard benchmark to test
// transactions range semantics before production certification exists. This is
// the isolated certification path only; ordinary jobs still require both the
// feature flag and a verified certification row.
func (p *Planner) multiRangeEligibleForJob(job *model.DownloadJob, ds string) bool {
	if job != nil && job.Source == "benchmark" && ds == "transactions" && job.Stats != nil {
		if n, ok := job.Stats["benchmark_shard_count"].(float64); ok && n >= 1 && n <= 256 {
			return true
		}
	}
	if job == nil {
		return false
	}
	return p.multiRangeEligibleV12(job.Chain, ds)
}

// txRangeCertified V1.4 §14：transactions Range 语义必须实机认证（1R==2R==4R==8R，missing/dup/boundary=0）。
// store 不可用（单元测试）时保持旧语义（env 为准），生产以认证行为准。
func (p *Planner) txRangeCertified(chain string) bool {
	if p.store == nil {
		return !p.txCertRequired
	}
	cert, err := p.store.GetRangeCertification(context.Background(), chain, "transactions")
	if err != nil {
		p.log.Warn("range certification lookup failed; transactions multi-range disabled", "chain", chain, "err", err)
		return false
	}
	if cert == nil || !cert.RangeVerified {
		p.log.Warn("transactions range NOT certified; multi-range disabled (run certification first)", "chain", chain)
		return false
	}
	return true
}

// v12Active Count Oracle 数据面是否参与该 (job,dataset)（Shadow Mode 也计入：观测不改 Planner）。
func (p *Planner) v12Active(ctx context.Context, job *model.DownloadJob, ds string) bool {
	if p.oracle == nil || !p.oracle.Enabled() || !p.oracle.Configured() {
		return false
	}
	if job.Source == "benchmark" {
		return false // Benchmark 是固定 shard_count 对照实验（§45/§46）
	}
	return p.oracle.Capable(ctx, job.Chain, ds)
}

// preferredPageSize 10K-first（§59）：画像成熟才替换；失败默认 10000。
func (p *Planner) preferredPageSize(ctx context.Context, chain, ds string) int {
	size := 10000
	if prof, err := p.store.GetPageProfile(ctx, chain, ds, ds); err == nil && prof != nil && prof.PreferredPageSize > 0 {
		size = prof.PreferredPageSize
	}
	return size
}

// estimateFor 记录 estimate / decision / observation；Zero 路径负责 Ankr verify（§26）。
// 返回 nil 表示该 (job,dataset) 不参与 Count 平面（保持 V1.1 Planner 完全不变）。
func (p *Planner) estimateFor(ctx context.Context, job *model.DownloadJob, subj model.Subject, item model.DatasetItem, ds string) *v12Estimate {
	if !p.v12Active(ctx, job, ds) {
		return nil
	}
	// 已有 profile（§30/§77：HIT 优先 preferred，Count 只用于 ETA/排序）；
	// MISS → 模式默认（economy1/balanced2/turbo4），与 V1.1 shardCountFor 一致。
	mode := job.Mode
	if mode == "" {
		mode = "balanced"
	}
	defaultBase := 2
	switch mode {
	case "economy":
		defaultBase = 1
	case "turbo":
		defaultBase = 4
	}
	preferred := 0
	profileHit := false
	if n, samples, err := p.store.GetSweetSpotProfile(ctx, job.Chain, ds, ds, "NORMAL"); err == nil && samples >= 3 && n > 0 {
		preferred = n
		profileHit = true
	}
	base := defaultBase
	if profileHit {
		switch mode {
		case "economy":
			base = maxInt(1, preferred/2)
		case "turbo":
			base = minInt(16, preferred*2)
		default:
			base = preferred
		}
	}
	est := &v12Estimate{ProfileHit: profileHit, Preferred: base, V11Initial: base}

	// 需要网络 Count 的场景：Shadow Mode（§89 全部记录）或 Profile MISS（§95）
	needCount := !profileHit || !p.oracle.Aware()
	if !needCount {
		est.Source = "profile"
		est.Initial = base
		est.Reason = "learned profile"
		p.commitEstimate(ctx, job, subj, item, ds, est)
		return est
	}

	// §16 软预算：超出 Planning Deadline 后不再发起新的 Count（直接走 V1.1 回退）
	if !p.budgetOpen() {
		est.Source = "fallback"
		est.Initial = base
		est.Reason = "planning_deadline_elapsed"
		p.commitEstimate(ctx, job, subj, item, ds, est)
		return est
	}
	head := job.HeadBlock
	if head <= 0 && job.ToBlock != nil && *job.ToBlock > 0 {
		head = *job.ToBlock
	}
	from, to := uint64(0), uint64(0)
	if job.RangeType == "block" && job.FromBlock != nil && job.ToBlock != nil && *job.ToBlock >= *job.FromBlock && *job.ToBlock > 0 {
		from, to = uint64(*job.FromBlock), uint64(*job.ToBlock)
	} else if head > 0 {
		from, to = 0, uint64(head)
	}
	req := model.CountRequest{
		Chain: job.Chain, Dataset: ds, Address: subj.Subject,
		FromBlock: from, ToBlock: to, TargetBlock: uint64(head),
	}
	planCtx := context.WithValue(ctx, "job_id", job.ID)
	res := p.oracle.Count(planCtx, req)
	obsProvider := res.Provider
	if obsProvider == "" {
		obsProvider = "chainbase"
	}
	obs := &model.CountObservation{
		Provider: obsProvider, Chain: job.Chain, Dataset: ds,
		AddressHash: subjectHash(subj.Subject),
		FromBlock:   int64(from), ToBlock: int64(to),
		RawCount: res.RawCount, AdjustedCount: res.AdjustedCount,
		LatencyMS: int(res.LatencyMS), CacheHit: res.CacheHit,
		Confidence: res.Confidence, JobID: job.ID,
	}
	est.RawCount = res.RawCount
	if res.RawCount < 0 {
		obs.Status = "error"
		obs.ErrorClass = res.FailureClass
		est.Source = "fallback"
		est.Reason = "count_" + res.FailureClass + ": " + truncateStr(res.FailureMsg, 160)
		est.Initial = base
		p.oracle.RecordObservation(obs)
		p.commitEstimate(ctx, job, subj, item, ds, est)
		return est
	}
	obs.Status = "ok"
	obs.AdjustedCount = res.AdjustedCount
	est.Source = res.Source

	if res.RawCount == 0 {
		// Count=0：必须 Ankr verify（§26）；Profile MISS + aware + verify 开启且多 Range 数据集才走 zero Coverage
		if p.oracle.Aware() && p.zeroVerify && p.multiRangeEligibleV12(job.Chain, ds) {
			empty, verifyErr := p.ankrZeroVerify(planCtx, job, subj, ds, from, to)
			if verifyErr == nil && empty {
				// 确认空 → zero-row Coverage（只在此路径允许，§26）
				markFrom, markTo := int64(0), int64(0)
				if to > 0 {
					markFrom, markTo = int64(from), int64(to)
				} else if head > 0 {
					markTo = head
				}
				if markTo > 0 && markFrom <= markTo {
					if err := p.store.AddCoverage(ctx, job.Chain, subj.Subject, ds, "ankr", markFrom, markTo, 0, 0, head); err != nil {
						p.log.Warn("zero coverage commit failed", "dataset", ds, "err", err)
					}
				}
				obs.Status = "zero_verify_empty"
				est.Source = "chainbase"
				est.PredictedRows = 0
				est.PredictedPages = 0
				est.Initial = 1
				est.Reason = "count_zero_ankr_verify_empty"
				est.ZeroEmpty = true
				p.oracle.RecordObservation(obs)
				p.markZeroSkip(item.ID)
				_ = p.store.UpdateDatasetStatus(ctx, item.ID, model.ItemCompleted)
				p.commitEstimate(ctx, job, subj, item, ds, est)
				return est
			}
			if verifyErr == nil {
				obs.Status = "zero_verify_found_data"
				est.Source = "fallback"
				est.Reason = "count_zero_ankr_has_data"
				est.Initial = base
				p.oracle.RecordObservation(obs)
			} else {
				obs.Status = "error"
				obs.ErrorClass = "upstream"
				est.Source = "fallback"
				est.Reason = "zero_verify_failed_fallthrough"
				est.Initial = base
				p.oracle.RecordObservation(obs)
			}
			p.commitEstimate(ctx, job, subj, item, ds, est)
			return est
		}
		// Shadow / verify 关闭：仅记录（Planner 保持 V1.1）
		est.Source = res.Source
		est.Initial = base
		est.Reason = "count_zero_shadow"
		p.oracle.RecordObservation(obs)
		p.commitEstimate(ctx, job, subj, item, ds, est)
		return est
	}

	// 正常 Count：Predicted Pages / Initial Shards（§29/§31）
	predicted := res.AdjustedCount
	if predicted <= 0 {
		predicted = res.RawCount
	}
	pageSize := p.preferredPageSize(ctx, job.Chain, ds)
	pages := (predicted + int64(pageSize) - 1) / int64(pageSize)
	est.Source = res.Source
	est.PredictedRows = predicted
	est.PredictedPages = pages
	if p.oracle.Aware() {
		// Count-aware（§95）：Profile MISS 时使用 Count 建议（§28/§31）
		rec := recommendInitialShards(pages)
		if !p.multiRangeEligibleV12(job.Chain, ds) {
			rec = 1 // 非多 Range 数据集单 Shard（V1.1 行为不变）
		}
		est.Initial = p.modeShards(mode, rec)
		est.Reason = "count_estimate"
	} else {
		// Shadow Mode（§89）：只记录 Count / Prediction Error，不改 V1.1 Planner
		est.Initial = base
		est.Reason = "count_estimate_shadow"
	}
	p.oracle.RecordObservation(obs)
	p.commitEstimate(ctx, job, subj, item, ds, est)
	return est
}

// modeShards economy 减半 / turbo 翻倍（与 V1.1 shardCountFor 语义一致，§41-44）。
func (p *Planner) modeShards(mode string, base int) int {
	switch mode {
	case "economy":
		base = maxInt(1, base/2)
	case "turbo":
		base = minInt(16, base*2)
	}
	if base < 1 {
		base = 1
	}
	if base > 16 {
		base = 16
	}
	return base
}

// recommendInitialShards §31：expected_pages → Initial Shard Recommendation。
func recommendInitialShards(pages int64) int {
	switch {
	case pages <= 1:
		return 1
	case pages <= 5:
		return 2
	case pages <= 20:
		return 2
	case pages <= 80:
		return 4
	default:
		return 8
	}
}

// ankrZeroVerify 1 个 Ankr 请求确认 Range 为空（§26）。
func (p *Planner) ankrZeroVerify(ctx context.Context, job *model.DownloadJob, subj model.Subject, ds string, from, to uint64) (bool, error) {
	if p.prov == nil {
		return false, fmt.Errorf("zero verify unavailable: provider not configured")
	}
	req := provider.FetchRequest{Chain: job.Chain, Dataset: ds, Address: subj.Subject, PageSize: 1000}
	if to > 0 {
		f, t := from, to
		req.FromBlock = &f
		req.ToBlock = &t
	}
	vctx, cancel := context.WithTimeout(context.WithValue(ctx, "job_id", job.ID), 10*time.Second)
	defer cancel()
	res, err := p.prov.Fetch(vctx, req)
	if err != nil {
		p.log.Warn("zero verify request failed; falling through to normal planner", "dataset", ds, "err", err)
		return false, err
	}
	return len(res.Rows) == 0, nil
}

// commitEstimate 落 Decision Log（§88）+ 合并 dataset stats（Planner Card，§79）+ Shadow Diff（V1.3 §98）。
func (p *Planner) commitEstimate(ctx context.Context, job *model.DownloadJob, subj model.Subject, item model.DatasetItem, ds string, est *v12Estimate) {
	p.maybeShadowDiff(ctx, job, subj, ds, est)
	decision := &model.PlanningDecision{
		JobID: job.ID, SubjectID: subj.ID, Dataset: ds,
		CountSource:     est.Source,
		PredictedRows:   est.PredictedRows,
		PredictedPages:  est.PredictedPages,
		ProfileHit:      est.ProfileHit,
		PreferredShards: est.Preferred,
		InitialShards:   est.Initial,
		DecisionReason:  est.Reason,
	}
	if st := p.schedulerStateSnapshot(ctx); st != nil {
		decision.ProviderSafeSlots = st.ConcurrencyTarget
		decision.GlobalActiveSlots = st.ActiveSlots
	}
	if err := p.store.InsertPlanningDecision(ctx, decision); err != nil {
		p.log.Warn("planning decision log failed", "err", err)
	}
	// dataset.stats 合并（不覆盖 gaps 等既有字段）
	stats := map[string]any{
		"estimate": map[string]any{
			"source": est.Source, "predicted_rows": est.PredictedRows, "predicted_pages": est.PredictedPages,
			"profile_hit": est.ProfileHit, "preferred_shards": est.Preferred, "initial_shards": est.Initial,
			"decision_reason": est.Reason, "ts": time.Now(),
		},
	}
	if est.RawCount >= 0 {
		stats["raw_count"] = est.RawCount
	}
	b, _ := json.Marshal(stats)
	if _, err := p.store.Pool.Exec(ctx,
		"UPDATE download_datasets SET stats = COALESCE(stats,'{}'::jsonb) || $2::jsonb WHERE id=$1", item.ID, b); err != nil {
		p.log.Warn("dataset stats merge failed", "err", err)
	}
}

// maybeShadowDiff V1.3 §97/§98：Port Shadow 模式下记录 old(V1.1 现状) vs new(Count 建议)
// 的 shard 决策差异，供 10%→100% 放量 Gate 评估（连续稳定样本）。Service 内部会按
// COUNT_PROVIDER_PORT_SHADOW 开关决定是否真正落库。
func (p *Planner) maybeShadowDiff(ctx context.Context, job *model.DownloadJob, subj model.Subject, ds string, est *v12Estimate) {
	if p.oracle == nil || !p.oracle.Shadow() || est.RawCount < 0 || est.ProfileHit {
		return // 仅 Shadow + 非 Profile 路径记录（Profile HIT 不需要 Count，无 diff 意义）
	}
	if job.HeadBlock <= 0 && (job.FromBlock == nil || job.ToBlock == nil) {
		return
	}
	newInitial := est.V11Initial
	if p.oracle.Aware() {
		return // aware 阶段已用 Count 决策，不需要 diff
	}
	if est.RawCount > 0 {
		pageSize := int64(p.preferredPageSize(ctx, job.Chain, ds))
		pages := (est.RawCount + pageSize - 1) / pageSize
		rec := recommendInitialShards(pages)
		if !p.multiRangeEligibleV12(job.Chain, ds) {
			rec = 1
		}
		mode := job.Mode
		if mode == "" {
			mode = "balanced"
		}
		newInitial = p.modeShards(mode, rec)
	}
	diffReason := "same_initial"
	if newInitial > est.V11Initial {
		diffReason = "higher_initial"
	} else if newInitial < est.V11Initial {
		diffReason = "lower_initial"
	}
	from, to := int64(0), int64(0)
	if job.RangeType == "block" && job.FromBlock != nil && job.ToBlock != nil {
		from, to = *job.FromBlock, *job.ToBlock
	} else if job.HeadBlock > 0 {
		to = job.HeadBlock
	}
	diff := &model.ShadowDiff{
		RequestHash: shortJobKey(job.ID, subj.Subject, ds),
		ChainID:     int64(p.oracleChainID(ctx, job.Chain)),
		Dataset:     ds,
		SubjectHash: subjectHash(subj.Subject),
		RangeStart:  from, RangeEnd: to,
		Status:             "SHADOW",
		Reason:             est.Reason,
		ProviderID:         est.Source,
		Count:              est.RawCount,
		OldSource:          "v11_planner",
		NewSource:          est.Source,
		OldCount:           -1,
		NewCount:           est.RawCount,
		OldInitial:         est.V11Initial,
		NewInitial:         newInitial,
		DecisionDiffReason: diffReason,
	}
	p.oracle.RecordShadowDiff(ctx, diff)
}

// oracleChainID 通过 store 能力矩阵取 chain_id（与 countoracle deployment 一致）。
func (p *Planner) oracleChainID(ctx context.Context, chain string) int64 {
	if p.oracle == nil {
		return 0
	}
	// Oracle 不暴露 ChainID 给 planner；这里从 store 能力矩阵读，避免重复查询
	cap, err := p.store.GetChainCapability(ctx, chain)
	if err != nil || cap == nil {
		return 0
	}
	if cap.EVMChainID == nil {
		return 0
	}
	return *cap.EVMChainID
}

// shortJobKey 稳定短 key（job+subject+dataset，用于 shadow diff request_hash）。
func shortJobKey(jobID, subject, ds string) string {
	return subjectHash(subject) + "|" + ds + "|" + jobID
}

// rangeShardsFor 最终初始 Shard 数（Profile HIT 优先；MISS 且 aware 用 Count 建议）。
func (p *Planner) rangeShardsFor(ctx context.Context, job *model.DownloadJob, subj model.Subject, item model.DatasetItem, ds string) int {
	base := p.shardCountFor(ctx, job, ds)
	if !p.v12Active(ctx, job, ds) {
		return base
	}
	est := p.estimateFor(ctx, job, subj, item, ds)
	if est == nil {
		return base
	}
	if est.ZeroEmpty {
		return 1 // 调用方在 estimateFor 内已提前 return（AddCoverage+COMPLETED）；此处只是兜底
	}
	if p.oracle.Aware() && !est.ProfileHit && est.Initial > 0 {
		return est.Initial
	}
	return base
}

// ---- Multi-address LPT（§46-§48）：同一 Job 多个地址按 Work Score 排序规划 ----

type workScore struct {
	subj  model.Subject
	score int64
}

// subjectWorkScores 预估每个地址的工作量（tx + token 期望页 × provider 延迟，§47）。
func (p *Planner) subjectWorkScores(ctx context.Context, job *model.DownloadJob, subjects []model.Subject) []workScore {
	out := make([]workScore, 0, len(subjects))
	for _, subj := range subjects {
		if !p.budgetOpen() {
			break // 预算耗尽：停止预取 Count，未打分地址保持原序（低分尾段）
		}
		var score int64
		for _, ds := range job.Datasets {
			if !p.budgetOpen() {
				break
			}
			if !p.v12Active(ctx, job, ds) {
				continue
			}
			latency := int64(3000)
			if prof, err := p.store.GetPageProfile(ctx, job.Chain, ds, ds); err == nil && prof != nil && prof.AvgLatencyMs > 0 {
				latency = prof.AvgLatencyMs
			}
			req := model.CountRequest{Chain: job.Chain, Dataset: ds, Address: subj.Subject, TargetBlock: uint64(job.HeadBlock)}
			res := p.oracle.Count(context.WithValue(ctx, "job_id", job.ID), req)
			if res.RawCount < 0 {
				continue
			}
			predicted := res.AdjustedCount
			if predicted <= 0 {
				predicted = res.RawCount
			}
			pageSize := int64(p.preferredPageSize(ctx, job.Chain, ds))
			if pageSize <= 0 {
				pageSize = 10000
			}
			pages := (predicted + pageSize - 1) / pageSize
			score += pages * latency
		}
		out = append(out, workScore{subj: subj, score: score})
	}
	// LPT：重的地址先规划（先入 Ready Queue，§46/§48：FIFO 会在尾部堆满热地址）
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

// recordEstimateActuals §81/§24：dataset 全部完成后回写 actual_rows / deviation，
// 并把 raw_count vs actual 样本交给 Count Calibration（§22/§24；只用于 Planning）。
func (sc *Scheduler) recordEstimateActuals(ctx context.Context, sh model.Shard) {
	if sc.countOracle == nil || !sc.countOracle.Enabled() {
		return
	}
	item, err := sc.store.GetDataset(ctx, sh.DatasetID)
	if err != nil {
		return
	}
	if item.Status != model.ItemCompleted {
		return // 该 dataset 尚未收敛（还有 shard 在跑）
	}
	// once 标记必须在确认 COMPLETED 之后（否则最后一块完成时会漏写 actuals）
	if _, done := sc.actualsRecorded.LoadOrStore(sh.DatasetID, true); done {
		return
	}
	rawCount := int64(-1)
	predicted := int64(-1)
	if v, ok := item.Stats["raw_count"].(float64); ok {
		rawCount = int64(v)
	}
	if v, ok := item.Stats["predicted_rows"].(float64); ok {
		predicted = int64(v)
	} else if est, ok := item.Stats["estimate"].(map[string]any); ok {
		if v, ok2 := est["predicted_rows"].(float64); ok2 {
			predicted = int64(v)
		}
	}
	if rawCount < 0 && predicted < 0 {
		return // 无 Count 记录（纯 V1.1 路径）
	}
	var actual int64
	_ = sc.store.Pool.QueryRow(ctx,
		"SELECT COALESCE(SUM(p.rows_inserted),0) FROM download_pages p "+
			"JOIN download_shards s ON s.id = p.shard_id WHERE s.dataset_id=$1 AND p.status IN ('INGESTED','VALIDATED')",
		sh.DatasetID).Scan(&actual)
	job, err := sc.store.GetJob(ctx, sh.JobID)
	if err != nil {
		return
	}
	subs, err := sc.store.SubjectsByJob(ctx, sh.JobID)
	if err != nil {
		return
	}
	var subject string
	for _, s := range subs {
		if s.ID == sh.SubjectID {
			subject = s.Subject
			break
		}
	}
	if subject == "" {
		return
	}
	// stats 回写（Planner Card：Predicted vs Actual，§81）
	stats := map[string]any{"actual_rows": actual, "actual_recorded_at": time.Now()}
	if predicted > 0 {
		dev := (float64(actual) - float64(predicted)) / float64(predicted) * 100
		stats["deviation_pct"] = dev
	}
	b, _ := json.Marshal(map[string]any{"actual": stats})
	if _, err := sc.store.Pool.Exec(ctx,
		"UPDATE download_datasets SET stats = COALESCE(stats,'{}'::jsonb) || $2::jsonb WHERE id=$1", sh.DatasetID, b); err != nil {
		sc.log.Warn("actual rows stats update failed", "err", err)
	}
	// 校准样本（raw Count vs Ankr/CH 真值；provider 维度 §51）
	if rawCount >= 0 {
		provider := "chainbase"
		if est, ok := item.Stats["estimate"].(map[string]any); ok {
			if src, ok2 := est["source"].(string); ok2 && src != "" {
				provider = src
			}
		}
		sc.countOracle.RecordActual(provider, job.Chain, sh.Dataset, subjectHash(subject), rawCount, actual)
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "..."
}
