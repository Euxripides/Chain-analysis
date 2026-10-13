package download

import (
	"context"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"time"
)

type densityPage struct {
	from, to int64
	rows     uint64
}

// Existing validated page metadata is a workload hint, never coverage truth.
// Read one recent populated dataset with a bounded query. This does not scan
// ClickHouse facts or call count providers, even for a cold address.
func (p *Planner) localRangeDensity(ctx context.Context, job *model.DownloadJob, subj model.Subject, item model.DatasetItem, gaps []store.BlockInterval, base int) (int64, []int64, int) {
	if p.store == nil || job.Source == "benchmark" || job.Chain == "tron" || job.Chain == "solana" || len(gaps) != 1 {
		return -1, nil, base
	}
	switch item.Dataset {
	case "transactions", "token_transfers", "logs":
	default:
		return -1, nil, base
	}
	queryCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	results, err := p.store.Pool.Query(queryCtx, `WITH recent AS (
 SELECT d.id FROM download_datasets d JOIN download_jobs j ON j.id=d.job_id JOIN download_subjects sub ON sub.id=d.subject_id
 WHERE j.chain=$1 AND sub.subject=$2 AND d.dataset=$3 AND d.status='COMPLETED' AND j.status='COMPLETED'
 AND EXISTS(SELECT 1 FROM download_pages pg WHERE pg.job_id=j.id AND pg.dataset=d.dataset AND pg.status='VALIDATED' AND pg.rows_fetched>0)
 ORDER BY d.updated_at DESC LIMIT 1)
 SELECT pg.from_block,pg.to_block,pg.rows_fetched FROM recent
 JOIN download_shards sh ON sh.dataset_id=recent.id JOIN download_pages pg ON pg.shard_id=sh.id
 WHERE pg.status='VALIDATED' AND pg.rows_fetched>0 AND pg.from_block>0 AND pg.to_block>=pg.from_block
 AND pg.to_block >= $4 AND pg.from_block <= $5
 ORDER BY pg.from_block,pg.to_block LIMIT 4096`, job.Chain, subj.Subject, item.Dataset, gaps[0].From, gaps[0].To)
	if err != nil {
		return -1, nil, base
	}
	defer results.Close()
	pages := []densityPage{}
	for results.Next() {
		var page densityPage
		if results.Scan(&page.from, &page.to, &page.rows) != nil {
			return -1, nil, base
		}
		page.from = max64(page.from, gaps[0].From)
		page.to = min64(page.to, gaps[0].To)
		pages = append(pages, page)
	}
	if results.Err() != nil {
		return -1, nil, base
	}
	pageSize := p.preferredPageSize(queryCtx, job.Chain, item.Dataset)
	first, bounds, n := densityPlan(pages, base, pageSize, string(job.EffectiveMode))
	if first >= 0 {
		p.log.Info("local page density plan", "job_id", job.ID, "dataset", item.Dataset, "pages", len(pages), "ranges", n)
	}
	return first, bounds, n
}

func densityPlan(pages []densityPage, base, pageSize int, mode string) (int64, []int64, int) {
	var total uint64
	for _, page := range pages {
		total += page.rows
	}
	if len(pages) == 0 || total <= uint64(maxInt(pageSize, 1)) {
		return -1, nil, base
	}
	n := localDensityShardCount(base, total, pageSize, mode)
	bounds := make([]int64, 0, n-1)
	var seen uint64
	index := 0
	for i := 1; i < n; i++ {
		target := total * uint64(i) / uint64(n)
		for index+1 < len(pages) && seen+pages[index].rows < target {
			seen += pages[index].rows
			index++
		}
		page := pages[index]
		boundary := page.from
		if page.rows > 0 && target > seen {
			boundary += int64(float64(page.to-page.from) * float64(target-seen) / float64(page.rows))
		}
		if len(bounds) == 0 || boundary > bounds[len(bounds)-1] {
			bounds = append(bounds, boundary)
		}
	}
	return pages[0].from, bounds, n
}

func localDensityShardCount(base int, rows uint64, pageSize int, mode string) int {
	if pageSize <= 0 {
		pageSize = 10000
	}
	pages := (rows + uint64(pageSize) - 1) / uint64(pageSize)
	n := int((pages + 7) / 8)
	cap := 4
	switch mode {
	case "safe", "economy":
		cap = 2
	case "turbo":
		cap = 8
	}
	if cap < base {
		cap = base
	}
	return maxInt(base, minInt(cap, n))
}
