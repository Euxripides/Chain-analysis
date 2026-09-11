CREATE TABLE IF NOT EXISTS address_dataset_sync_state (
    chain TEXT NOT NULL,
    subject TEXT NOT NULL,
    dataset TEXT NOT NULL,
    complete_from TIMESTAMPTZ NOT NULL,
    complete_through TIMESTAMPTZ NOT NULL,
    source_job_id UUID REFERENCES download_jobs(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, subject, dataset),
    CHECK (complete_through >= complete_from)
);

-- Seed trustworthy watermarks from historical full jobs whose dataset and job
-- both reached COMPLETED. Shard time windows represent scanned empty periods as
-- well as periods containing rows, so MAX(to_timestamp) is the safe tail cursor.
INSERT INTO address_dataset_sync_state
    (chain,subject,dataset,complete_from,complete_through,source_job_id,updated_at)
SELECT DISTINCT ON (j.chain,ds.subject,d.dataset)
    j.chain,ds.subject,d.dataset,
    MIN(sh.from_timestamp) OVER scope,
    MAX(sh.to_timestamp) OVER scope,
    j.id,now()
FROM download_jobs j
JOIN download_subjects ds ON ds.job_id=j.id
JOIN download_datasets d ON d.job_id=j.id AND d.subject_id=ds.id
JOIN download_shards sh ON sh.dataset_id=d.id
WHERE j.range_type='full' AND j.status='COMPLETED' AND d.status='COMPLETED'
  AND sh.status='COMPLETED' AND sh.from_timestamp IS NOT NULL AND sh.to_timestamp IS NOT NULL
WINDOW scope AS (PARTITION BY j.id,ds.id,d.id)
ORDER BY j.chain,ds.subject,d.dataset,j.created_at DESC
ON CONFLICT(chain,subject,dataset) DO UPDATE SET
    complete_from=LEAST(address_dataset_sync_state.complete_from,EXCLUDED.complete_from),
    complete_through=GREATEST(address_dataset_sync_state.complete_through,EXCLUDED.complete_through),
    source_job_id=EXCLUDED.source_job_id,
    updated_at=now();
