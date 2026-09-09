// V1.1 Benchmark API（§45/§46）：POST /api/v1/admin/ankr/benchmarks 创建并触发固定 target 实测；
// GET 查询单次与对比数据（前端 Compare UI）。
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

type benchmarkCreateRequest struct {
	Chain       string `json:"chain"`
	Dataset     string `json:"dataset"`
	Address     string `json:"address"`
	TargetBlock int64  `json:"target_block"`
	ShardCount  int    `json:"shard_count"`
	PageSize    int    `json:"page_size"`
}

func addressHash(addr string) string {
	sum := sha256.Sum256([]byte(addr))
	return hex.EncodeToString(sum[:])[:16]
}

func (s *Server) handleBenchmarkCreate(w http.ResponseWriter, r *http.Request) {
	var req benchmarkCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request: " + err.Error()})
		return
	}
	if req.Chain == "" || req.Dataset == "" || req.Address == "" || req.TargetBlock <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "chain/dataset/address/target_block required"})
		return
	}
	if req.ShardCount < 0 || req.ShardCount > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "shard_count must be 0(adaptive)..256"})
		return
	}
	if req.PageSize <= 0 {
		req.PageSize = 10000
	}
	switch req.Dataset {
	case "transactions", "token_transfers", "logs":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "benchmark supports transactions/token_transfers/logs only"})
		return
	}
	// 固定 target_block 的 block Range 任务（§7：所有 A/B 同 target）
	from := int64(0)
	stats := map[string]any{}
	if req.ShardCount > 0 {
		stats["benchmark_shard_count"] = float64(req.ShardCount)
	} // shard_count=0 → Adaptive（§13 Case E：Profile 学习后的自适应分片）
	day := time.Now()
	n, nerr := s.deps.Store.CountJobNoForDay(r.Context(), day)
	if nerr != nil {
		n = 0
	}
	jobNo := fmt.Sprintf("DL-%s-%04d", day.Format("20060102"), n+1)
	// 数据真实跨度提示（密度感知切分，§12/§17）：空前缀单独成 Shard
	if first, last, err := s.benchmarkDataSpan(r.Context(), req.Chain, req.Dataset, req.Address); err == nil && first > 0 {
		stats["data_first_block"] = float64(first)
		stats["data_last_block"] = float64(last)
	}
	// 分位数边界（按行数等分 Range，§17 工作量均衡；P2 密度边界前移）
	if req.ShardCount > 1 {
		if bounds, err := s.benchmarkQuantileBoundaries(r.Context(), req.Chain, req.Dataset, req.Address, req.ShardCount); err == nil && len(bounds) > 0 {
			vals := make([]any, len(bounds))
			for i, b := range bounds {
				vals[i] = float64(b)
			}
			stats["data_boundaries"] = vals
		}
	}
	job := &model.DownloadJob{
		JobNo:  jobNo,
		Status: model.JobCreated, Priority: 130, Source: "benchmark", Mode: "benchmark",
		Chain: req.Chain, RangeType: "block", FromBlock: &from, ToBlock: &req.TargetBlock,
		Datasets: []string{req.Dataset}, ReusePolicy: "refresh", HeadBlock: req.TargetBlock,
		Stats: stats,
	}
	// Count-then-insert is only a hint; concurrent benchmark submissions can
	// still choose the same human-readable job number.  Retry the unique-key
	// collision with the next suffix so the admin API remains usable under the
	// intended parallel harness load.
	var createErr error
	for attempt := 0; attempt < 8; attempt++ {
		if attempt > 0 {
			job.JobNo = fmt.Sprintf("DL-%s-%04d", day.Format("20060102"), n+1+attempt)
		}
		if _, createErr = s.deps.Store.CreateJobWithSubjects(r.Context(), job, []string{req.Address}); createErr == nil {
			break
		}
		if !strings.Contains(createErr.Error(), "job_no_key") {
			break
		}
	}
	if createErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create job: " + createErr.Error()})
		return
	}
	bench := &model.BenchmarkRun{
		Chain: req.Chain, Dataset: req.Dataset, AddressHash: addressHash(req.Address),
		TargetBlock: req.TargetBlock, ShardCount: req.ShardCount, PageSize: req.PageSize,
		JobID: job.ID, Status: "RUNNING",
	}
	id, err := s.deps.Store.CreateBenchmarkRun(r.Context(), bench)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create benchmark: " + err.Error()})
		return
	}
	s.deps.Store.InsertAudit(r.Context(), nil, "admin.ankr.benchmark", "ankr_benchmark_runs", id, map[string]any{
		"chain": req.Chain, "dataset": req.Dataset, "shard_count": req.ShardCount, "target_block": req.TargetBlock,
	})
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "running", "benchmark_id": id, "job_id": job.ID,
		"chain": req.Chain, "dataset": req.Dataset, "shard_count": req.ShardCount, "target_block": req.TargetBlock,
	})
}

// benchmarkQuantileBoundaries 按行数分位数取边界（n-1 个分位点 → n 个等行量 Range，§17）。
func (s *Server) benchmarkQuantileBoundaries(ctx context.Context, chain, dataset, address string, n int) ([]uint64, error) {
	if n < 2 || n > 256 {
		return nil, errors.New("n must be 2..256")
	}
	var table, cond string
	args := []any{chain, address}
	switch dataset {
	case "transactions", "token_transfers":
		table = dataset
		cond = "(from_address = ? OR to_address = ?)"
		args = append(args, address)
	case "logs":
		table = "logs"
		cond = "address = ?"
	default:
		return nil, errors.New("unsupported dataset")
	}
	qs := make([]string, 0, n-1)
	for i := 1; i < n; i++ {
		qs = append(qs, fmt.Sprintf("%.6f", float64(i)/float64(n)))
	}
	q := "SELECT quantilesExact(" + strings.Join(qs, ",") + ")(block_number) FROM blockchain." + table + " WHERE chain = ? AND " + cond
	var bounds []uint64
	if err := s.deps.CH.DB().QueryRow(ctx, q, args...).Scan(&bounds); err != nil {
		return nil, err
	}
	return bounds, nil
}

// benchmarkDataSpan 查询该地址在 CH 中的真实数据跨度（V1.1 §12 密度感知切分提示）。
func (s *Server) benchmarkDataSpan(ctx context.Context, chain, dataset, address string) (int64, int64, error) {
	var q string
	switch dataset {
	case "transactions", "token_transfers":
		q = "SELECT COALESCE(min(block_number),0), COALESCE(max(block_number),0) FROM blockchain." + dataset + " WHERE chain = ? AND (from_address = ? OR to_address = ?)"
	case "logs":
		q = "SELECT COALESCE(min(block_number),0), COALESCE(max(block_number),0) FROM blockchain.logs WHERE chain = ? AND address = ?"
	default:
		return 0, 0, errors.New("unsupported dataset for span")
	}
	spanArgs := []any{chain, address}
	if dataset == "transactions" || dataset == "token_transfers" {
		spanArgs = append(spanArgs, address) // WHERE 中 address 出现两次（from/to）
	}
	var minB, maxB uint64
	if err := s.deps.CH.DB().QueryRow(ctx, q, spanArgs...).Scan(&minB, &maxB); err != nil {
		return 0, 0, err
	}
	return int64(minB), int64(maxB), nil
}

func (s *Server) handleBenchmarkGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	b, err := s.deps.Store.GetBenchmarkRun(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "benchmark not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleBenchmarkList(w http.ResponseWriter, r *http.Request) {
	chain := r.URL.Query().Get("chain")
	dataset := r.URL.Query().Get("dataset")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := s.deps.Store.ListBenchmarkRuns(r.Context(), chain, dataset, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"benchmarks": list})
}
