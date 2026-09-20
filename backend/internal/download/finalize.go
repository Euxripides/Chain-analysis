// Coverage Finalizer（文档 §62/§63）：
// 只有 All pages ingested + Validation 通过 + Coverage Commit 才能 COMPLETE。
// 严禁在真正入库和验证前写 COMPLETE（§7/§63/约束 40）。
package download

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// finalizerLoop 收敛 FETCHED / INGESTED 的 Shard（§62：INGESTED ≠ COMPLETE）。
func (sc *Scheduler) finalizerLoop(ctx context.Context) {
	// Finalization is independent per shard. Cold TRON plans deliberately use
	// many small shards to keep fetch concurrency high, so a one-second,
	// single-threaded finalizer turned the last 15-20 completed shards into a
	// multi-second artificial tail. Poll faster and finalize with a bounded pool.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, status := range []string{model.ShardFetched, model.ShardIngested} {
				shards, err := sc.store.ListShardsByStatus(ctx, status, 64)
				if err != nil {
					sc.log.Error("finalizer list failed", "status", status, "err", err)
					continue
				}
				sc.finalizeShardBatch(ctx, shards)
			}
		}
	}
}

func (sc *Scheduler) finalizeShardBatch(ctx context.Context, shards []model.Shard) {
	if len(shards) == 0 {
		return
	}
	workers := minInt(8, len(shards))
	jobs := make(chan model.Shard, len(shards))
	completed := make(chan model.Shard, len(shards))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sh := range jobs {
				if sc.finalizeShard(ctx, sh) {
					completed <- sh
				}
			}
		}()
	}
	for _, sh := range shards {
		jobs <- sh
	}
	close(jobs)
	wg.Wait()
	close(completed)
	finalized := make([]model.Shard, 0, len(shards))
	for sh := range completed {
		finalized = append(finalized, sh)
	}
	sc.recomputeFinalizedBatch(ctx, finalized)
}

func (sc *Scheduler) finalizeShard(ctx context.Context, sh model.Shard) bool {
	total, ingested, _, err := sc.store.PageCountsByShard(ctx, sh.ID)
	if err != nil {
		sc.log.Error("page counts failed", "shard_id", sh.ID, "err", err)
		return false
	}
	if total == 0 || ingested < total {
		// 不可恢复页判定：存在 FAILED_FINAL 页且没有任何仍可重放的页 =>
		// 这个 Shard 永远不会再进展。继续等待只会让任务永久卡住，因此把它
		// 收敛为 FAILED_FINAL（非重试），由 Dataset 层记录失败原因。
		if total > 0 {
			if _, pending, permanent, lastErr, serr := sc.store.PageIngestSummary(ctx, sh.ID); serr == nil && permanent > 0 && pending == 0 {
				reason := "ingest unrecoverable: " + lastErr
				if _, ferr := sc.store.FailShard(ctx, sh.ID, reason, false); ferr != nil {
					sc.log.Error("terminal shard fail failed", "shard_id", sh.ID, "err", ferr)
				} else {
					sc.log.Warn("shard finalized as failed", "shard_id", sh.ID, "job_id", sh.JobID,
						"dataset", sh.Dataset, "permanent_pages", permanent, "reason", reason)
					sc.publish("events.shards."+sh.JobID, map[string]any{
						"type": "shard_status", "shard_id": sh.ID, "status": model.ShardFailedFinal,
						"error": reason, "ts": time.Now(),
					})
				}
				return false
			}
		}
		// Fetch 未完成或仍有页面在 Ingest → 等待（§62）
		if sh.Status == model.ShardFetched || sh.Status == model.ShardIngested {
			now := time.Now()
			last, loaded := sc.replayCheckedAt.Load(sh.ID)
			if !loaded || now.Sub(last.(time.Time)) >= incompleteReplayInterval {
				sc.replayCheckedAt.Store(sh.ID, now)
				sc.replayIncompleteShard(ctx, sh)
			}
		}
		return false
	}
	// 校验（§63：边界校验 + 行一致性 + 可选 FINAL 复核）
	if err := sc.validateShard(ctx, sh); err != nil {
		sc.log.Error("shard validation failed", "shard_id", sh.ID, "err", err)
		_, ferr := sc.store.FailShard(ctx, sh.ID, "validate: "+err.Error(), true)
		if ferr != nil {
			sc.log.Error("fail shard failed", "err", ferr)
		}
		sc.publish("events.shards."+sh.JobID, map[string]any{
			"type": "shard_status", "shard_id": sh.ID, "status": model.ShardFailedRetry,
			"error": "validate: " + err.Error(), "ts": time.Now(),
		})
		return false
	}
	_ = sc.store.MarkPagesValidated(ctx, sh.ID)

	pages, _ := sc.store.PagesByShard(ctx, sh.ID)
	var minB, maxB int64
	var wireBytes, decodedBytes int64
	for _, p := range pages {
		if minB == 0 || (p.FromBlock > 0 && p.FromBlock < minB) {
			minB = p.FromBlock
		}
		if p.ToBlock > maxB {
			maxB = p.ToBlock
		}
		wireBytes += p.WireBytes
		decodedBytes += p.DecodedBytes
	}
	_ = wireBytes

	// 写对象存储 Shard Manifest（全部页面已上传时完整；否则尽力而为，§44）
	manifestKey := sc.writeShardManifest(ctx, sh)

	job, jerr := sc.store.GetJob(ctx, sh.JobID)
	if jerr != nil {
		sc.log.Error("get job failed", "shard_id", sh.ID, "err", jerr)
		return false
	}
	address := sc.subjectAddress(ctx, sh.SubjectID)
	ingestDuration := int64(0)
	if sh.FetchedAt != nil {
		ingestDuration = time.Since(*sh.FetchedAt).Milliseconds()
	}
	// Coverage Commit（§63）：Validation 通过后才提交
	sc.commitCoverage(ctx, sh, job.Chain, address, minB, maxB, sh.DownloadedRows, sh.DownloadedBytes)
	if err := sc.store.CompleteShardFinalized(ctx, sh.ID, manifestKey, sh.DownloadedRows, sh.DownloadedBytes, ingestDuration); err != nil {
		sc.log.Error("complete shard finalized failed", "shard_id", sh.ID, "err", err)
		return false
	}
	// 画像（§12/§32）
	sc.recordProfiles(ctx, sh, pages, job.Chain)
	// Spool 清理（全部页面已上传）
	sc.maybeCleanupSpool(ctx, sh)
	// Adaptive Sharding continuation remains per shard. Parent aggregation is
	// deliberately done once for the whole finalizer batch below; recomputing
	// the same dataset / subject / job for every shard created lock contention
	// and an artificial multi-second completion tail.
	if sc.cfg.Ankr.RangeShardingEnabled {
		if err := sc.planner.ContinueShards(ctx, sh); err != nil {
			sc.log.Error("continue shards failed", "err", err)
		}
	}
	sc.publish("events.shards."+sh.JobID, map[string]any{
		"type": "shard_status", "shard_id": sh.ID, "status": model.ShardCompleted,
		"rows": sh.DownloadedRows, "bytes": sh.DownloadedBytes, "ts": time.Now(),
	})
	sc.log.Info("shard finalized", "shard_id", sh.ID, "dataset", sh.Dataset,
		"rows", sh.DownloadedRows, "pages", total, "decoded_bytes", decodedBytes,
		"ingest_duration_ms", ingestDuration)
	sc.replayCheckedAt.Delete(sh.ID)
	return true
}

func (sc *Scheduler) recomputeFinalizedBatch(ctx context.Context, shards []model.Shard) {
	if len(shards) == 0 {
		return
	}
	datasets := make(map[string]model.Shard)
	subjects := make(map[string]model.Shard)
	jobs := make(map[string]model.Shard)
	for _, sh := range shards {
		datasets[sh.DatasetID] = sh
		subjects[sh.SubjectID] = sh
		jobs[sh.JobID] = sh
	}
	for id := range datasets {
		if err := sc.store.RecomputeDataset(ctx, id); err != nil {
			sc.log.Error("recompute dataset failed", "dataset_id", id, "err", err)
		}
	}
	for id, sh := range subjects {
		if err := sc.store.RecomputeSubject(ctx, id); err != nil {
			sc.log.Error("recompute subject failed", "subject_id", id, "err", err)
		}
		if sc.meta != nil {
			if job, err := sc.store.GetJob(ctx, sh.JobID); err == nil {
				address := sc.subjectAddress(ctx, id)
				if err := sc.meta.SyncFromCoverage(ctx, jobOrgID(job), jobWorkspaceID(job), job.Chain, address, "current"); err != nil {
					sc.log.Error("workspace address stats sync failed", "job_id", sh.JobID, "subject", address, "err", err)
				}
			}
		}
	}
	for _, sh := range jobs {
		// Reuse the established metadata and event path once per affected job.
		// Its dataset/subject recomputes above are already current.
		progress, err := sc.store.RecomputeJob(ctx, sh.JobID)
		if err != nil {
			sc.log.Error("recompute job failed", "job_id", sh.JobID, "err", err)
			continue
		}
		if job, err := sc.store.GetJob(ctx, sh.JobID); err == nil {
			sc.publish("events.jobs."+sh.JobID, map[string]any{
				"type": "job_status", "job_id": sh.JobID, "status": job.Status, "progress": progress, "ts": time.Now(),
			})
		}
	}
	for _, sh := range datasets {
		// V1.2 (§81/§24): guarded by actualsRecorded and now observes the
		// dataset after its single aggregate recompute.
		sc.recordEstimateActuals(ctx, sh)
	}
}

// validateShard §63：行一致性 + 边界校验 + FINAL 复核证据。
func (sc *Scheduler) validateShard(ctx context.Context, sh model.Shard) error {
	pages, err := sc.store.PagesByShard(ctx, sh.ID)
	if err != nil {
		return err
	}
	var parsed, inserted int64
	var minB, maxB int64
	for _, p := range pages {
		if p.Status != model.PageIngested && p.Status != model.PageValidated {
			return fmt.Errorf("page %d not ingested (status=%s)", p.PageNo, p.Status)
		}
		parsed += int64(p.RowsParsed)
		inserted += int64(p.RowsInserted)
		if p.FromBlock > 0 && (minB == 0 || p.FromBlock < minB) {
			minB = p.FromBlock
		}
		if p.ToBlock > maxB {
			maxB = p.ToBlock
		}
	}
	if parsed != inserted {
		return fmt.Errorf("row consistency failed: parsed=%d inserted=%d", parsed, inserted)
	}
	// 边界校验（§63 Boundary validation）：带界请求的行必须在区间内
	if sh.FromBlock > 0 && minB > 0 && minB < sh.FromBlock {
		return fmt.Errorf("boundary violation: min block %d below range start %d", minB, sh.FromBlock)
	}
	if sh.ToBlock > 0 && maxB > sh.ToBlock {
		return fmt.Errorf("boundary violation: max block %d above range end %d", maxB, sh.ToBlock)
	}
	// FINAL 复核证据（不阻塞：只记录，防重复下载量被误判）
	if sc.cfg.Ankr.ValidateFinalEnabled && sh.FromBlock > 0 && sh.ToBlock > 0 && parsed > 0 {
		job, jerr := sc.store.GetJob(ctx, sh.JobID)
		if jerr == nil {
			addr := sc.subjectAddress(ctx, sh.SubjectID)
			if n, err := sc.writer.CountFinal(ctx, sh.Dataset, job.Chain, addr, uint64(sh.FromBlock), uint64(sh.ToBlock)); err == nil {
				sc.log.Info("validation final count", "shard_id", sh.ID, "dataset", sh.Dataset,
					"final_unique_rows", n, "inserted", inserted)
			} else {
				sc.log.Warn("final count query failed (non-blocking)", "shard_id", sh.ID, "err", err)
			}
		}
	}
	return nil
}

// writeShardManifest 汇总页面上传结果写对象存储 Manifest（§39/§44）。
func (sc *Scheduler) writeShardManifest(ctx context.Context, sh model.Shard) string {
	pages, err := sc.store.PagesByShard(ctx, sh.ID)
	if err != nil || len(pages) == 0 {
		return ""
	}
	reqHash := ""
	if len(pages) > 0 {
		reqHash = pages[0].QueryHash
	}
	started := time.Now()
	var parts []manifestPart
	complete := true
	for _, p := range pages {
		if v, ok := sc.pipeline.uploads.Load(p.ID); ok {
			u := v.(uploadInfo)
			parts = append(parts, manifestPart{Key: u.key, SHA256: u.sha256, Rows: u.rows, BytesGz: u.bytesGz, PageToken: p.NextPageTokenHash})
		} else {
			complete = false
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Key < parts[j].Key })
	m := manifestMeta{
		RequestHash: reqHash, Provider: "ankr", Method: downloadMethodByDataset[sh.Dataset],
		Chain: "", Address: "", Dataset: sh.Dataset, FromBlock: sh.FromBlock, ToBlock: sh.ToBlock,
		StartedAt: started, FinishedAt: time.Now(), Parts: parts,
	}
	job, jerr := sc.store.GetJob(ctx, sh.JobID)
	if jerr == nil {
		m.Chain = job.Chain
		m.Provider = sc.sourceProviderForDataset(job.Chain, sh.Dataset)
		if job.Chain == "solana" {
			// Solana 的数据行由 getSignaturesForAddress + getTransaction 合成，
			// manifest 记录后者（真正产出行的调用）。
			m.Method = "getTransaction"
			m.Provider = "solana"
		} else if job.Chain == "tron" {
			if m.Provider == "trongrid" && sh.Dataset == "transactions" {
				m.Method = "trongrid_transactions"
			} else {
				m.Method = map[string]string{"transactions": "tron_getblockbylimitnext", "token_transfers": "tron_eth_getLogs", "internal_transactions": "tron_gettransactioninfobyblocknum"}[sh.Dataset]
			}
		}
		m.Address = sc.subjectAddress(ctx, sh.SubjectID)
	}
	if !complete {
		sc.log.Warn("shard manifest incomplete: raw upload pending, keeping spool copy",
			"shard_id", sh.ID, "uploaded", len(parts), "pages", len(pages))
	}
	key := rawManifestKey(m.Provider, m.Chain, sh.Dataset, reqHash, started)
	mb, _ := json.Marshal(m)
	if _, err := sc.raw.PutGzip(ctx, key, mb); err != nil {
		sc.log.Warn("manifest write failed", "err", err)
	}
	return key
}

// maybeCleanupSpool 全部页面已上传才释放本地 Spool（§44/§72）。
func (sc *Scheduler) maybeCleanupSpool(ctx context.Context, sh model.Shard) {
	if sc.spool == nil {
		return
	}
	pages, err := sc.store.PagesByShard(ctx, sh.ID)
	if err != nil || len(pages) == 0 {
		return
	}
	for _, p := range pages {
		if _, ok := sc.pipeline.uploads.Load(p.ID); !ok {
			return // 有未上传页面 → 保留本地副本（MinIO 故障时安全）
		}
	}
	job, jerr := sc.store.GetJob(ctx, sh.JobID)
	if jerr != nil {
		return
	}
	if err := sc.spool.RemoveShard(sh.JobID, sh.Dataset, sh.ID); err != nil {
		sc.log.Warn("spool cleanup failed", "shard_id", sh.ID, "err", err)
	}
	_ = job
}

// recordProfiles 写入 Shard / Page 画像（§12/§32）。
func (sc *Scheduler) recordProfiles(ctx context.Context, sh model.Shard, pages []model.Page, chain string) {
	// Benchmark outcomes are finalized asynchronously after canonical integrity
	// checks. Never let an INVALID/unfinished benchmark train production page or
	// shard profiles before that verdict exists.
	if job, err := sc.store.GetJob(ctx, sh.JobID); err == nil && job.Mode == "benchmark" {
		return
	}
	// Shard 画像
	var lat []int64
	var wireBytes, decodedBytes int64
	for _, p := range pages {
		wireBytes += p.WireBytes
		decodedBytes += p.DecodedBytes
		if p.FetchStartedAt != nil && p.BodyCompletedAt != nil {
			lat = append(lat, p.BodyCompletedAt.Sub(*p.FetchStartedAt).Milliseconds())
		}
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	var p50, p95 int64
	if len(lat) > 0 {
		p50 = lat[(len(lat)*50)/100]
		p95 = lat[(len(lat)*95)/100]
	}
	ingestMs := int64(0)
	if sh.FetchedAt != nil {
		ingestMs = time.Since(*sh.FetchedAt).Milliseconds()
	}
	sp := model.ShardProfile{
		Chain: chain, Dataset: sh.Dataset, SubjectClass: "address",
		BlockFrom: sh.FromBlock, BlockTo: sh.ToBlock,
		Rows: sh.DownloadedRows, Pages: len(pages),
		WireBytes: wireBytes, DecodedBytes: decodedBytes,
		FetchDurationMs: sh.FetchDurationMs, IngestDurationMs: ingestMs,
		PageLatencyP50Ms: p50, PageLatencyP95Ms: p95, PageGapAvgMs: sh.PageGapAvgMs,
	}
	if err := sc.store.UpsertShardProfile(ctx, sp); err != nil {
		sc.log.Warn("shard profile upsert failed", "err", err)
	}
	// Page 画像（query_shape = dataset）
	var rowsSum int64
	var avgLat int64
	for _, l := range lat {
		avgLat += l
	}
	if len(lat) > 0 {
		avgLat /= int64(len(lat))
	}
	for _, p := range pages {
		rowsSum += int64(p.RowsFetched)
	}
	pkey := sc.pagePolicy.Key(chain, sh.Dataset, "address", sc.subjectAddress(ctx, sh.SubjectID))
	st := sc.pagePolicy.Snapshot(pkey)
	var timeoutRate, oversizedRate float64
	if st != nil && st.Successes+st.Failures > 0 {
		total := float64(st.Successes + st.Failures)
		timeoutRate = float64(st.TotalTimeout) / total
		oversizedRate = float64(st.TotalTooLarge) / total
	}
	pp := model.PageProfile{
		Chain: chain, Dataset: sh.Dataset, QueryShape: sh.Dataset,
		PreferredPageSize: sh.PageSize, AvgRows: avgInt(pages),
		AvgLatencyMs: avgLat, P95LatencyMs: p95,
		TimeoutRate: timeoutRate, OversizedRate: oversizedRate,
		PagesObserved: int64(len(pages)),
	}
	if len(pages) > 0 {
		pp.AvgWireBytes = wireBytes / int64(len(pages))
		pp.AvgDecodedBytes = decodedBytes / int64(len(pages))
	}
	_ = rowsSum
	if err := sc.store.UpsertPageProfile(ctx, pp); err != nil {
		sc.log.Warn("page profile upsert failed", "err", err)
	}
}

func avgInt(pages []model.Page) int {
	if len(pages) == 0 {
		return 0
	}
	var n int64
	for _, p := range pages {
		n += int64(p.RowsFetched)
	}
	return int(n / int64(len(pages)))
}
