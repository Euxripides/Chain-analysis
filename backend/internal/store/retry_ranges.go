package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Only the historical resume-removal defect has enough persisted evidence to
// reconstruct a deleted shard. Other empty retries remain rejected.
func restoreRemovedRetryRanges(ctx context.Context, tx pgx.Tx, jobID string) (int64, error) {
	var from, to int64
	var rangeType string
	if err := tx.QueryRow(ctx, `SELECT range_type,COALESCE(from_block,0),COALESCE(to_block,NULLIF(head_block,0),0) FROM download_jobs WHERE id=$1`, jobID).Scan(&rangeType, &from, &to); err != nil {
		return 0, err
	}
	if (rangeType != "full" && rangeType != "block") || to <= from {
		return 0, nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM download_datasets WHERE job_id=$1 AND status='FAILED' AND stats->>'coverage'='PARTIAL' AND stats->>'failure_reason' LIKE 'incomplete shard removed by resume;%' FOR UPDATE`, jobID)
	if err != nil {
		return 0, err
	}
	var datasets []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		datasets = append(datasets, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	var restored int64
	for _, dataset := range datasets {
		rs, err := tx.Query(ctx, `SELECT id,from_block,to_block,status FROM download_shards WHERE dataset_id=$1 ORDER BY from_block,id FOR UPDATE`, dataset)
		if err != nil {
			return 0, err
		}
		var parts []BlockInterval
		var template string
		for rs.Next() {
			var id, status string
			var iv BlockInterval
			if err = rs.Scan(&id, &iv.From, &iv.To, &status); err != nil {
				rs.Close()
				return 0, err
			}
			if status != "COMPLETED" {
				rs.Close()
				return 0, fmt.Errorf("removed-range repair has unfinished shards")
			}
			parts = append(parts, iv)
			template = id
		}
		err = rs.Err()
		rs.Close()
		if err != nil {
			return 0, err
		}
		if template == "" {
			continue
		}
		next := from
		var missing []BlockInterval
		for _, iv := range MergeBlockRanges(parts) {
			if iv.To < next {
				continue
			}
			if iv.From > to {
				break
			}
			if iv.From > next {
				missing = append(missing, BlockInterval{next, iv.From - 1})
			}
			if iv.To >= to {
				next = to + 1
				break
			}
			next = iv.To + 1
		}
		if next <= to {
			missing = append(missing, BlockInterval{next, to})
		}
		if len(missing) > 64 {
			return 0, fmt.Errorf("too many removed ranges for a bounded recovery")
		}
		for _, gap := range missing {
			for _, iv := range splitRetryInterval(gap) {
				_, err = tx.Exec(ctx, `INSERT INTO download_shards(id,job_id,subject_id,dataset_id,dataset,from_block,to_block,status,priority,max_retries,page_size,provider_key,api_channel,operation_class,routing_policy_key,routing_policy_revision,profile_revision,page_state)
 SELECT $1,job_id,subject_id,dataset_id,dataset,$2,$3,'PENDING',priority,max_retries,page_size,provider_key,api_channel,operation_class,routing_policy_key,routing_policy_revision,profile_revision,'{"restored_removed_range":true}'::jsonb FROM download_shards WHERE id=$4`, uuid.NewString(), iv.From, iv.To, template)
				if err != nil {
					return 0, err
				}
				restored++
			}
		}
		if len(missing) > 0 {
			_, err = tx.Exec(ctx, `UPDATE download_datasets SET stats=stats||'{"removed_range_recovery":true}'::jsonb WHERE id=$1`, dataset)
			if err != nil {
				return 0, err
			}
		}
	}
	return restored, nil
}

func removedRangeRecovered(stats map[string]any, status string) bool {
	recovered, _ := stats["removed_range_recovery"].(bool)
	reason, _ := stats["failure_reason"].(string)
	return recovered && status == "COMPLETED" && strings.HasPrefix(reason, "incomplete shard removed by resume;")
}

// Fresh, disjoint block ranges need no cursor translation or extra count calls.
// Only the restored gap is parallelized; successful ranges retain their pages.
func splitRetryInterval(iv BlockInterval) []BlockInterval {
	const minWindow = int64(1000000)
	n := int((iv.To - iv.From + 1) / minWindow)
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	var out []BlockInterval
	start := iv.From
	remaining := iv.To - iv.From + 1
	for i := 0; i < n; i++ {
		size := remaining / int64(n-i)
		end := start + size - 1
		out = append(out, BlockInterval{start, end})
		start = end + 1
		remaining -= size
	}
	return out
}
