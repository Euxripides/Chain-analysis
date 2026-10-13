package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// Claim every format in one transaction. A second API process can never take
// half of the group and make the shared scan publish a mixed data version.
func (s *Server) claimExportPreparationGroup(ctx context.Context) ([]exportPreparation, error) {
	tx, err := s.deps.Store.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var jobID string
	var version time.Time
	err = tx.QueryRow(ctx, `
		SELECT p.job_id::text, p.data_version
		FROM export_preparations p JOIN download_jobs j ON j.id=p.job_id
		WHERE j.status='COMPLETED' AND j.source='user_download'
		  AND p.format IN ('csv','jsonl','parquet','xlsx')
		  AND (SELECT COUNT(*) FROM download_subjects sub WHERE sub.job_id=j.id)=1
		  AND (SELECT COALESCE(SUM(bytes),0) FROM export_preparations WHERE status='READY') < $1
		GROUP BY p.job_id,p.data_version
		HAVING COUNT(*)=4 AND bool_and(p.attempts<3 AND
		  (p.status='PENDING' OR
		   (p.status='FAILED' AND p.retry_after<=now()) OR
		   (p.status='RUNNING' AND p.lease_until<now())))
		ORDER BY MIN(p.updated_at),p.job_id LIMIT 1`, maxPreparedExportBytes).Scan(&jobID, &version)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT format, attempts FROM export_preparations
		WHERE job_id=$1 AND data_version=$2
		  AND format IN ('csv','jsonl','parquet','xlsx') ORDER BY format
		FOR UPDATE SKIP LOCKED`, jobID, version)
	if err != nil {
		return nil, err
	}
	group := make([]exportPreparation, 0, 4)
	for rows.Next() {
		var item exportPreparation
		item.jobID, item.dataVersion = jobID, version
		if err := rows.Scan(&item.format, &item.attempt); err != nil {
			rows.Close()
			return nil, err
		}
		group = append(group, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(group) != 4 {
		return nil, pgx.ErrNoRows
	}
	priority := map[string]int{"xlsx": 0, "parquet": 1, "csv": 2, "jsonl": 3}
	sort.SliceStable(group, func(i, j int) bool {
		return priority[group[i].format] < priority[group[j].format]
	})
	for i := range group {
		group[i].attempt++
		tag, err := tx.Exec(ctx, `
			UPDATE export_preparations SET status='RUNNING',attempts=$4,
			  lease_until=now()+interval '2 minutes', retry_after=NULL,
			  error='',updated_at=now()
			WHERE job_id=$1 AND format=$2 AND data_version=$3`,
			jobID, group[i].format, version, group[i].attempt)
		if err != nil {
			return nil, fmt.Errorf("claim grouped format %s: %w", group[i].format, err)
		}
		if tag.RowsAffected() != 1 {
			return nil, fmt.Errorf("claim grouped format %s: row changed", group[i].format)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return group, nil
}

func (s *Server) prepareExportGroup(parent context.Context, group []exportPreparation) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, p := range group {
					// A format may already be READY while another upload runs.
					tag, err := s.deps.Store.Pool.Exec(ctx, `
						UPDATE export_preparations SET lease_until=now()+interval '2 minutes'
						WHERE job_id=$1 AND format=$2 AND data_version=$3
						  AND status='RUNNING' AND attempts=$4`,
						p.jobID, p.format, p.dataVersion, p.attempt)
					if err != nil {
						cancel()
						return
					}
					if tag.RowsAffected() == 0 {
						var status string
						var attempt int
						readErr := s.deps.Store.Pool.QueryRow(ctx, `
							SELECT status,attempts FROM export_preparations
							WHERE job_id=$1 AND format=$2 AND data_version=$3`,
							p.jobID, p.format, p.dataVersion).Scan(&status, &attempt)
						if readErr != nil || status != "READY" || attempt != p.attempt {
							cancel()
							return
						}
					}
				}
			}
		}
	}()
	err := s.buildPreparedExportGroup(ctx, group)
	// Release an in-flight heartbeat query as well as its select loop before
	// joining it; a blocked database call cannot observe stopHeartbeat.
	cancel()
	close(stopHeartbeat)
	<-heartbeatDone
	if parent.Err() != nil {
		return
	}
	if err == nil {
		return
	}
	if s.deps.Log != nil {
		s.deps.Log.Warn("grouped export preparation failed", "job_id", group[0].jobID, "error", err)
	}
	for _, p := range group {
		backoff := time.Duration(15*p.attempt*p.attempt) * time.Second
		_, _ = s.deps.Store.Pool.Exec(parent, `
			UPDATE export_preparations SET status='FAILED', lease_until=NULL,
			  retry_after=now()+$5::interval,error=$6,updated_at=now()
			WHERE job_id=$1 AND format=$2 AND data_version=$3
			  AND status='RUNNING' AND attempts=$4`,
			p.jobID, p.format, p.dataVersion, p.attempt,
			fmt.Sprintf("%d seconds", int(backoff.Seconds())), err.Error())
	}
}

func (s *Server) buildPreparedExportGroup(ctx context.Context, group []exportPreparation) error {
	if len(group) == 0 {
		return errors.New("empty export preparation group")
	}
	job, err := s.deps.Store.GetJob(ctx, group[0].jobID)
	if err != nil {
		return err
	}
	if job.Status != model.JobCompleted || !s.jobExportVersion(ctx, job).Equal(group[0].dataVersion) {
		return fmt.Errorf("job version changed during grouped preparation")
	}
	if job.OrganizationID == nil || job.WorkspaceID == nil {
		return fmt.Errorf("job lacks tenant scope")
	}
	subjects, err := s.deps.Store.SubjectsByJob(ctx, job.ID)
	if err != nil {
		return err
	}
	addresses := make([]string, 0, len(subjects))
	for _, sub := range subjects {
		addresses = append(addresses, sub.Subject)
	}
	addresses, err = dataset.NormalizeAndDedupeAddressesForChain(job.Chain, addresses)
	if err != nil {
		return err
	}
	if len(addresses) != 1 {
		return fmt.Errorf("grouped preparation requires one address, got %d", len(addresses))
	}
	if len(job.Datasets) == 0 {
		return fmt.Errorf("grouped preparation requires at least one dataset")
	}
	parts := make([]preparedDatasetParts, 0, len(job.Datasets))
	defer func() { removePreparedDatasetParts(parts) }()
	for _, ds := range job.Datasets {
		part, err := s.buildSharedFormatParts(ctx, job, job.Chain, *job.WorkspaceID, addresses[0], ds)
		if err != nil {
			return err
		}
		parts = append(parts, part)
	}
	if err := runPreparedExportFormats(ctx, group, parts,
		func(ctx context.Context, shared []preparedDatasetParts) ([]preparedDatasetParts, error) {
			started := time.Now()
			xlsx, err := buildPreparedXLSXParts(ctx, shared)
			if err == nil && s.deps.Log != nil {
				s.deps.Log.Info("prepared xlsx parts encoded", "job_id", job.ID,
					"datasets", len(shared), "xlsx_ms", time.Since(started).Milliseconds())
			}
			return xlsx, err
		},
		func(ctx context.Context, p exportPreparation, ready []preparedDatasetParts) error {
			return s.publishPreparedExportFormat(ctx, p, job, addresses[0], ready)
		}); err != nil {
		return err
	}
	jobAfter, err := s.deps.Store.GetJob(ctx, job.ID)
	if err != nil || jobAfter.Status != model.JobCompleted || !s.jobExportVersion(ctx, jobAfter).Equal(group[0].dataVersion) {
		return errors.New("job changed before grouped exports became ready")
	}
	return nil
}

// All formats consume the same completed scan. XLSX encoding runs once without
// holding an upload slot; already encoded formats can publish immediately.
// A format-local error does not cancel its siblings. Parent cancellation still
// reaches every operation, and Wait keeps the shared inputs alive until all
// consumers have stopped. Only this function owns the derived XLSX files.
func runPreparedExportFormats(ctx context.Context, group []exportPreparation, shared []preparedDatasetParts,
	buildXLSX func(context.Context, []preparedDatasetParts) ([]preparedDatasetParts, error),
	publish func(context.Context, exportPreparation, []preparedDatasetParts) error) error {
	seen := make(map[string]bool, len(group))
	for _, p := range group {
		switch p.format {
		case "csv", "jsonl", "parquet", "xlsx":
		default:
			return fmt.Errorf("unsupported prepared format %q", p.format)
		}
		if seen[p.format] {
			return fmt.Errorf("duplicate prepared format %q", p.format)
		}
		seen[p.format] = true
	}
	var work errgroup.Group
	uploads := make(chan struct{}, 2)
	for _, p := range group {
		p := p
		work.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			ready := shared
			if p.format == "xlsx" {
				var err error
				ready, err = buildXLSX(ctx, shared)
				if err != nil {
					return fmt.Errorf("prepare xlsx: %w", err)
				}
				defer removePreparedDatasetParts(ready)
			}
			select {
			case uploads <- struct{}{}:
				defer func() { <-uploads }()
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := publish(ctx, p, ready); err != nil {
				return fmt.Errorf("publish %s: %w", p.format, err)
			}
			return nil
		})
	}
	return work.Wait()
}

func (s *Server) publishPreparedExportFormat(ctx context.Context, p exportPreparation,
	job *model.DownloadJob, address string, parts []preparedDatasetParts) error {
	started := time.Now()
	zipIt := len(job.Datasets) > 1
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkPreparedExportClaim(ctx, p); err != nil {
		return err
	}
	if current, err := s.deps.Store.GetJob(ctx, job.ID); err != nil ||
		current.Status != model.JobCompleted || !s.jobExportVersion(ctx, current).Equal(p.dataVersion) {
		return errors.New("job changed during grouped export upload")
	}
	snapshot, err := json.Marshal(map[string]any{
		"chain": job.Chain, "addresses": []string{address}, "datasets": job.Datasets,
		"format": p.format, "include_meta": false, "zip": zipIt,
		"include_decoded_input": false, "job_id": job.ID,
		"partial": false, "job_status": model.JobCompleted, "file_layout": "dataset",
	})
	if err != nil {
		return err
	}
	exp, err := s.deps.Store.CreateExportV2(ctx, *job.WorkspaceID, *job.OrganizationID,
		nil, &job.ID, strings.Join(job.Datasets, "+"), p.format, zipIt, snapshot)
	if err != nil {
		return err
	}
	if err := s.deps.Store.UpdateExportV2(ctx, exp.ID, "RUNNING", "", "", 0, ""); err != nil {
		return err
	}
	rows, sha, loc, err := s.uploadAllFormatParts(ctx, exp, job, address, p.format, parts)
	if err != nil {
		_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "FAILED", "", "", 0, err.Error())
		return err
	}
	if current, err := s.deps.Store.GetJob(ctx, job.ID); err != nil ||
		current.Status != model.JobCompleted || !s.jobExportVersion(ctx, current).Equal(p.dataVersion) {
		_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "FAILED", "", "", 0, "job changed during upload")
		return errors.New("job changed during grouped export upload")
	}
	size, _, err := s.statExportObject(ctx, loc.Key)
	if err != nil {
		_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "FAILED", "", "", 0, err.Error())
		return err
	}
	if err := s.completePreparedExport(ctx, p, exp.ID, loc.Key, sha, rows, size); err != nil {
		// Do not overwrite a committed completion if the connection failed
		// after COMMIT reached the server but before its reply was received.
		_, _ = s.deps.Store.Pool.Exec(ctx, `UPDATE exports SET status='FAILED',error=$2,
			finished_at=now(),updated_at=now() WHERE id=$1 AND status='RUNNING'`, exp.ID, err.Error())
		return err
	}
	detail := map[string]any{
		"chain": job.Chain, "addresses": 1, "datasets": job.Datasets,
		"format": p.format, "zip": zipIt, "rows": rows,
	}
	for k, v := range loc.auditFields() {
		detail[k] = v
	}
	s.deps.Store.InsertAudit(ctx, nil, "export.completed", "export", exp.ID, detail)
	s.writeAuditCtx(ctx, "export.completed", "export", "export", exp.ID, detail)
	if s.deps.Log != nil {
		s.deps.Log.Info("prepared export format ready", "job_id", job.ID,
			"format", p.format, "rows", rows, "bytes", size,
			"elapsed_ms", time.Since(started).Milliseconds())
	}
	return nil
}

var errExportPreparationLost = errors.New("export preparation claim changed or expired")

func (s *Server) checkPreparedExportClaim(ctx context.Context, p exportPreparation) error {
	var valid bool
	err := s.deps.Store.Pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM export_preparations p JOIN download_jobs j ON j.id=p.job_id
		WHERE p.job_id=$1 AND p.format=$2 AND p.data_version=$3
		  AND p.status='RUNNING' AND p.attempts=$4 AND p.lease_until>clock_timestamp()
		  AND j.status='COMPLETED')`, p.jobID, p.format, p.dataVersion, p.attempt).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return errExportPreparationLost
	}
	return nil
}

// Publish the export record and its preparation together after verification.
// Lock the job first, matching completion-trigger order, then the preparation;
// a retry/lease takeover cannot interleave a stale READY with a new data version.
func (s *Server) completePreparedExport(ctx context.Context, p exportPreparation,
	exportID, objectRef, sha string, rows, size int64) error {
	tx, err := s.deps.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var id string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM download_jobs
		WHERE id=$1 AND status='COMPLETED' FOR UPDATE`, p.jobID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errExportPreparationLost
		}
		return fmt.Errorf("publish job: %w", err)
	}
	var owned int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM export_preparations
		WHERE job_id=$1 AND format=$2 AND data_version=$3 AND status='RUNNING'
		  AND attempts=$4 AND lease_until>clock_timestamp() FOR UPDATE`,
		p.jobID, p.format, p.dataVersion, p.attempt).Scan(&owned); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errExportPreparationLost
		}
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE exports SET status='COMPLETED',object_ref=$2,
		sha256=$3,rows=$4,error='',finished_at=now(),updated_at=now()
		WHERE id=$1 AND job_id=$5 AND format=$6 AND status='RUNNING'`,
		exportID, objectRef, sha, rows, p.jobID, p.format)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("export record changed before completion")
	}
	tag, err = tx.Exec(ctx, `UPDATE export_preparations SET status='READY',export_id=$5,
		bytes=$6,lease_until=NULL,retry_after=NULL,error='',updated_at=now()
		WHERE job_id=$1 AND format=$2 AND data_version=$3 AND status='RUNNING'
		  AND attempts=$4 AND lease_until>clock_timestamp()`,
		p.jobID, p.format, p.dataVersion, p.attempt, exportID, size)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errExportPreparationLost
	}
	return tx.Commit(ctx)
}
