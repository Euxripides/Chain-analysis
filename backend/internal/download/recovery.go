// Crash Recovery（文档 §43/§93）：kill -9 重启后：
// 扫描 spool manifest → DURABLE 但未 INGEST 的页面 → replay ingest；
// 未 DURABLE 的进度 → 由 checkpoint 续传重新 fetch。必须无数据洞。
package download

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/spool"
)

// recoverSpool 启动时扫描本地 Spool 并重放未 Ingest 页面（§43）。
func (sc *Scheduler) recoverSpool(ctx context.Context) error {
	if !sc.cfg.Ankr.LocalSpoolEnabled || !sc.cfg.Ankr.AsyncIngestEnabled || sc.spool == nil {
		return nil
	}
	shardDirs, err := sc.spool.ListShards()
	if err != nil {
		return err
	}
	replayed := 0
	for _, sd := range shardDirs {
		sh, err := sc.store.GetShard(ctx, sd.ShardID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_ = sc.spool.RemoveShard(sd.JobID, sd.Dataset, sd.ShardID)
			} else {
				sc.log.Error("spool recovery lookup failed; preserving raw pages", "shard_id", sd.ShardID, "err", err)
			}
			continue
		}
		switch sh.Status {
		case model.ShardCompleted:
			// Completed facts may still await durable raw archiving.
			sc.maybeCleanupSpool(ctx, *sh)
			continue
		case model.ShardCancelled:
			continue
		case model.ShardFailedFinal:
			continue
		}
		// RUNNING/LEASED（崩溃前正在 fetch）：无论 spool 是否还有页面，
		// 一律交还调度从 checkpoint 续传（页面可能已 ingest+upload 并清理）。
		if sh.Status == model.ShardRunning || sh.Status == model.ShardLeased {
			if err := sc.store.ReleaseShard(ctx, sh.ID, "worker restarted, resume from checkpoint"); err != nil {
				sc.log.Error("release shard failed", "shard_id", sh.ID, "err", err)
				continue
			}
			sc.log.Info("released interrupted shard for resume", "shard_id", sh.ID, "checkpoint_page", sh.PageNo)
			sh.Status = model.ShardPending
		}
		pages, err := sc.spool.ScanShard(sd.JobID, sd.Dataset, sd.ShardID)
		if err != nil {
			sc.log.Error("spool scan failed", "shard_id", sd.ShardID, "err", err)
			continue
		}
		if len(pages) == 0 {
			continue
		}
		// 页面行补齐（DURABLE）
		for _, pf := range pages {
			p := model.Page{
				JobID: sd.JobID, ShardID: sd.ShardID, Dataset: sd.Dataset, PageNo: pf.PageNo,
				QueryHash: pf.Meta.QueryHash, PageSize: pf.Meta.PageSize,
				WireBytes: pf.Meta.WireBytes, DecodedBytes: pf.Meta.DecodedBytes,
				RawPath: pf.RawPath, RawSHA256: pf.Meta.SHA256,
				RowsFetched: pf.Meta.Rows, NextPageTokenHash: pf.Meta.NextPageTokenHash,
				FromBlock: pf.Meta.BlockFrom, ToBlock: pf.Meta.BlockTo,
			}
			if err := sc.store.EnsurePageRow(ctx, p); err != nil {
				sc.log.Error("ensure page row failed", "shard_id", sd.ShardID, "page_no", pf.PageNo, "err", err)
			}
		}
		// 未 Ingest 页面 → 重放（幂等：CH ReplacingMergeTree 稳定键去重，§59）
		need, err := sc.store.PagesNeedingIngest(ctx, sh.ID)
		if err != nil {
			sc.log.Error("pages needing ingest query failed", "shard_id", sh.ID, "err", err)
			continue
		}
		job, jerr := sc.store.GetJob(ctx, sh.JobID)
		if jerr != nil {
			continue
		}
		address := sc.subjectAddress(ctx, sh.SubjectID)
		for _, pg := range need {
			pr := pageRef{
				pageID: pg.ID, jobID: sh.JobID, shardID: sh.ID, dataset: sh.Dataset,
				pageNo: pg.PageNo, chain: job.Chain, address: address,
				reqHash: pg.QueryHash, rawPath: pg.RawPath,
				metaPath: spoolMetaPath(pg.RawPath),
				meta: spool.PageMeta{JobID: sh.JobID, Dataset: sh.Dataset, ShardID: sh.ID, PageNo: pg.PageNo,
					SHA256: pg.RawSHA256, DecodedBytes: pg.DecodedBytes, Rows: pg.RowsFetched,
					QueryHash: pg.QueryHash, NextPageTokenHash: pg.NextPageTokenHash,
					BlockFrom: pg.FromBlock, BlockTo: pg.ToBlock},
				minB: pg.FromBlock, maxB: pg.ToBlock, snapshotBlock: uint64(sh.ToBlock),
			}
			sc.pipeline.rememberPage(pr)
			sc.pipeline.submit(ctx, pr)
			replayed++
		}
		if replayed > 0 {
			sc.log.Info("spool replay scheduled", "shard_id", sd.ShardID, "pages", len(need))
		}
	}
	if replayed > 0 {
		sc.log.Info("crash recovery replay total", "pages", replayed)
	}
	// A restart can land after SetShardFetched but before every durable page has
	// reached ClickHouse. Such a shard is not leaseable and the lease reaper only
	// handles LEASED/RUNNING, so recover it from the durable page rows as well as
	// from the spool directory scan above.
	for _, status := range []string{model.ShardFetched, model.ShardIngested} {
		shards, listErr := sc.store.ListShardsByStatus(ctx, status, 256)
		if listErr != nil {
			return listErr
		}
		for _, sh := range shards {
			replayed += sc.replayIncompleteShard(ctx, sh)
		}
	}
	return nil
}

// replayIncompleteShard schedules durable/failed pages that have not reached
// INGESTED. submit() is page-id deduplicated, so startup recovery and the
// periodic finalizer may safely race without inserting a page twice.
func (sc *Scheduler) replayIncompleteShard(ctx context.Context, sh model.Shard) int {
	if sc.pipeline == nil {
		return 0
	}
	need, err := sc.store.PagesNeedingIngest(ctx, sh.ID)
	if err != nil {
		sc.log.Error("pages needing ingest query failed", "shard_id", sh.ID, "err", err)
		return 0
	}
	if len(need) == 0 {
		return 0
	}
	job, err := sc.store.GetJob(ctx, sh.JobID)
	if err != nil {
		sc.log.Error("replay get job failed", "shard_id", sh.ID, "err", err)
		return 0
	}
	address := sc.subjectAddress(ctx, sh.SubjectID)
	replayed := 0
	for _, pg := range need {
		pr := pageRef{
			pageID: pg.ID, jobID: sh.JobID, shardID: sh.ID, dataset: sh.Dataset,
			pageNo: pg.PageNo, chain: job.Chain, address: address,
			reqHash: pg.QueryHash, rawPath: pg.RawPath, metaPath: spoolMetaPath(pg.RawPath),
			meta: spool.PageMeta{JobID: sh.JobID, Dataset: sh.Dataset, ShardID: sh.ID,
				PageNo: pg.PageNo, SHA256: pg.RawSHA256, DecodedBytes: pg.DecodedBytes,
				Rows: pg.RowsFetched, QueryHash: pg.QueryHash,
				NextPageTokenHash: pg.NextPageTokenHash, BlockFrom: pg.FromBlock, BlockTo: pg.ToBlock},
			minB: pg.FromBlock, maxB: pg.ToBlock, snapshotBlock: uint64(sh.ToBlock),
		}
		sc.pipeline.rememberPage(pr)
		if sc.pipeline.submit(ctx, pr) {
			replayed++
		}
	}
	if replayed > 0 {
		sc.log.Info("incomplete fetched shard replay scheduled", "shard_id", sh.ID, "pages", replayed)
	}
	return replayed
}

const incompleteReplayInterval = 5 * time.Second

func spoolMetaPath(rawPath string) string {
	// rawPath: .../page-000001.raw → .../page-000001.manifest.json
	if len(rawPath) < 4 {
		return rawPath + ".manifest.json"
	}
	return rawPath[:len(rawPath)-4] + ".manifest.json"
}

func (sc *Scheduler) subjectAddress(ctx context.Context, subjectID string) string {
	// SubjectsByJob 兜底太贵，这里直接查
	var addr string
	_ = sc.store.Pool.QueryRow(ctx, "SELECT subject FROM download_subjects WHERE id=$1", subjectID).Scan(&addr)
	return addr
}
