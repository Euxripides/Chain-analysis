package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type AddressDatasetSyncState struct {
	CompleteFrom    time.Time
	CompleteThrough time.Time
}

func (s *Store) GetAddressDatasetSyncState(ctx context.Context, chain, subject, dataset string) (*AddressDatasetSyncState, error) {
	state := &AddressDatasetSyncState{}
	err := s.Pool.QueryRow(ctx, `SELECT complete_from,complete_through
		FROM address_dataset_sync_state WHERE chain=$1 AND subject=$2 AND dataset=$3`, chain, subject, dataset).
		Scan(&state.CompleteFrom, &state.CompleteThrough)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return state, err
}

func (s *Store) recordCompletedFullSync(ctx context.Context, datasetID string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO address_dataset_sync_state
		(chain,subject,dataset,complete_from,complete_through,source_job_id,updated_at)
		SELECT j.chain,ds.subject,d.dataset,MIN(sh.from_timestamp),MAX(sh.to_timestamp),j.id,now()
		FROM download_datasets d
		JOIN download_jobs j ON j.id=d.job_id
		JOIN download_subjects ds ON ds.id=d.subject_id
		JOIN download_shards sh ON sh.dataset_id=d.id
		WHERE d.id=$1 AND j.range_type='full' AND sh.from_timestamp IS NOT NULL AND sh.to_timestamp IS NOT NULL
		GROUP BY j.chain,ds.subject,d.dataset,j.id
		ON CONFLICT(chain,subject,dataset) DO UPDATE SET
			complete_from=LEAST(address_dataset_sync_state.complete_from,EXCLUDED.complete_from),
			complete_through=GREATEST(address_dataset_sync_state.complete_through,EXCLUDED.complete_through),
			source_job_id=EXCLUDED.source_job_id,updated_at=now()`, datasetID)
	return err
}
