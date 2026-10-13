package store

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type historyTimeRange struct{ From, To, Cutoff time.Time }

func assessTimeHistory(ds string, anchor time.Time, ranges []historyTimeRange, now time.Time) HistoryCoverage {
	result := HistoryCoverage{Dataset: ds, Reason: "history_unknown", Gaps: []BlockInterval{}}
	if anchor.IsZero() || len(ranges) == 0 {
		return result
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].From.Before(ranges[j].From) })
	if ranges[0].From.After(anchor) {
		return result
	}
	end := ranges[0].To
	through := ranges[0].Cutoff
	for _, r := range ranges[1:] {
		if r.From.After(end.Add(time.Millisecond)) {
			result.Reason = "historical_gap"
			return result
		}
		if r.To.After(end) {
			end = r.To
		}
		if r.Cutoff.After(through) {
			through = r.Cutoff
		}
	}
	if through.IsZero() || through.After(now) || end.Before(through) {
		return result
	}
	result.CompleteThrough = &through
	result.HistoryComplete = true
	result.Usable = now.Sub(through) < 7*24*time.Hour
	if result.Usable {
		result.Reason = "fresh_enough"
	} else {
		result.Reason = "stale_tail"
	}
	return result
}

// TRON uses native timestamp request ranges. Require the durable full-sync
// anchor AND continuous landed page evidence; never just trust MIN/MAX dates.
func (s *Store) explorerTimeHistory(ctx context.Context, chain, address string, datasets []string, now time.Time) ([]HistoryCoverage, bool, error) {
	ranges := map[string][]historyTimeRange{}
	anchors := map[string]time.Time{}
	rows, err := s.Pool.Query(ctx, `SELECT dataset,complete_from FROM address_dataset_sync_state WHERE chain=$1 AND subject=$2 AND dataset=ANY($3)`, chain, address, datasets)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var ds string
		var at time.Time
		if err := rows.Scan(&ds, &at); err != nil {
			rows.Close()
			return nil, false, err
		}
		anchors[ds] = at
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT d.dataset,sh.from_timestamp,sh.to_timestamp,LEAST(sh.to_timestamp,j.resolved_at),sh.downloaded_rows
	 FROM download_subjects sub JOIN download_jobs j ON j.id=sub.job_id JOIN download_datasets d ON d.subject_id=sub.id
	 JOIN download_shards sh ON sh.dataset_id=d.id
	 JOIN LATERAL(SELECT count(*) n,min(page_no) first,max(page_no) last,
	 bool_and(status IN ('INGESTED','VALIDATED') AND ingested_at IS NOT NULL AND raw_sha256<>'') landed FROM download_pages WHERE shard_id=sh.id) p ON true
	 WHERE j.chain=$1 AND sub.subject=$2 AND d.dataset=ANY($3) AND d.status='COMPLETED' AND sh.status='COMPLETED'
	 AND j.range_type='full' AND j.resolved_at IS NOT NULL AND sh.from_timestamp IS NOT NULL AND sh.to_timestamp IS NOT NULL
	 AND p.n>0 AND p.first>=0 AND p.first<=1 AND p.last-p.first+1=p.n AND p.landed
	 AND EXISTS(SELECT 1 FROM download_pages tail WHERE tail.shard_id=sh.id AND tail.page_no=p.last AND tail.next_page_token_hash='')
	 ORDER BY sh.from_timestamp LIMIT 5001`, chain, address, datasets)
	if err != nil {
		return nil, false, err
	}
	local := false
	count := 0
	for rows.Next() {
		var ds string
		var r historyTimeRange
		var n int64
		if err := rows.Scan(&ds, &r.From, &r.To, &r.Cutoff, &n); err != nil {
			rows.Close()
			return nil, false, err
		}
		count++
		local = local || n > 0
		ranges[ds] = append(ranges[ds], r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if count > 5000 {
		return nil, false, fmt.Errorf("time coverage evidence exceeds interactive limit")
	}
	result := make([]HistoryCoverage, 0, len(datasets))
	for _, ds := range datasets {
		item := assessTimeHistory(ds, anchors[ds], ranges[ds], now)
		local = local || item.HistoryComplete
		result = append(result, item)
	}
	return result, local, nil
}
