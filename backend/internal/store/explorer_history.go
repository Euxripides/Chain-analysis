package store

import (
	"context"
	"fmt"
	"time"
)

// HistoryCoverage is a query policy result. It does not weaken dataset_coverage
// or the coverage contract used by investigations and exports.
type HistoryCoverage struct {
	Dataset         string          `json:"dataset"`
	HistoryComplete bool            `json:"history_complete"`
	Usable          bool            `json:"usable"`
	CompleteThrough *time.Time      `json:"complete_through"`
	Reason          string          `json:"reason"`
	Gaps            []BlockInterval `json:"gaps"`
	CoveredRanges   []BlockInterval `json:"-"`
}

type historyCutoff struct {
	Head int64
	At   time.Time
}

func assessHistory(dataset string, ranges []BlockInterval, cutoffs []historyCutoff, now time.Time) HistoryCoverage {
	result := HistoryCoverage{Dataset: dataset, Reason: "history_unknown", Gaps: []BlockInterval{}}
	merged := MergeBlockRanges(append([]BlockInterval(nil), ranges...))
	result.CoveredRanges = merged
	if len(merged) == 0 {
		return result
	}
	if len(merged) > 1 {
		result.Reason = "historical_gap"
		for i := 1; i < len(merged); i++ {
			result.Gaps = append(result.Gaps, BlockInterval{From: merged[i-1].To + 1, To: merged[i].From - 1})
		}
		return result
	}
	// A full-history request must provide a fixed head/cutoff. Actual first/last
	// transaction timestamps are deliberately never used here.
	var cutoff *time.Time
	for _, c := range cutoffs {
		if c.Head > 0 && !c.At.IsZero() && !c.At.After(now) && merged[0].From == 0 && merged[0].To >= c.Head {
			at := c.At.UTC()
			if cutoff == nil || at.After(*cutoff) {
				cutoff = &at
			}
		}
	}
	if cutoff == nil {
		return result
	}
	result.CompleteThrough = cutoff
	result.HistoryComplete = true
	result.Usable = now.Sub(*cutoff) < 7*24*time.Hour
	if result.Usable {
		result.Reason = "fresh_enough"
	} else {
		result.Reason = "stale_tail"
	}
	return result
}

// ExplorerHistory reads request-bound coverage and successful pagination proof.
// Missing legacy proof stays unknown; COMPLETED alone is insufficient.
func (s *Store) ExplorerHistory(ctx context.Context, chain, address string, datasets []string, now time.Time) ([]HistoryCoverage, bool, error) {
	if chain == "tron" {
		return s.explorerTimeHistory(ctx, chain, address, datasets, now)
	}
	ranges := map[string][]BlockInterval{}
	cutoffs := map[string][]historyCutoff{}
	rows, err := s.Pool.Query(ctx, `
		SELECT dataset,from_block,to_block,row_count FROM dataset_coverage
		WHERE chain=$1 AND subject=$2 AND dataset=ANY($3) AND status='COMPLETE'
		ORDER BY dataset,from_block LIMIT 5001`, chain, address, datasets)
	if err != nil {
		return nil, false, err
	}
	local, count := false, 0
	for rows.Next() {
		var ds string
		var from, to, n int64
		if err := rows.Scan(&ds, &from, &to, &n); err != nil {
			rows.Close()
			return nil, false, err
		}
		count++
		local = local || n > 0
		if from >= 0 && to >= from && to > 0 {
			ranges[ds] = append(ranges[ds], BlockInterval{From: from, To: to})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if count > 5000 {
		return nil, false, fmt.Errorf("coverage evidence exceeds interactive limit")
	}
	// Successful requested shard bounds also prove empty intervals. The terminal
	// page must have no continuation token, all pages must be landed, and their
	// sequence must be contiguous. Split parent shards are excluded explicitly.
	rows, err = s.Pool.Query(ctx, `
		SELECT d.dataset,sh.from_block,sh.to_block,j.head_block,j.resolved_at
		FROM download_subjects sub JOIN download_jobs j ON j.id=sub.job_id
		JOIN download_datasets d ON d.subject_id=sub.id
		JOIN download_shards sh ON sh.dataset_id=d.id
		JOIN LATERAL (
		 SELECT count(*) AS n,min(page_no) AS first,max(page_no) AS last,
		 bool_and(status IN ('INGESTED','VALIDATED') AND ingested_at IS NOT NULL AND raw_sha256<>'') AS landed
		 FROM download_pages WHERE shard_id=sh.id
		) p ON true
		WHERE j.chain=$1 AND sub.subject=$2 AND d.dataset=ANY($3)
		AND d.status='COMPLETED' AND sh.status='COMPLETED'
		AND sh.from_timestamp IS NULL AND sh.to_timestamp IS NULL
		AND sh.to_block>0 AND p.n>0 AND p.first>=0 AND p.first<=1 AND p.last-p.first+1=p.n AND p.landed
		AND EXISTS(SELECT 1 FROM download_pages tail WHERE tail.shard_id=sh.id AND tail.page_no=p.last AND tail.next_page_token_hash='')
		AND j.range_type='full' AND j.head_block>0 AND j.resolved_at IS NOT NULL
		ORDER BY j.resolved_at DESC LIMIT 5001`, chain, address, datasets)
	if err != nil {
		return nil, false, err
	}
	count = 0
	for rows.Next() {
		var ds string
		var from, to, head int64
		var at time.Time
		if err := rows.Scan(&ds, &from, &to, &head, &at); err != nil {
			rows.Close()
			return nil, false, err
		}
		count++
		ranges[ds] = append(ranges[ds], BlockInterval{From: from, To: to})
		cutoffs[ds] = append(cutoffs[ds], historyCutoff{Head: head, At: at})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if count > 5000 {
		return nil, false, fmt.Errorf("pagination evidence exceeds interactive limit")
	}
	// Fully skipped requests may reuse already verified coverage. They still
	// require a resolved full-history boundary, rather than a completion time.
	rows, err = s.Pool.Query(ctx, `SELECT d.dataset,j.head_block,j.resolved_at
	 FROM download_subjects sub JOIN download_jobs j ON j.id=sub.job_id JOIN download_datasets d ON d.subject_id=sub.id
	 WHERE j.chain=$1 AND sub.subject=$2 AND d.dataset=ANY($3) AND j.range_type='full' AND j.head_block>0
	 AND d.status IN ('COMPLETED','SKIPPED') AND j.resolved_at IS NOT NULL ORDER BY j.resolved_at DESC LIMIT 1001`, chain, address, datasets)
	if err != nil {
		return nil, false, err
	}
	count = 0
	for rows.Next() {
		var ds string
		var c historyCutoff
		if err := rows.Scan(&ds, &c.Head, &c.At); err != nil {
			rows.Close()
			return nil, false, err
		}
		count++
		cutoffs[ds] = append(cutoffs[ds], c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if count > 1000 {
		return nil, false, fmt.Errorf("history evidence exceeds interactive limit")
	}
	result := make([]HistoryCoverage, 0, len(datasets))
	for _, ds := range datasets {
		item := assessHistory(ds, ranges[ds], cutoffs[ds], now)
		local = local || item.HistoryComplete
		result = append(result, item)
	}
	return result, local, nil
}
