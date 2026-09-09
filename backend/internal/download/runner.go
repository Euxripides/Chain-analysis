package download

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
	"github.com/blockchain-platform/blockchain-platform/internal/spool"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
)

var downloadMethodByDataset = map[string]string{
	"transactions":          "ankr_getTransactionsByAddress",
	"token_transfers":       "ankr_getTokenTransfers",
	"nft_transfers":         "ankr_getNftTransfers",
	"logs":                  "ankr_getLogs",
	"balances":              "ankr_getAccountBalance",
	"internal_transactions": "ankr_getInternalTransactions",
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

func requestHash(sh model.Shard, chain, address, method, providerName string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%d|%d", providerName, method, chain, address, sh.Dataset, sh.FromBlock, sh.ToBlock)
	if sh.FromTimestamp != nil {
		fmt.Fprintf(h, "|%d", sh.FromTimestamp.UnixMilli())
	}
	if sh.ToTimestamp != nil {
		fmt.Fprintf(h, "|%d", sh.ToTimestamp.UnixMilli())
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (sc *Scheduler) sourceProviderForDataset(chain, dataset string) string {
	if chain == "tron" {
		return sc.cfg.Tron.ProviderForDataset(dataset)
	}
	return "ankr"
}

func downloadPriority(priority int) string {
	if priority >= 100 {
		return "manual"
	}
	return "background"
}

// tokenHash 日志/指标只展示 token hash，不落真实 token（§42）。
func tokenHash(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}

// asyncMode 双路径开关（§98 Feature Flags）：全部启用且组件就绪才走新引擎，否则保留旧同步链路。
func (sc *Scheduler) asyncMode() bool {
	return sc.cfg.Ankr.AsyncIngestEnabled && sc.cfg.Ankr.LocalSpoolEnabled && sc.spool != nil && sc.pipeline != nil
}

// runShard 执行一个 Shard（§19 Fetch Plane 唯一职责）：
// 构造请求 → Provider Call → Response Guard → Durable Spool → Checkpoint → 下一页。
// ClickHouse / Metadata / Activity / 对象存储全部移出 Fetch Critical Path（§4）。
func (sc *Scheduler) runShard(ctx context.Context, sh model.Shard) {
	defer sc.wg.Done()
	// dispatchLoop reserves one weighted slot before starting the goroutine;
	// release it on every return path, including errors and cancellation.
	defer atomic.AddInt64(&sc.weighted, -1)
	atomic.AddInt64(&sc.active, 1)
	defer atomic.AddInt64(&sc.active, -1)
	defer sc.tokens.releaseShard(sh.ID)
	nonzeroTracked := false
	defer func() {
		if nonzeroTracked {
			atomic.AddInt64(&sc.activeNonzero, -1)
		}
	}()

	ctx = context.WithValue(ctx, "job_id", sh.JobID)
	ctx = context.WithValue(ctx, "shard_id", sh.ID)

	log := sc.log.With("job_id", sh.JobID, "shard_id", sh.ID, "dataset", sh.Dataset)
	started := time.Now()

	address := sc.subjectAddress(ctx, sh.SubjectID)
	job, err := sc.store.GetJob(ctx, sh.JobID)
	if err != nil {
		log.Error("get job failed", "err", err)
		return
	}
	chain := job.Chain
	var envelope *RangeEnvelope
	if sh.Dataset == "transactions" && (sh.FromBlock != 0 || sh.ToBlock != 0) {
		if sh.FromBlock < 0 || sh.ToBlock < sh.FromBlock || sh.ToBlock == int64(^uint64(0)>>1) {
			_, _ = sc.store.FailShard(ctx, sh.ID, "invalid immutable range envelope", false)
			return
		}
		e, eerr := NewRangeEnvelope(uint64(sh.FromBlock), uint64(sh.ToBlock)+1)
		if eerr != nil {
			_, _ = sc.store.FailShard(ctx, sh.ID, eerr.Error(), false)
			return
		}
		envelope = &e
	}

	method := downloadMethodByDataset[sh.Dataset]
	providerName := sc.sourceProviderForDataset(chain, sh.Dataset)
	if chain == "tron" {
		if providerName == "trongrid" && sh.Dataset == "transactions" {
			method = "trongrid_transactions"
		} else {
			method = map[string]string{"transactions": "tron_getblockbylimitnext", "token_transfers": "tron_eth_getLogs", "internal_transactions": "tron_gettransactioninfobyblocknum"}[sh.Dataset]
		}
	}
	reqHash := requestHash(sh, chain, address, method, providerName)
	manifest := manifestMeta{
		RequestHash: reqHash, Provider: providerName, Method: method, Chain: chain, Address: address,
		Dataset: sh.Dataset, FromBlock: sh.FromBlock, ToBlock: sh.ToBlock,
		FromTimestamp: sh.FromTimestamp, ToTimestamp: sh.ToTimestamp, StartedAt: started, Parts: []manifestPart{},
	}

	pageToken := sh.PageToken
	pageNo := sh.PageNo
	rowsTotal := sh.DownloadedRows
	bytesTotal := sh.DownloadedBytes
	var wireTotal, decodedTotal int64
	var pageGaps []int64
	var lastBodyCompleted time.Time
	var shardMinB, shardMaxB int64
	var tronLastTimestamp time.Time
	var tronLastTxID string
	var tronPrimaryRows, tronFallbackRows, tronProviderSwitches int64
	var tronActiveProvider string
	if chain == "tron" && sh.PageState != nil {
		if ms, ok := numberFromAny(sh.PageState["tron_last_timestamp_ms"]); ok && ms > 0 {
			tronLastTimestamp = time.UnixMilli(ms).UTC()
		}
		if txID, ok := sh.PageState["tron_last_tx_id"].(string); ok {
			tronLastTxID = txID
		}
		tronPrimaryRows, _ = numberFromAny(sh.PageState["primary_rows"])
		tronFallbackRows, _ = numberFromAny(sh.PageState["fallback_rows"])
		tronProviderSwitches, _ = numberFromAny(sh.PageState["provider_switches"])
		tronActiveProvider, _ = sh.PageState["active_provider"].(string)
	}
	seenPageTokens := map[string]struct{}{}
	if providerName == "trongrid" && (sh.Dataset == "transactions" || sh.Dataset == "token_transfers") && pageToken != "" {
		seenPageTokens[pageToken] = struct{}{}
	}
	var tronRecoveryFrom *time.Time
	var tronRecoveryBlock *uint64
	tronCursorRecovered := false

	// 范围完备性守卫（BUG-002 修复）
	var guardSawAnyRow bool
	var guardBelowSeen bool
	var guardAboveSeen bool
	var guardFinalPageSaturated bool

	// PagePolicy：Resume 继承上次自适应状态（§31/§32）
	pkey := sc.pagePolicy.Key(chain, sh.Dataset, "address", address)
	if sh.PageSize > 0 {
		if st := pageStateFromMap(sh.PageState); st != nil {
			st.CurrentPageSize = sh.PageSize
			sc.pagePolicy.Restore(pkey, st)
		}
	}
	currentSize := sh.PageSize
	if currentSize <= 0 {
		// §32：新任务从历史最佳 pageSize 启动（画像成熟后才生效）
		if sc.cfg.Ankr.AdaptivePageEnabled {
			if p, err := sc.store.GetPageProfile(ctx, chain, sh.Dataset, sh.Dataset); err == nil && p != nil && p.PagesObserved >= 5 {
				sc.pagePolicy.Restore(pkey, &ankr.PagePolicyState{CurrentPageSize: p.PreferredPageSize, UpdatedAt: time.Now()})
				sc.log.Debug("page profile preferred size applied", "dataset", sh.Dataset, "page_size", p.PreferredPageSize)
			}
		}
		currentSize = sc.pagePolicy.Current(pkey, sh.Dataset)
	}
	if chain == "tron" && (currentSize <= 0 || currentSize > 200) {
		currentSize = 200
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
		// Backpressure（§71/§72）：Spool/CH/Provider 保护状态停止新页请求
		if sc.bp != nil && !sc.bp.FetchAllowed() {
			log.Warn("fetch paused by backpressure", "state", sc.bp.State().String(), "reason", sc.bp.Reason())
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		req := provider.FetchRequest{
			Chain: chain, Dataset: sh.Dataset, Address: address,
			PageSize: currentSize, PageToken: pageToken, Priority: downloadPriority(sh.Priority),
		}
		if sh.PageState != nil {
			req.QueryMode, _ = sh.PageState["query_mode"].(string)
		}
		switch sh.Dataset {
		case "balances":
			req.FromBlock, req.ToBlock, req.FromTime, req.ToTime = nil, nil, nil, nil
		default:
			// V1.1：实测 Ankr 尊重 block 边界（fromBlock 为枚举起点、toBlock 为终点），
			// 多 Range 必须传边界（§4/§6）；客户端过滤 + 范围守卫保留为安全网。
			if !(sh.FromBlock == 0 && sh.ToBlock == 0 && sh.FromTimestamp == nil) && !(chain == "tron" && sh.FromTimestamp != nil) {
				f := uint64(sh.FromBlock)
				t := uint64(sh.ToBlock)
				if envelope != nil {
					var rerr error
					f, t, rerr = ToAnkrInclusiveRange(envelope.Start, envelope.End)
					if rerr != nil {
						_, _ = sc.store.FailShard(ctx, sh.ID, rerr.Error(), false)
						return
					}
				}
				req.FromBlock = &f
				req.ToBlock = &t
			}
			if sh.FromTimestamp != nil {
				req.FromTime = sh.FromTimestamp
			}
			if sh.ToTimestamp != nil {
				req.ToTime = sh.ToTimestamp
			}
			if chain == "tron" && tronRecoveryFrom != nil {
				req.FromTime = tronRecoveryFrom
			}
			if chain == "tron" && tronRecoveryBlock != nil {
				req.FromBlock = tronRecoveryBlock
				req.FromTime = nil
			}
		}

		// 全局内存预算（§47/§48）：按预测大小预留 decoded 内存，超预算阻塞 = 内存背压
		estBytes := sc.predictPageBytes(chain, sh.Dataset, currentSize)
		if sc.mem != nil {
			sc.mem.Reserve(estBytes)
		}

		// Page Gap（§35）：上一页 BODY_COMPLETE → 本页请求发出
		pageStart := time.Now()
		if !lastBodyCompleted.IsZero() {
			gap := pageStart.Sub(lastBodyCompleted).Seconds()
			observability.AnkrPageGapSeconds.Observe(gap)
			pageGaps = append(pageGaps, int64(gap*1000))
		}
		req.RequestID = fmt.Sprintf("%s-%d-%d", sh.ID, pageNo+1, time.Now().UnixNano())
		res, dec, err := sc.fetchWithRetry(ctx, pkey, sh.Dataset, req)
		if dec != nil && dec.SplitRange {
			if sc.mem != nil {
				sc.mem.Release(estBytes)
			}
			if sc.cfg.Ankr.V14Enabled {
				// §10/§11：V1.4 只允许 READY/PLANNED 拆分；FETCHING Range 不拆 →
				// 标记 retryable 失败，由 resume/规划层在 PENDING 状态重新规划拆分。
				msg := "[v14_split_requested_on_running_shard] READY-only adaptive split (§10): replan via resume"
				sc.log.Warn(msg, "shard_id", sh.ID, "dataset", sh.Dataset)
				_, _ = sc.store.FailShard(ctx, sh.ID, msg, true)
				return
			}
			sc.log.Warn("split range on repeated min-page failures", "shard_id", sh.ID, "dataset", sh.Dataset, "range", fmt.Sprintf("%d-%d", sh.FromBlock, sh.ToBlock))
			sc.splitShardRange(ctx, sh)
			return
		}
		if err != nil {
			if sc.mem != nil {
				sc.mem.Release(estBytes)
			}
			if tron.IsFallbackTooExpensive(err) {
				_ = sc.store.PauseShard(ctx, sh.ID)
				_ = sc.store.SetJobStatusError(ctx, sh.JobID, model.JobPausedFallbackTooExpensive, err.Error())
				log.Warn("TRON fallback range exceeds safety gate; task paused", "err", err)
				return
			}
			if chain == "tron" && pageToken != "" && !tronCursorRecovered {
				if pe, ok := err.(*provider.Error); ok && pe.Class == provider.ClassInvalidRequest {
					if providerName == "ankr" {
						if next, ok := numberFromAny(sh.PageState["tron_next_block"]); ok && next > 0 {
							overlap := int64(sc.cfg.Tron.OverlapBlocks)
							if overlap <= 0 {
								overlap = 20
							}
							from := next - overlap
							if from < 0 {
								from = 0
							}
							block := uint64(from)
							tronRecoveryBlock = &block
						}
						pageToken = ""
						tronCursorRecovered = true
						log.Warn("TRON Ankr block cursor invalid; rebuilding with overlap", "from_block", tronRecoveryBlock)
						continue
					}
					overlap := sc.cfg.Tron.IncrementalOverlap
					if overlap <= 0 {
						overlap = 3 * time.Minute
					}
					if !tronLastTimestamp.IsZero() {
						from := tronLastTimestamp.Add(-overlap)
						tronRecoveryFrom = &from
					}
					pageToken = ""
					tronCursorRecovered = true
					log.Warn("TRON fingerprint invalid; rebuilding cursor with overlap", "from_timestamp", tronRecoveryFrom)
					continue
				}
			}
			retryable := false
			errMsg := err.Error()
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				retryable = true
			}
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
		if chain == "tron" {
			primaryRows, fallbackRows, activeProvider := tronRowSources(res.Rows)
			tronPrimaryRows += primaryRows
			tronFallbackRows += fallbackRows
			if activeProvider != "" && tronActiveProvider != "" && activeProvider != tronActiveProvider {
				tronProviderSwitches++
			}
			if activeProvider != "" {
				tronActiveProvider = activeProvider
			}
			currentSize = res.RequestPageSize
			if currentSize <= 0 || currentSize > 200 {
				currentSize = 200
			}
		} else {
			currentSize = req.PageSize
		}
		lastBodyCompleted = time.Now()
		var invariant RangeInvariantResult
		if envelope != nil {
			invariant, err = ValidateTransactionRange(envelope.Start, envelope.End, res.Rows)
			log.Info("ankr_page_provenance", "request_id", res.RequestID, "lease_owner", sh.LeaseOwner, "lease_epoch", sh.LeaseEpoch, "page_seq", pageNo+1, "attempt", res.AttemptNo, "range_start", envelope.Start, "range_end_exclusive", envelope.End, "request_from_block", res.RequestFromBlock, "request_to_block_inclusive", res.RequestToBlock, "request_page_size", res.RequestPageSize, "page_token_present", pageToken != "", "page_token_hash", tokenHash(pageToken), "response_rows", len(res.Rows), "response_min_block", invariant.MinBlock, "response_max_block", invariant.MaxBlock, "out_of_range_rows", invariant.OutOfRangeRows, "wire_bytes", res.WireBytes, "decoded_bytes", res.DecodedBytes, "started_at", res.StartedAt, "body_complete_at", res.BodyCompleteAt)
			if err != nil {
				observability.AnkrRangeViolation.WithLabelValues(chain, sh.Dataset).Inc()
				observability.AnkrOutOfRangeRows.WithLabelValues(chain, sh.Dataset).Add(float64(invariant.OutOfRangeRows))
				if sc.spool != nil {
					_, _ = sc.spool.WriteQuarantine(spool.QuarantineMeta{JobID: sh.JobID, ShardID: sh.ID, Dataset: sh.Dataset, PageNo: pageNo + 1, Attempt: res.AttemptNo, RequestID: res.RequestID, LeaseEpoch: sh.LeaseEpoch, RangeStart: envelope.Start, RangeEndExclusive: envelope.End, RequestFromBlock: *res.RequestFromBlock, RequestToBlock: *res.RequestToBlock, RequestPageSize: res.RequestPageSize, PageTokenHash: tokenHash(pageToken), ResponseRows: len(res.Rows), ResponseMinBlock: invariant.MinBlock, ResponseMaxBlock: invariant.MaxBlock, OutOfRangeRows: invariant.OutOfRangeRows, StartedAt: res.StartedAt, BodyCompleteAt: res.BodyCompleteAt}, res.RawResponseBody)
				}
				_, _ = sc.store.FailShard(ctx, sh.ID, err.Error(), false)
				return
			}
		}
		if providerName == "trongrid" && (sh.Dataset == "transactions" || sh.Dataset == "token_transfers") {
			if res.NextPageToken != "" {
				if _, seen := seenPageTokens[res.NextPageToken]; seen {
					err := fmt.Errorf("[pagination_loop] TronGrid fingerprint repeated for address stream")
					_, _ = sc.store.FailShard(ctx, sh.ID, err.Error(), false)
					return
				}
				seenPageTokens[res.NextPageToken] = struct{}{}
			}
		} else if chain != "tron" {
			if err := sc.tokens.claim(res.NextPageToken, sh.ID, uint64(sh.FromBlock), uint64(sh.ToBlock)+1); err != nil {
				observability.AnkrTokenCrossShard.WithLabelValues(chain, sh.Dataset).Inc()
				_, _ = sc.store.FailShard(ctx, sh.ID, err.Error(), false)
				return
			}
		}

		// 内存预算修正：实际 decoded 大于预估时补预留（可能阻塞）
		actualDecoded := res.DecodedBytes
		// Release exactly what was reserved.  When the response is smaller than
		// the prediction, releasing only actualDecoded leaks the prediction
		// delta; repeated pages then exhaust the global byte budget and leave all
		// subsequent shards blocked forever in byteBudget.Reserve.
		reservedBytes := estBytes
		if sc.mem != nil && actualDecoded > estBytes {
			sc.mem.ReserveExtra(actualDecoded - estBytes)
			reservedBytes = actualDecoded
		}

		var bodyEst int64
		for _, r := range res.Rows {
			bodyEst += int64(len(r))
		}
		if chain != "tron" {
			nextSize := sc.pagePolicy.OnSuccess(pkey, sh.Dataset, int64(currentSize), bodyEst, time.Since(pageStart))
			if nextSize != currentSize {
				currentSize = nextSize
				log.Debug("page size upgraded", "page_size", nextSize, "dataset", sh.Dataset)
			}
		}

		// 客户端侧范围过滤（transactions/nft 不信任 Provider 边界）
		preFilterCount := len(res.Rows)
		filtered, belowSeenPage, aboveSeenPage := sc.filterRows(sh, res.Rows)
		res.Rows = filtered
		if len(res.Rows) > 0 && !nonzeroTracked {
			atomic.AddInt64(&sc.activeNonzero, 1)
			nonzeroTracked = true
		}
		if preFilterCount > 0 {
			guardSawAnyRow = true
		}
		if belowSeenPage {
			guardBelowSeen = true
		}
		if aboveSeenPage {
			guardAboveSeen = true
		}
		if preFilterCount >= currentSize {
			guardFinalPageSaturated = true
		} else {
			guardFinalPageSaturated = false
		}

		wireTotal += res.WireBytes
		decodedTotal += res.DecodedBytes

		// 本页行块范围（过滤后）
		var minB, maxB int64
		for _, raw := range res.Rows {
			if bn, ok := ingest.RawBlockNumber(raw); ok && bn > 0 {
				trackBlocks(&minB, &maxB, int64(bn))
			}
			if chain == "tron" {
				if ts, ok := ingest.RawTimestamp(raw); ok && (tronLastTimestamp.IsZero() || ts.Before(tronLastTimestamp)) {
					tronLastTimestamp = ts
				}
				if txID, ok := rawTransactionID(raw); ok {
					tronLastTxID = txID
				}
			}
		}
		if shardMinB == 0 || (minB > 0 && minB < shardMinB) {
			shardMinB = minB
		}
		if maxB > shardMaxB {
			shardMaxB = maxB
		}

		if sc.asyncMode() {
			// ---- 新引擎：Durable Spool → Checkpoint → 异步 Ingest（§37/§40/§41）----
			pageNo++
			meta := spool.PageMeta{
				JobID: sh.JobID, Dataset: sh.Dataset, ShardID: sh.ID, PageNo: pageNo,
				BlockFrom: minB, BlockTo: maxB, PageSize: currentSize,
				WireBytes: res.WireBytes, DecodedBytes: res.DecodedBytes,
				QueryHash: reqHash, NextPageTokenHash: tokenHash(res.NextPageToken),
				RequestStartedAt: pageStart, BodyCompletedAt: lastBodyCompleted, ReceivedAt: time.Now(),
			}
			rawPath, metaPath, werr := sc.spool.WritePage(meta, res.Rows)
			if werr != nil {
				if sc.mem != nil {
					sc.mem.Release(reservedBytes)
				}
				_, _ = sc.store.FailShard(ctx, sh.ID, "spool write: "+werr.Error(), true)
				return
			}
			if sc.mem != nil {
				sc.mem.Release(reservedBytes)
			}
			rowsTotal += int64(len(res.Rows))
			bytesTotal += res.DecodedBytes
			pg := model.Page{
				JobID: sh.JobID, ShardID: sh.ID, Dataset: sh.Dataset, PageNo: pageNo,
				QueryHash: reqHash, PageSize: currentSize,
				WireBytes: res.WireBytes, DecodedBytes: res.DecodedBytes,
				RawPath: rawPath, RawSHA256: meta.SHA256, RowsFetched: len(res.Rows),
				NextPageTokenHash: tokenHash(res.NextPageToken),
				FromBlock:         minB, ToBlock: maxB,
				FetchStartedAt: &pageStart, BodyCompletedAt: &lastBodyCompleted,
			}
			pageID, cerr2 := sc.store.PageDurableCheckpointCAS(ctx, pg, sh.LeaseEpoch, res.NextPageToken, pageNo, currentSize, rowsTotal, bytesTotal, tronPageState(sc.pagePolicy.Snapshot(pkey), chain, providerName, sh.Dataset, address, req.QueryMode, tronLastTimestamp, tronLastTxID, res.NextPageToken, res.RequestToBlock, tronPrimaryRows, tronFallbackRows, tronProviderSwitches, tronActiveProvider))
			if cerr2 != nil {
				if store.IsCheckpointRangeMismatch(cerr2) {
					observability.AnkrCheckpointRangeMismatch.WithLabelValues(chain, sh.Dataset).Inc()
				}
				if leaseEpochMismatch(cerr2) {
					// §33：epoch 不匹配 = 本 worker 已失去 Shard 所有权（reaper/其它 worker 接管）
					observability.AnkrFetchLeaseEpochMismatchTotal.Inc()
					log.Warn("lease epoch mismatch, stopping shard work (split-brain prevented)")
					return
				}
				log.Error("page durable checkpoint failed", "err", cerr2)
				_, _ = sc.store.FailShard(ctx, sh.ID, "checkpoint: "+cerr2.Error(), true)
				return
			}
			pr := pageRef{
				pageID: pageID, jobID: sh.JobID, shardID: sh.ID, dataset: sh.Dataset,
				pageNo: pageNo, chain: chain, address: address, reqHash: reqHash,
				rawPath: rawPath, metaPath: metaPath, meta: meta,
				minB: minB, maxB: maxB, snapshotBlock: uint64(sh.ToBlock),
			}
			sc.pipeline.rememberPage(pr)
			if !sc.pipeline.submit(ctx, pr) {
				return // 停机：checkpoint 已持久，重启后重放
			}
			sc.log.Info("page durable", "page_no", pageNo, "rows", len(res.Rows),
				"wire_bytes", res.WireBytes, "decoded_bytes", res.DecodedBytes,
				"provider_latency_ms", time.Since(pageStart).Milliseconds())
		} else {
			// ---- 旧同步链路（Feature Flag 关闭时的基线，§98）----
			if len(res.Rows) > 0 {
				now := time.Now()
				partKey := fmt.Sprintf("raw/%s/%s/%s/%s/%s/%s/part-%06d.json.gz",
					providerName,
					chain, sh.Dataset, now.Format("2006"), now.Format("01"), reqHash, pageNo+1)
				rawPart, _ := json.Marshal(map[string]any{"rows": res.Rows})
				sum := sha256.Sum256(rawPart)
				rstart := time.Now()
				n, rerr := sc.raw.PutGzip(ctx, partKey, rawPart)
				observability.RawWriteLatencyMs.WithLabelValues(sc.cfg.RawBucket).Observe(float64(time.Since(rstart).Milliseconds()))
				if rerr != nil {
					if sc.mem != nil {
						sc.mem.Release(reservedBytes)
					}
					_, _ = sc.store.FailShard(ctx, sh.ID, "raw persist: "+rerr.Error(), true)
					return
				}
				bytesTotal += n
				manifest.Parts = append(manifest.Parts, manifestPart{
					Key: partKey, SHA256: hex.EncodeToString(sum[:]), Rows: len(res.Rows), BytesGz: n, PageToken: tokenHash(pageToken),
				})
				minB2, maxB2, ierr := sc.ingestRows(ctx, sh, res.Rows, chain, address)
				if ierr != nil {
					if sc.mem != nil {
						sc.mem.Release(reservedBytes)
					}
					_, _ = sc.store.FailShard(ctx, sh.ID, "ingest: "+ierr.Error(), true)
					return
				}
				if minB == 0 || (minB2 > 0 && minB2 < minB) {
					minB = minB2
				}
				if maxB2 > maxB {
					maxB = maxB2
				}
				rowsTotal += int64(len(res.Rows))
			}
			if sc.mem != nil {
				sc.mem.Release(reservedBytes)
			}
			pageNo++
			if err := sc.store.CheckpointShardCAS(ctx, sh.ID, sh.LeaseEpoch, res.NextPageToken, pageNo, currentSize, rowsTotal, bytesTotal, tronPageState(sc.pagePolicy.Snapshot(pkey), chain, providerName, sh.Dataset, address, req.QueryMode, tronLastTimestamp, tronLastTxID, res.NextPageToken, res.RequestToBlock, tronPrimaryRows, tronFallbackRows, tronProviderSwitches, tronActiveProvider)); err != nil {
				if store.IsCheckpointRangeMismatch(err) {
					observability.AnkrCheckpointRangeMismatch.WithLabelValues(chain, sh.Dataset).Inc()
				}
				if leaseEpochMismatch(err) {
					observability.AnkrFetchLeaseEpochMismatchTotal.Inc()
					log.Warn("lease epoch mismatch at checkpoint, stopping shard work")
					return
				}
				log.Error("checkpoint failed", "err", err)
			}
		}

		if !res.HasMore {
			break
		}
		if guardBelowSeen && guardAboveSeen {
			break
		}
		pageToken = res.NextPageToken
	}

	// BUG-002 守卫：翻页结束后校验目标范围确实被覆盖
	if verdict := rangeCoverageVerdict(sh, guardSawAnyRow, guardBelowSeen, guardAboveSeen, rowsTotal, guardFinalPageSaturated); verdict != nil {
		log.Error("range not reached by provider pagination; refusing silent 0-row completion",
			"shard_id", sh.ID, "dataset", sh.Dataset,
			"from_block", sh.FromBlock, "to_block", sh.ToBlock,
			"pages", pageNo, "rows", rowsTotal, "err", verdict)
		newStatus, ferr := sc.store.FailShard(ctx, sh.ID, verdict.Error(), true)
		if ferr != nil {
			log.Error("fail shard failed", "err", ferr)
		}
		sc.publish("events.shards."+sh.JobID, map[string]any{
			"type": "shard_status", "shard_id": sh.ID, "status": newStatus,
			"error": verdict.Error(), "ts": time.Now(),
		})
		return
	}

	if sc.asyncMode() {
		// ---- 新引擎：Fetch 完成 → FETCHED，等待 Finalizer 校验后 COMPLETE（§62）----
		fetchDuration := time.Since(started).Milliseconds()
		var gapAvg int64
		if len(pageGaps) > 0 {
			var sum int64
			for _, g := range pageGaps {
				sum += g
			}
			gapAvg = sum / int64(len(pageGaps))
		}
		if err := sc.store.SetShardFetchedCAS(ctx, sh.ID, sh.LeaseEpoch, pageNo-sh.PageNo, wireTotal, decodedTotal, fetchDuration, gapAvg); err != nil {
			if leaseEpochMismatch(err) {
				observability.AnkrFetchLeaseEpochMismatchTotal.Inc()
				log.Warn("lease epoch mismatch at fetch completion, stopping shard work")
				return
			}
			log.Error("set shard fetched failed", "err", err)
			return
		}
		log.Info("shard fetch complete", "rows", rowsTotal, "pages", pageNo-sh.PageNo,
			"wire_bytes", wireTotal, "decoded_bytes", decodedTotal,
			"fetch_duration_ms", fetchDuration, "page_gap_avg_ms", gapAvg)
		return
	}

	// ---- 旧同步链路收尾 ----
	manifest.FinishedAt = time.Now()
	manifestKey := fmt.Sprintf("raw/%s/%s/%s/%s/%s/%s/manifest.json.gz",
		providerName, chain, sh.Dataset, manifest.StartedAt.Format("2006"), manifest.StartedAt.Format("01"), reqHash)
	mb, _ := json.Marshal(manifest)
	if _, err := sc.raw.PutGzip(ctx, manifestKey, mb); err != nil {
		log.Warn("manifest write failed", "err", err)
	}
	if err := sc.store.CompleteShard(ctx, sh.ID, manifestKey, rowsTotal, bytesTotal); err != nil {
		log.Error("complete shard failed", "err", err)
		return
	}
	sc.commitCoverage(ctx, sh, chain, address, shardMinB, shardMaxB, rowsTotal, bytesTotal)
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

func tronPageState(state *ankr.PagePolicyState, chain, providerName, dataset, address, queryMode string, lastTimestamp time.Time, lastTxID, nextPageToken string, requestToBlock *uint64, primaryRows, fallbackRows, providerSwitches int64, activeProvider string) map[string]any {
	out := pageStateToMap(state)
	// Query intent is provider-neutral checkpoint state. Infer it from the
	// immutable request shape when restoring older shards that predate V4.
	if chain == "tron" {
		out["query_mode"] = queryMode
		out["primary_rows"], out["fallback_rows"], out["provider_switches"] = primaryRows, fallbackRows, providerSwitches
		if activeProvider != "" {
			out["active_provider"] = activeProvider
		}
		if info, ok := tron.InspectHybridCursor(nextPageToken); ok {
			out["tron_cursor_type"] = "hybrid"
			out["active_provider"] = info.ActiveProvider
			out["covered_start"], out["covered_end"] = info.CoveredStart, info.CoveredEnd
			if info.ActiveProvider == "ankr" {
				out["fallback_start_block"], out["fallback_end_block"] = info.FallbackStart, info.FallbackEnd
			}
		}
	}
	if chain == "tron" && !lastTimestamp.IsZero() {
		if out == nil {
			out = map[string]any{}
		}
		out["tron_last_timestamp_ms"] = lastTimestamp.UnixMilli()
	}
	if chain == "tron" && providerName == "trongrid" && !strings.HasPrefix(nextPageToken, "tron:hybrid:") {
		if out == nil {
			out = map[string]any{}
		}
		out["tron_provider"], out["tron_cursor_type"] = "trongrid", "fingerprint"
		out["tron_dataset"], out["tron_address"] = dataset, address
		out["tron_fingerprint"] = nextPageToken
		if lastTxID != "" {
			out["tron_last_tx_id"] = lastTxID
		}
	}
	if chain == "tron" && providerName == "ankr" && strings.HasPrefix(nextPageToken, "ankr:block:") {
		if payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(nextPageToken, "ankr:block:")); err == nil {
			var cursor struct {
				Provider  string `json:"provider"`
				Type      string `json:"type"`
				NextBlock uint64 `json:"next_block"`
				EndBlock  uint64 `json:"end_block"`
				Span      uint64 `json:"span"`
			}
			if json.Unmarshal(payload, &cursor) == nil {
				if out == nil {
					out = map[string]any{}
				}
				out["tron_provider"], out["tron_cursor_type"] = cursor.Provider, cursor.Type
				out["tron_next_block"], out["tron_scan_to_block"], out["tron_log_span"] = cursor.NextBlock, cursor.EndBlock, cursor.Span
			}
		}
	}
	if chain == "tron" && providerName == "ankr" && requestToBlock != nil {
		if out == nil {
			out = map[string]any{}
		}
		out["tron_provider"] = "ankr"
		out["tron_cursor_type"] = "block"
		out["tron_last_success_block"] = *requestToBlock
	}
	return out
}

func tronRowSources(rows []json.RawMessage) (primary, fallback int64, active string) {
	for _, raw := range rows {
		var row map[string]any
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		if strings.EqualFold(fmt.Sprint(row["_provider"]), "ankr") {
			fallback++
			active = "ankr"
		} else {
			primary++
			active = "trongrid"
		}
	}
	return
}

func numberFromAny(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	}
	return 0, false
}

func rawTransactionID(raw json.RawMessage) (string, bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", false
	}
	for _, key := range []string{"txID", "transaction_id", "tx_id"} {
		if value, ok := m[key].(string); ok && value != "" {
			return value, true
		}
	}
	return "", false
}

// predictPageBytes 响应大小预测（§49）：page_size × avg_bytes_per_row（画像缺省用经验值）。
func (sc *Scheduler) predictPageBytes(chain, dataset string, pageSize int) int64 {
	perRow := int64(1024)
	if p, err := sc.store.GetPageProfile(context.Background(), chain, dataset, dataset); err == nil && p != nil && p.AvgDecodedBytes > 0 && p.PagesObserved > 3 {
		if p.AvgRows > 0 {
			perRow = p.AvgDecodedBytes / int64(p.AvgRows)
		}
	}
	if perRow < 256 {
		perRow = 256
	}
	return int64(pageSize) * perRow
}

// jobControl 在每页之间检查 Job 状态（暂停/取消/上下文退出）。
func (sc *Scheduler) jobControl(ctx context.Context, sh model.Shard) (string, error) {
	job, err := sc.store.GetJob(ctx, sh.JobID)
	if err != nil {
		return "continue", err
	}
	subjectStatus, subjectErr := sc.store.SubjectStatus(ctx, sh.JobID, sh.SubjectID)
	if subjectErr != nil {
		return "continue", subjectErr
	}
	if subjectStatus == model.ItemPaused {
		_ = sc.store.PauseShard(ctx, sh.ID)
		return "paused", nil
	}
	if subjectStatus == model.ItemCompleted || subjectStatus == model.ItemSkipped {
		_ = sc.store.CancelShard(ctx, sh.ID)
		return "cancelled", nil
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
		// 任务取消：本地 Spool 可安全清理（已 Ingest 页面在 CH，未 Ingest 页面随任务丢弃）
		if sc.spool != nil {
			_ = sc.spool.RemoveShard(sh.JobID, sh.Dataset, sh.ID)
		}
		return "cancelled", nil
	}
	select {
	case <-ctx.Done():
		_ = sc.store.PauseShard(context.Background(), sh.ID)
		return "ctx-done", nil
	default:
	}
	return "continue", nil
}

// fetchWithRetry 重试决策矩阵（§21/§27/§28）。
func (sc *Scheduler) fetchWithRetry(ctx context.Context, pkey, dataset string, req provider.FetchRequest) (provider.FetchResult, *ankr.PageDecision, error) {
	var lastErr error
	var lastDec *ankr.PageDecision
	maxAttempts := sc.cfg.Ankr.PageRetryMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	// TronGrid owns its retry/backoff budget inside the HTTP client. The
	// scheduler must not replay an exhausted fingerprint page a second time,
	// otherwise one 429/timeout can multiply request cost and break quota math.
	tronGridOwned := req.Chain == "tron" && sc.sourceProviderForDataset(req.Chain, dataset) == "trongrid" &&
		(dataset == "transactions" || dataset == "token_transfers")
	if tronGridOwned {
		maxAttempts = 1
	}
	budget := time.Duration(sc.cfg.Ankr.PageRetryBudgetSeconds) * time.Second
	budgetStart := time.Now()
	exhaustedByBudget := false
	for attempt := 0; retryBudgetOK(attempt, budgetStart, maxAttempts, budget); attempt++ {
		attemptReq := req
		attemptReq.AttemptNo = attempt + 1
		attemptReq.RequestID = fmt.Sprintf("%s-a%d", req.RequestID, attempt+1)
		actx := context.WithValue(ctx, "retry_attempt", attempt)
		requestTimeout := sc.cfg.Ankr.RequestTimeout
		// A time-range fallback must first resolve both timestamps to TRON blocks.
		// That work is bounded and cancellable, but can legitimately require more
		// RPC round trips than one ordinary Ankr page request.
		if tronGridOwned && dataset == "token_transfers" && req.FromTime != nil {
			requestTimeout *= 3
		}
		actx, cancel := context.WithTimeout(actx, requestTimeout)
		res, err := sc.prov.Fetch(actx, attemptReq)
		cancel()
		if err == nil {
			if aerr := validateAttribution(attemptReq.RequestID, res.RequestID); aerr != nil {
				observability.AnkrResponseAttributionMismatch.WithLabelValues(req.Chain, dataset).Inc()
				return provider.FetchResult{}, nil, aerr
			}
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
			req.PageSize = dec.NextPageSize
			sc.log.Warn("page size reduced on retryable error",
				"error_class", string(pe.Class), "dataset", dataset,
				"page_size", req.PageSize, "err", pe.Message)
			observability.AnkrPageSizeGauge.WithLabelValues(pkey).Set(float64(req.PageSize))
		}
		if dec.ReduceRate && dec.RetryAfter > 0 {
			dec.Backoff = dec.RetryAfter
		}
		backoff := dec.Backoff
		if backoff <= 0 {
			backoff = time.Duration(1<<uint(minInt(attempt, 5)))*time.Second + time.Duration(rand.Int63n(1000))*time.Millisecond
		}
		if !retryBudgetOK(attempt+1, budgetStart, maxAttempts, budget) {
			exhaustedByBudget = true
			break
		}
		select {
		case <-ctx.Done():
			return provider.FetchResult{}, nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
	if exhaustedByBudget {
		observability.AnkrFetchRetryBudgetExhaustedTotal.Inc()
	}
	return provider.FetchResult{}, lastDec, lastErr
}

// rangeCoverageVerdict 范围完备性守卫（BUG-002）。
func rangeCoverageVerdict(sh model.Shard, sawAnyRow, belowSeen, aboveSeen bool, inRangeRows int64, finalPageSaturated bool) error {
	bounded := sh.FromBlock > 0 || sh.ToBlock > 0 || sh.FromTimestamp != nil || sh.ToTimestamp != nil
	if !bounded {
		return nil
	}
	switch sh.Dataset {
	case "transactions", "token_transfers", "nft_transfers":
	default:
		return nil
	}
	if inRangeRows > 0 || (belowSeen && aboveSeen) || !sawAnyRow || !finalPageSaturated {
		return nil
	}
	var span string
	switch {
	case sh.FromBlock > 0 && sh.ToBlock > 0:
		span = fmt.Sprintf("blocks %d..%d", sh.FromBlock, sh.ToBlock)
	case sh.FromBlock > 0:
		span = fmt.Sprintf("blocks >= %d", sh.FromBlock)
	case sh.ToBlock > 0:
		span = fmt.Sprintf("blocks <= %d", sh.ToBlock)
	default:
		span = "requested timestamp range"
	}
	return fmt.Errorf("[range_not_reached] provider pagination ended before covering requested %s (rows seen only on one side of the range and the final page was still full; provider history may be truncated) — refusing silent 0-row completion", span)
}

// filterRows 客户端侧范围过滤：块区间 + 时间区间。
func (sc *Scheduler) filterRows(sh model.Shard, rows []json.RawMessage) ([]json.RawMessage, bool, bool) {
	belowSeen := false
	aboveSeen := false
	out := rows[:0]
	for _, raw := range rows {
		if bn, ok := ingest.RawBlockNumber(raw); ok && bn > 0 {
			if sh.FromBlock > 0 && int64(bn) < sh.FromBlock {
				belowSeen = true
				continue
			}
			if sh.ToBlock > 0 && int64(bn) > sh.ToBlock {
				aboveSeen = true
				continue
			}
		}
		if ts, ok := ingest.RawTimestamp(raw); ok {
			if sh.FromTimestamp != nil && ts.Before(*sh.FromTimestamp) {
				belowSeen = true
				continue
			}
			if sh.ToTimestamp != nil && ts.After(*sh.ToTimestamp) {
				aboveSeen = true
				continue
			}
		}
		out = append(out, raw)
	}
	return out, belowSeen, aboveSeen
}

// ingestRows 旧同步链路：标准化并批量写入 ClickHouse（新引擎由 pipeline 异步消费）。
func (sc *Scheduler) ingestRows(ctx context.Context, sh model.Shard, rows []json.RawMessage, chain, address string) (int64, int64, error) {
	batch, err := sc.buildRows(ctx, sh.Dataset, rows, chain, address, uint64(sh.ToBlock))
	if err != nil {
		return 0, 0, err
	}
	switch sh.Dataset {
	case "transactions":
		if err := sc.writer.WriteTransactions(ctx, batch.txs); err != nil {
			return 0, 0, err
		}
		if len(batch.acts) > 0 {
			if err := sc.writer.WriteActivity(ctx, batch.acts); err != nil {
				return 0, 0, err
			}
		}
	case "token_transfers":
		if err := sc.writer.WriteTokenTransfers(ctx, batch.transfers); err != nil {
			return 0, 0, err
		}
		if len(batch.acts) > 0 {
			if err := sc.writer.WriteActivity(ctx, batch.acts); err != nil {
				return 0, 0, err
			}
		}
	case "nft_transfers":
		if err := sc.writer.WriteNftTransfers(ctx, batch.nfts); err != nil {
			return 0, 0, err
		}
	case "logs":
		if err := sc.writer.WriteLogs(ctx, batch.logRows); err != nil {
			return 0, 0, err
		}
	case "balances":
		if err := sc.writer.WriteBalances(ctx, batch.bals); err != nil {
			return 0, 0, err
		}
	case "internal_transactions":
		if err := sc.writer.WriteInternalTransfers(ctx, batch.internals); err != nil {
			return 0, 0, err
		}
		if len(batch.acts) > 0 {
			if err := sc.writer.WriteActivity(ctx, batch.acts); err != nil {
				return 0, 0, err
			}
		}
	}
	return batch.minB, batch.maxB, nil
}

// resolveMethod 在入库前补齐 Method 轻量字段。
func (sc *Scheduler) resolveMethod(ctx context.Context, chain string, r *ingest.TxRow) {
	if sc.methodResolver == nil {
		return
	}
	res, err := sc.methodResolver.Resolve(ctx, method.MethodResolveRequest{
		Chain: chain, ContractAddress: r.To, MethodID: r.MethodID, Input: r.Input,
		TxHash: r.TxHash, Value: r.ValueRaw, ToIsNull: r.To == "",
	})
	if err != nil {
		sc.log.Debug("method resolve failed", "tx_hash", r.TxHash, "err", err)
		return
	}
	r.ApplyMethod(res)
}

// resolveEvent 事件签名解析。
func (sc *Scheduler) resolveEvent(r *ingest.LogRow) {
	if sc.methodRegistry == nil || len(r.Topics) == 0 {
		return
	}
	if record, ok := sc.methodRegistry.EventSignature(strings.ToLower(r.Topics[0])); ok {
		r.ApplyEvent(record.Name, record.Signature, record.Source, false)
	}
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

// splitShardRange 最小 pageSize 仍反复失败 → 拆成两半；原 Shard 不标 COMPLETE。
func (sc *Scheduler) splitShardRange(ctx context.Context, sh model.Shard) {
	if sh.ToBlock-sh.FromBlock < 2 {
		_, _ = sc.store.FailShard(ctx, sh.ID, "range unsplittable, retry later", true)
		return
	}
	mid := sh.FromBlock + (sh.ToBlock-sh.FromBlock)/2
	base := model.Shard{
		JobID: sh.JobID, SubjectID: sh.SubjectID, DatasetID: sh.DatasetID, Dataset: sh.Dataset,
		FromTimestamp: sh.FromTimestamp, ToTimestamp: sh.ToTimestamp,
		Status: model.ShardPending, Priority: sh.Priority, MaxRetries: sh.MaxRetries,
		PageState: sh.PageState,
	}
	first, second := base, base
	first.FromBlock, first.ToBlock = sh.FromBlock, mid
	second.FromBlock, second.ToBlock = mid+1, sh.ToBlock
	if err := sc.store.CreateShards(ctx, []model.Shard{first, second}); err != nil {
		sc.log.Error("split shard failed", "shard_id", sh.ID, "err", err)
		_, _ = sc.store.FailShard(ctx, sh.ID, "split failed: "+err.Error(), true)
		return
	}
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

// commitCoverage 数据全部持久化 + 校验之后才提交 Coverage（不允许提前标记 COMPLETE，§7）。
func (sc *Scheduler) commitCoverage(ctx context.Context, sh model.Shard, chain, address string, minBlock, maxBlock, rows, bytes int64) {
	if rows == 0 {
		sc.log.Info("coverage skipped (zero rows)", "shard_id", sh.ID, "dataset", sh.Dataset)
		return
	}
	if sh.Dataset == "balances" {
		if err := sc.store.AddCoverage(ctx, chain, address, "balances", sc.sourceProviderForDataset(chain, sh.Dataset), 0, sh.ToBlock, rows, bytes, sh.ToBlock); err != nil {
			sc.log.Error("coverage commit failed", "shard_id", sh.ID, "err", err)
		}
		return
	}
	from, to := sh.FromBlock, sh.ToBlock
	if from == 0 && to == 0 {
		if maxBlock-minBlock >= 2 {
			from, to = minBlock+1, maxBlock-1
		} else {
			sc.log.Info("coverage skipped (boundary only)", "shard_id", sh.ID, "min", minBlock, "max", maxBlock)
			return
		}
	}
	if err := sc.store.AddCoverage(ctx, chain, address, sh.Dataset, sc.sourceProviderForDataset(chain, sh.Dataset), from, to, rows, bytes, sh.ToBlock); err != nil {
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
						if err := sc.meta.SyncFromCoverage(ctx, job.Chain, subj.Subject, status); err != nil {
							sc.log.Error("workspace address stats sync failed", "job_id", sh.JobID, "subject", subj.Subject, "err", err)
						}
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
