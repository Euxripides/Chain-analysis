package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
)

var downloadMethodByDataset = map[string]string{
	"transactions":    "ankr_getTransactionsByAddress",
	"token_transfers": "ankr_getTokenTransfers",
	"nft_transfers":   "ankr_getNftTransfers",
	"logs":            "ankr_getLogs",
	"balances":        "ankr_getAccountBalance",
}

type manifestMeta struct {
	RequestHash   string         `json:"request_hash"`
	Provider      string         `json:"provider"`
	Method        string         `json:"method"`
	Chain         string         `json:"chain"`
	Address       string         `json:"address"`
	Dataset       string         `json:"dataset"`
	FromBlock     int64          `json:"from_block"`
	ToBlock       int64          `json:"to_block"`
	FromTimestamp *time.Time     `json:"from_timestamp,omitempty"`
	ToTimestamp   *time.Time     `json:"to_timestamp,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    time.Time      `json:"finished_at"`
	Parts         []manifestPart `json:"parts"`
}

type manifestPart struct {
	Key       string `json:"key"`
	SHA256    string `json:"sha256"`
	Rows      int    `json:"rows"`
	BytesGz   int64  `json:"bytes_gz"`
	PageToken string `json:"page_token"`
}

func requestHash(sh model.Shard, chain, address, method string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%d|%d", "ankr", method, chain, address, sh.Dataset, sh.FromBlock, sh.ToBlock)
	if sh.FromTimestamp != nil {
		fmt.Fprintf(h, "|%d", sh.FromTimestamp.UnixMilli())
	}
	if sh.ToTimestamp != nil {
		fmt.Fprintf(h, "|%d", sh.ToTimestamp.UnixMilli())
	}
	return hex.EncodeToString(h.Sum(nil))
}

// runShard 执行一个 Shard：Ankr 串行翻页 → Raw 落盘 → 标准化 → ClickHouse → Checkpoint → Coverage。
func (sc *Scheduler) runShard(ctx context.Context, sh model.Shard) {
	defer sc.wg.Done()
	atomic.AddInt64(&sc.active, 1)
	defer atomic.AddInt64(&sc.active, -1)

	// 关键日志与用量记录需要 job_id / shard_id / trace 上下文
	ctx = context.WithValue(ctx, "job_id", sh.JobID)
	ctx = context.WithValue(ctx, "shard_id", sh.ID)

	log := sc.log.With("job_id", sh.JobID, "shard_id", sh.ID, "dataset", sh.Dataset)
	started := time.Now()

	address := ""
	subjects, _ := sc.store.SubjectsByJob(ctx, sh.JobID)
	for _, s := range subjects {
		if s.ID == sh.SubjectID {
			address = s.Subject
		}
	}
	job, err := sc.store.GetJob(ctx, sh.JobID)
	if err != nil {
		log.Error("get job failed", "err", err)
		return
	}
	chain := job.Chain

	method := downloadMethodByDataset[sh.Dataset]
	reqHash := requestHash(sh, chain, address, method)
	manifest := manifestMeta{
		RequestHash: reqHash, Provider: "ankr", Method: method, Chain: chain, Address: address,
		Dataset: sh.Dataset, FromBlock: sh.FromBlock, ToBlock: sh.ToBlock,
		FromTimestamp: sh.FromTimestamp, ToTimestamp: sh.ToTimestamp, StartedAt: started, Parts: []manifestPart{},
	}

	pageToken := sh.PageToken
	pageNo := sh.PageNo
	rowsTotal := sh.DownloadedRows
	bytesTotal := sh.DownloadedBytes
	var minBlock, maxBlock int64

	// V1.1：PagePolicy（§16-§18）——Resume 继承上次自适应状态
	pkey := sc.pagePolicy.Key(chain, sh.Dataset, "address", address)
	if sh.PageSize > 0 {
		if st := pageStateFromMap(sh.PageState); st != nil {
			st.CurrentPageSize = sh.PageSize
			sc.pagePolicy.Restore(pkey, st)
		}
	}
	currentSize := sh.PageSize
	if currentSize <= 0 {
		currentSize = sc.pagePolicy.Current(pkey, sh.Dataset)
	}

	for {
		action, cerr := sc.jobControl(ctx, sh)
		if cerr != nil {
			log.Error("job control failed", "err", cerr)
		}
		if action != "continue" {
			log.Info("shard stopped by job control", "action", action)
			return
		}

		req := provider.FetchRequest{
			Chain: chain, Dataset: sh.Dataset, Address: address,
			PageSize: currentSize, PageToken: pageToken,
		}
		// 实测 Ankr 行为差异：
		// - transactions / token_transfers / nft_transfers：带 toBlock 返回空、
		//   fromBlock 被忽略；只能从最新往前翻页，边界由客户端过滤 + 降序停止实现。
		// - logs：尊重 from/toBlock。
		switch sh.Dataset {
		case "transactions", "token_transfers", "nft_transfers":
			// 不传块边界（时间边界也由客户端过滤）
		case "balances":
			req.FromBlock, req.ToBlock, req.FromTime, req.ToTime = nil, nil, nil, nil
		default:
			if !(sh.FromBlock == 0 && sh.ToBlock == 0 && sh.FromTimestamp == nil) {
				f := uint64(sh.FromBlock)
				t := uint64(sh.ToBlock)
				req.FromBlock = &f
				req.ToBlock = &t
			}
			if sh.FromTimestamp != nil {
				req.FromTime = sh.FromTimestamp
			}
			if sh.ToTimestamp != nil {
				req.ToTime = sh.ToTimestamp
			}
		}

		pageStart := time.Now()
		res, dec, err := sc.fetchWithRetry(ctx, pkey, sh.Dataset, req)
		if dec != nil && dec.SplitRange {
			// §20/§43：最小 pageSize 仍反复失败 → 拆 Range；原 Shard 不能标 COMPLETE
			sc.log.Warn("split range on repeated min-page failures", "shard_id", sh.ID, "dataset", sh.Dataset, "range", fmt.Sprintf("%d-%d", sh.FromBlock, sh.ToBlock))
			sc.splitShardRange(ctx, sh)
			return
		}
		if err != nil {
			retryable := false
			errMsg := err.Error()
			if pe, ok := err.(*provider.Error); ok {
				if pe.Retryable {
					retryable = true
				}
				if pe.Class != "" {
					errMsg = "[" + string(pe.Class) + "] " + errMsg
				}
			}
			newStatus, ferr := sc.store.FailShard(ctx, sh.ID, errMsg, retryable)
			if ferr != nil {
				log.Error("fail shard failed", "err", ferr)
			}
			log.Error("shard fetch failed", "err", err, "new_status", newStatus)
			sc.publish("events.shards."+sh.JobID, map[string]any{
				"type": "shard_status", "shard_id": sh.ID, "status": newStatus, "error": err.Error(), "ts": time.Now(),
			})
			return
		}
		currentSize = req.PageSize
		// 成功页反馈（§14）：连续快成功才升档（一次一级，降档后有 cooldown）
		var bodyEst int64
		for _, r := range res.Rows {
			bodyEst += int64(len(r))
		}
		nextSize := sc.pagePolicy.OnSuccess(pkey, sh.Dataset, int64(currentSize), bodyEst, time.Since(pageStart))
		if nextSize != currentSize {
			currentSize = nextSize
			log.Debug("page size upgraded", "page_size", nextSize, "dataset", sh.Dataset)
		}

		// 客户端侧范围过滤（transactions/nft 不信任 Provider 边界）
		filtered, passedLower := sc.filterRows(sh, res.Rows)
		res.Rows = filtered

		if len(res.Rows) > 0 {
			now := time.Now()
			partKey := fmt.Sprintf("raw/ankr/%s/%s/%s/%s/%s/part-%06d.json.gz",
				chain, sh.Dataset, now.Format("2006"), now.Format("01"), reqHash, pageNo+1)
			rawPart, _ := json.Marshal(map[string]any{"rows": res.Rows})
			sum := sha256.Sum256(rawPart)
			rstart := time.Now()
			n, rerr := sc.raw.PutGzip(ctx, partKey, rawPart)
			observability.RawWriteLatencyMs.WithLabelValues(sc.cfg.RawBucket).Observe(float64(time.Since(rstart).Milliseconds()))
			if rerr != nil {
				_, _ = sc.store.FailShard(ctx, sh.ID, "raw persist: "+rerr.Error(), true)
				return
			}
			bytesTotal += n
			manifest.Parts = append(manifest.Parts, manifestPart{
				Key: partKey, SHA256: hex.EncodeToString(sum[:]), Rows: len(res.Rows), BytesGz: n, PageToken: pageToken,
			})

			minB, maxB, ierr := sc.ingestRows(ctx, sh, res.Rows, chain, address)
			if ierr != nil {
				_, _ = sc.store.FailShard(ctx, sh.ID, "ingest: "+ierr.Error(), true)
				return
			}
			if minBlock == 0 || (minB > 0 && minB < minBlock) {
				minBlock = minB
			}
			if maxB > maxBlock {
				maxBlock = maxB
			}
			rowsTotal += int64(len(res.Rows))
		}

		pageNo++
		// Checkpoint：持久化与入库之后才推进（崩溃后可安全恢复；§18 保存 PagePolicy 状态）
		if err := sc.store.CheckpointShard(ctx, sh.ID, res.NextPageToken, pageNo, currentSize, rowsTotal, bytesTotal, pageStateToMap(sc.pagePolicy.Snapshot(pkey))); err != nil {
			log.Error("checkpoint failed", "err", err)
		}

		if !res.HasMore {
			break
		}
		if passedLower {
			// 降序遍历已越过下边界：剩余页全部更旧，停止
			break
		}
		pageToken = res.NextPageToken
	}

	manifest.FinishedAt = time.Now()
	manifestKey := fmt.Sprintf("raw/ankr/%s/%s/%s/%s/%s/manifest.json.gz",
		chain, sh.Dataset, manifest.StartedAt.Format("2006"), manifest.StartedAt.Format("01"), reqHash)
	mb, _ := json.Marshal(manifest)
	if _, err := sc.raw.PutGzip(ctx, manifestKey, mb); err != nil {
		log.Warn("manifest write failed", "err", err)
	}

	if err := sc.store.CompleteShard(ctx, sh.ID, manifestKey, rowsTotal, bytesTotal); err != nil {
		log.Error("complete shard failed", "err", err)
		return
	}
	sc.commitCoverage(ctx, sh, chain, address, minBlock, maxBlock, rowsTotal, bytesTotal)
	if err := sc.planner.ContinueShards(ctx, sh); err != nil {
		log.Error("continue shards failed", "err", err)
	}
	sc.recompute(ctx, sh)
	sc.publish("events.shards."+sh.JobID, map[string]any{
		"type": "shard_status", "shard_id": sh.ID, "status": model.ShardCompleted,
		"rows": rowsTotal, "bytes": bytesTotal, "ts": time.Now(),
	})
	log.Info("shard completed", "rows", rowsTotal, "bytes", bytesTotal, "duration", time.Since(started))
}

// jobControl 在每页之间检查 Job 状态（暂停/取消/上下文退出）。
func (sc *Scheduler) jobControl(ctx context.Context, sh model.Shard) (string, error) {
	job, err := sc.store.GetJob(ctx, sh.JobID)
	if err != nil {
		return "continue", err
	}
	switch job.Status {
	case model.JobPauseReq:
		if err := sc.store.PauseShard(ctx, sh.ID); err == nil {
			sc.publish("events.shards."+sh.JobID, map[string]any{
				"type": "shard_status", "shard_id": sh.ID, "status": model.ShardPaused, "ts": time.Now(),
			})
		}
		return "paused", nil
	case model.JobCancelReq, model.JobCancelled:
		if err := sc.store.CancelShard(ctx, sh.ID); err == nil {
			sc.publish("events.shards."+sh.JobID, map[string]any{
				"type": "shard_status", "shard_id": sh.ID, "status": model.ShardCancelled, "ts": time.Now(),
			})
		}
		return "cancelled", nil
	}
	select {
	case <-ctx.Done():
		// 进程退出：暂停当前 Shard，恢复时继续
		_ = sc.store.PauseShard(context.Background(), sh.ID)
		return "ctx-done", nil
	default:
	}
	return "continue", nil
}

// fetchWithRetry V1.1（§21/§27/§28）：
//   - RateLimit / Upstream：指数退避 + 全抖动（429 优先 Retry-After，§26）
//   - Timeout / ResponseTooLarge：PagePolicy 降一档 + 短抖动，重试同一逻辑页
//   - 最小 pageSize 连续失败：返回 SplitRange 决策（由调用方拆 Range）
//   - context.Canceled 禁止重试（§10）
func (sc *Scheduler) fetchWithRetry(ctx context.Context, pkey, dataset string, req provider.FetchRequest) (provider.FetchResult, *ankr.PageDecision, error) {
	var lastErr error
	var lastDec *ankr.PageDecision
	for attempt := 0; attempt < 8; attempt++ {
		actx := context.WithValue(ctx, "retry_attempt", attempt)
		actx, cancel := context.WithTimeout(actx, sc.cfg.Ankr.RequestTimeout)
		res, err := sc.prov.Fetch(actx, req)
		cancel()
		if err == nil {
			return res, nil, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return provider.FetchResult{}, nil, ctx.Err()
		}
		pe, ok := err.(*provider.Error)
		if !ok || !pe.Retryable {
			return provider.FetchResult{}, nil, err
		}

		dec := sc.pagePolicy.OnError(pkey, dataset, pe, req.PageSize)
		lastDec = &dec
		if dec.SplitRange {
			return provider.FetchResult{}, &dec, nil
		}
		if dec.NextPageSize != req.PageSize {
			// 降一档重试同一逻辑页（不推进 pageToken，§37）
			req.PageSize = dec.NextPageSize
			sc.log.Warn("page size reduced on retryable error",
				"error_class", string(pe.Class), "dataset", dataset,
				"page_size", req.PageSize, "err", pe.Message)
			observability.AnkrPageSizeGauge.WithLabelValues(pkey).Set(float64(req.PageSize))
		}
		if dec.ReduceRate && dec.RetryAfter > 0 {
			// §26：429/-32090 优先遵守 Retry-After
			dec.Backoff = dec.RetryAfter
		}
		backoff := dec.Backoff
		if backoff <= 0 {
			backoff = time.Duration(1<<uint(minInt(attempt, 5)))*time.Second + time.Duration(rand.Int63n(1000))*time.Millisecond
		}
		select {
		case <-ctx.Done():
			return provider.FetchResult{}, nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
	return provider.FetchResult{}, lastDec, lastErr
}

// filterRows 客户端侧范围过滤：块区间 + 时间区间；passedLower 表示本页已越过下边界。
func (sc *Scheduler) filterRows(sh model.Shard, rows []json.RawMessage) ([]json.RawMessage, bool) {
	passedLower := false
	out := rows[:0]
	for _, raw := range rows {
		if bn, ok := ingest.RawBlockNumber(raw); ok && bn > 0 {
			if sh.FromBlock > 0 && int64(bn) < sh.FromBlock {
				passedLower = true
				continue
			}
			if sh.ToBlock > 0 && int64(bn) > sh.ToBlock {
				continue
			}
		}
		if ts, ok := ingest.RawTimestamp(raw); ok {
			if sh.FromTimestamp != nil && ts.Before(*sh.FromTimestamp) {
				passedLower = true
				continue
			}
			if sh.ToTimestamp != nil && ts.After(*sh.ToTimestamp) {
				continue
			}
		}
		out = append(out, raw)
	}
	return out, passedLower
}

// ingestRows 标准化并批量写入 ClickHouse（返回观察到的最小/最大块）。
func (sc *Scheduler) ingestRows(ctx context.Context, sh model.Shard, rows []json.RawMessage, chain, address string) (int64, int64, error) {
	var minB, maxB int64
	switch sh.Dataset {
	case "transactions":
		batch := make([]ingest.TxRow, 0, len(rows))
		acts := make([]ingest.ActivityRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeTransaction(raw, chain, "ankr")
			if err != nil {
				return 0, 0, err
			}
			trackBlocks(&minB, &maxB, int64(r.BlockNumber))
			batch = append(batch, r)
			dir, cp := directionOf(address, r.From, r.To)
			acts = append(acts, ingest.ActivityRow{
				Chain: chain, Address: address, Counterparty: cp, Direction: dir,
				ActivityType: "transaction", Timestamp: r.Timestamp, BlockNumber: r.BlockNumber,
				TxHash: r.TxHash, Dataset: "transactions", Provider: "ankr",
			})
		}
		if err := sc.writer.WriteTransactions(ctx, batch); err != nil {
			return 0, 0, err
		}
		if len(acts) > 0 {
			if err := sc.writer.WriteActivity(ctx, acts); err != nil {
				return 0, 0, err
			}
		}
		return minB, maxB, nil
	case "token_transfers":
		batch := make([]ingest.TokenTransferRow, 0, len(rows))
		acts := make([]ingest.ActivityRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeTokenTransfer(raw, chain, "ankr")
			if err != nil {
				return 0, 0, err
			}
			trackBlocks(&minB, &maxB, int64(r.BlockNumber))
			batch = append(batch, r)
			dir, cp := directionOf(address, r.From, r.To)
			acts = append(acts, ingest.ActivityRow{
				Chain: chain, Address: address, Counterparty: cp, Direction: dir,
				ActivityType: "transfer", Timestamp: r.Timestamp, BlockNumber: r.BlockNumber,
				TxHash: r.TxHash, TokenAddress: r.TokenAddress, Amount: r.Amount,
				Dataset: "token_transfers", Provider: "ankr",
			})
		}
		if err := sc.writer.WriteTokenTransfers(ctx, batch); err != nil {
			return 0, 0, err
		}
		if len(acts) > 0 {
			if err := sc.writer.WriteActivity(ctx, acts); err != nil {
				return 0, 0, err
			}
		}
		return minB, maxB, nil
	case "nft_transfers":
		batch := make([]ingest.NftTransferRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeNftTransfer(raw, chain, "ankr")
			if err != nil {
				return 0, 0, err
			}
			trackBlocks(&minB, &maxB, int64(r.BlockNumber))
			batch = append(batch, r)
		}
		return minB, maxB, sc.writer.WriteNftTransfers(ctx, batch)
	case "logs":
		batch := make([]ingest.LogRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeLog(raw, chain, "ankr")
			if err != nil {
				return 0, 0, err
			}
			trackBlocks(&minB, &maxB, int64(r.BlockNumber))
			batch = append(batch, r)
		}
		return minB, maxB, sc.writer.WriteLogs(ctx, batch)
	case "balances":
		batch := make([]ingest.BalanceRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeBalance(raw, chain, address, uint64(sh.ToBlock), "ankr")
			if err != nil {
				return 0, 0, err
			}
			batch = append(batch, r)
		}
		return 0, 0, sc.writer.WriteBalances(ctx, batch)
	}
	return minB, maxB, nil
}

// directionOf 计算活动方向与对手方。
func directionOf(subject, from, to string) (direction, counterparty string) {
	switch {
	case from == subject && to == subject:
		return "self", subject
	case from == subject:
		return "out", to
	case to == subject:
		return "in", from
	default:
		return "in", from
	}
}

// pageStateFromMap 从 shard.page_state(JSONB) 恢复策略状态。
func pageStateFromMap(m map[string]any) *ankr.PagePolicyState {
	if m == nil {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	var st ankr.PagePolicyState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil
	}
	return &st
}

// pageStateToMap 序列化策略状态进 Checkpoint。
func pageStateToMap(st *ankr.PagePolicyState) map[string]any {
	if st == nil {
		return map[string]any{}
	}
	b, _ := json.Marshal(st)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}

// splitShardRange §20/§43：最小 pageSize 仍反复失败 → 拆成两半；原 Shard 不标 COMPLETE。
func (sc *Scheduler) splitShardRange(ctx context.Context, sh model.Shard) {
	if sh.ToBlock-sh.FromBlock < 2 {
		// 无法再拆（含 live/full 0..0）：标记可重试，等下次调度
		_, _ = sc.store.FailShard(ctx, sh.ID, "range unsplittable, retry later", true)
		return
	}
	mid := sh.FromBlock + (sh.ToBlock-sh.FromBlock)/2
	base := model.Shard{
		JobID: sh.JobID, SubjectID: sh.SubjectID, DatasetID: sh.DatasetID, Dataset: sh.Dataset,
		FromTimestamp: sh.FromTimestamp, ToTimestamp: sh.ToTimestamp,
		Status: model.ShardPending, Priority: sh.Priority, MaxRetries: sh.MaxRetries,
	}
	first, second := base, base
	first.FromBlock, first.ToBlock = sh.FromBlock, mid
	second.FromBlock, second.ToBlock = mid+1, sh.ToBlock
	if err := sc.store.CreateShards(ctx, []model.Shard{first, second}); err != nil {
		sc.log.Error("split shard failed", "shard_id", sh.ID, "err", err)
		_, _ = sc.store.FailShard(ctx, sh.ID, "split failed: "+err.Error(), true)
		return
	}
	// 已完成页保留在 ClickHouse（幂等去重）；本 shard 不标 COMPLETE、不提交 Coverage
	_ = sc.store.CancelShard(ctx, sh.ID)
	sc.publish("events.shards."+sh.JobID, map[string]any{
		"type": "shard_status", "shard_id": sh.ID, "status": model.ShardCancelled,
		"reason": "split_range", "ts": time.Now(),
	})
	sc.log.Warn("shard split into two ranges",
		"shard_id", sh.ID, "first", fmt.Sprintf("%d-%d", first.FromBlock, first.ToBlock),
		"second", fmt.Sprintf("%d-%d", second.FromBlock, second.ToBlock))
}

func trackBlocks(minB, maxB *int64, b int64) {
	if b <= 0 {
		return
	}
	if *minB == 0 || b < *minB {
		*minB = b
	}
	if b > *maxB {
		*maxB = b
	}
}

// commitCoverage 数据全部持久化 + 校验之后才提交 Coverage（不允许提前标记 COMPLETE）。
func (sc *Scheduler) commitCoverage(ctx context.Context, sh model.Shard, chain, address string, minBlock, maxBlock, rows, bytes int64) {
	if rows == 0 {
		// 未下载到任何行：不能证明该区间已覆盖，不提交（防止 Provider 边界异常导致误标）
		sc.log.Info("coverage skipped (zero rows)", "shard_id", sh.ID, "dataset", sh.Dataset)
		return
	}
	if sh.Dataset == "balances" {
		if err := sc.store.AddCoverage(ctx, chain, address, "balances", "ankr", 0, sh.ToBlock, rows, bytes, sh.ToBlock); err != nil {
			sc.log.Error("coverage commit failed", "shard_id", sh.ID, "err", err)
		}
		return
	}
	from, to := sh.FromBlock, sh.ToBlock
	if from == 0 && to == 0 {
		// live/时间范围：只提交观察到的内部块（边界块保守保留，重复下载幂等去重）
		if maxBlock-minBlock >= 2 {
			from, to = minBlock+1, maxBlock-1
		} else {
			sc.log.Info("coverage skipped (boundary only)", "shard_id", sh.ID, "min", minBlock, "max", maxBlock)
			return
		}
	}
	if err := sc.store.AddCoverage(ctx, chain, address, sh.Dataset, "ankr", from, to, rows, bytes, sh.ToBlock); err != nil {
		sc.log.Error("coverage commit failed", "shard_id", sh.ID, "err", err)
	}
}

func (sc *Scheduler) recompute(ctx context.Context, sh model.Shard) {
	if err := sc.store.RecomputeDataset(ctx, sh.DatasetID); err != nil {
		sc.log.Error("recompute dataset failed", "err", err)
	}
	if err := sc.store.RecomputeSubject(ctx, sh.SubjectID); err != nil {
		sc.log.Error("recompute subject failed", "err", err)
	}
	// V1.2：同步 workspace_addresses 统计与状态（§46）
	if sc.meta != nil {
		job, jerr := sc.store.GetJob(ctx, sh.JobID)
		if jerr == nil {
			if subjects, err := sc.store.SubjectsByJob(ctx, sh.JobID); err == nil {
				for _, subj := range subjects {
					if subj.ID == sh.SubjectID {
						status := "current"
						if sh.Status == model.ShardFailedFinal {
							status = "failed"
						}
						_ = sc.meta.SyncFromCoverage(ctx, job.Chain, subj.Subject, status)
					}
				}
			}
		}
	}
	progress, err := sc.store.RecomputeJob(ctx, sh.JobID)
	if err != nil {
		sc.log.Error("recompute job failed", "err", err)
	}
	if job, err := sc.store.GetJob(ctx, sh.JobID); err == nil {
		sc.publish("events.jobs."+sh.JobID, map[string]any{
			"type": "job_status", "job_id": sh.JobID, "status": job.Status, "progress": progress, "ts": time.Now(),
		})
	}
}
