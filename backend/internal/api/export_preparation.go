package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// A completed user download is prepared before the Export button becomes
// available. PostgreSQL owns the work queue so an API restart cannot lose it.
type exportPreparation struct {
	jobID       string
	format      string
	dataVersion time.Time
	attempt     int
}

// A completion transition, rather than a later stats update, seals the data
// version. The trigger replaces this value when a retried job completes again.
func (s *Server) jobExportVersion(ctx context.Context, job *model.DownloadJob) time.Time {
	if job == nil {
		return time.Time{}
	}
	if job.Status != model.JobCompleted || s.deps.Store == nil {
		return job.UpdatedAt
	}
	var version time.Time
	if err := s.deps.Store.Pool.QueryRow(ctx,
		`SELECT data_version FROM export_preparations WHERE job_id=$1 LIMIT 1`, job.ID).Scan(&version); err == nil {
		return version
	}
	return job.UpdatedAt
}

// Bounds the new cache independently of pre-existing export objects. This is
// a safety ceiling, not a promise that every queued format can be prepared.
const maxPreparedExportBytes int64 = 100 << 30

// handleJobExportPrepare lets a user requeue a failed or expired format, or
// prepare a completed job that predates the automatic completion trigger.
func (s *Server) handleJobExportPrepare(w http.ResponseWriter, r *http.Request) {
	job, ok := s.requireJobScope(r, chi.URLParam(r, "id"))
	if !ok {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "任务不存在或无权访问", nil)
		return
	}
	if job.Status != model.JobCompleted {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "下载任务尚未完成", nil)
		return
	}
	var req struct {
		Format string `json:"format"`
	}
	if r.ContentLength > 0 && decodeJSON(r, &req) != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if req.Format != "" && req.Format != "csv" && req.Format != "xlsx" && req.Format != "jsonl" && req.Format != "parquet" {
		validationError(w, r, "格式不支持", nil)
		return
	}
	// The first INSERT handles a completed job created before migration 0076.
	_, err := s.deps.Store.Pool.Exec(r.Context(), `
		INSERT INTO export_preparations(job_id,format,data_version)
		SELECT $1::uuid, f.format, $2::timestamptz
		FROM unnest(ARRAY['csv','xlsx','jsonl','parquet']) AS f(format)
		ON CONFLICT (job_id,format) DO NOTHING`, job.ID, job.UpdatedAt)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "准备导出文件失败", nil)
		return
	}
	// A format explicitly requested by the user may be retried after failure,
	// expiry, or physical loss. The version guard prevents reviving old data.
	if req.Format != "" {
		var status, objectRef string
		var expiresAt *time.Time
		readErr := s.deps.Store.Pool.QueryRow(r.Context(), `
			SELECT p.status, COALESCE(e.object_ref,''), e.expires_at
			FROM export_preparations p LEFT JOIN exports e ON e.id=p.export_id
			WHERE p.job_id=$1 AND p.format=$2`,
			job.ID, req.Format).Scan(&status, &objectRef, &expiresAt)
		if readErr != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取文件准备状态失败", nil)
			return
		}
		if status == "READY" {
			if expiresAt != nil && !expiresAt.After(time.Now()) {
				respondError(w, r, http.StatusConflict, "EXPORT_EXPIRING", "旧文件正在到期清理，请稍后重试", nil)
				return
			}
			if objectRef != "" {
				if _, _, statErr := s.statExportObject(r.Context(), objectRef); statErr == nil {
					respondOK(w, r, map[string]any{"job_id": job.ID, "format": req.Format, "status": "READY"})
					return
				} else if !errors.Is(statErr, errExportObjectMissing) {
					respondError(w, r, http.StatusServiceUnavailable, "EXPORT_STORAGE_UNAVAILABLE", "暂时无法核实文件，请稍后重试", nil)
					return
				}
			}
			// The old file is physically absent. It cannot consume cache capacity.
			_, _ = s.deps.Store.Pool.Exec(r.Context(),
				`UPDATE exports SET status='FAILED', error='prepared object missing', updated_at=now() WHERE id=(SELECT export_id FROM export_preparations WHERE job_id=$1 AND format=$2)`,
				job.ID, req.Format)
		}
		_, err = s.deps.Store.Pool.Exec(r.Context(), `
			UPDATE export_preparations SET status='PENDING', attempts=0, export_id=NULL,
				bytes=0, lease_until=NULL, retry_after=NULL, error='', updated_at=now()
			WHERE job_id=$1 AND format=$2
			  AND status IN ('FAILED','EXPIRED','READY')`, job.ID, req.Format)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "重试准备失败", nil)
			return
		}
	}
	s.writeAuditNow(r, "export.preparation_requested", "export", "download_job", job.ID,
		map[string]any{"format": req.Format})
	respondAccepted(w, r, map[string]any{"job_id": job.ID, "format": req.Format, "status": "PREPARING"})
}

// StartExportPreparer runs one heavy export at a time in this API process.
// The row lease prevents two API instances from preparing the same format.
func (s *Server) StartExportPreparer(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if s.deps.Store == nil || s.deps.Raw == nil || s.query == nil {
			return
		}
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		lastCleanup := time.Time{}
		for {
			if ctx.Err() != nil {
				return
			}
			if time.Since(lastCleanup) >= time.Hour {
				if err := s.cleanExpiredPreparedExports(ctx); err != nil && s.deps.Log != nil {
					s.deps.Log.Error("expired export preparation cleanup failed", "error", err)
				}
				if _, err := s.deps.Store.Pool.Exec(ctx, `
					DELETE FROM api_create_idempotency WHERE ctid IN
					  (SELECT ctid FROM api_create_idempotency WHERE expires_at<now()
					   ORDER BY expires_at LIMIT 1000)`); err != nil && s.deps.Log != nil {
					s.deps.Log.Error("expired create idempotency cleanup failed", "error", err)
				}
				lastCleanup = time.Now()
			}
			group, groupErr := s.claimExportPreparationGroup(ctx)
			if groupErr != nil && !errors.Is(groupErr, pgx.ErrNoRows) && s.deps.Log != nil {
				s.deps.Log.Error("claim grouped export preparation failed", "error", groupErr)
			}
			if groupErr == nil {
				s.prepareExportGroup(ctx, group)
				continue
			}
			prep, err := s.claimExportPreparation(ctx)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) && s.deps.Log != nil {
				s.deps.Log.Error("claim export preparation failed", "error", err)
			}
			if err == nil {
				s.prepareExport(ctx, prep)
				continue
			}
			if errors.Is(err, pgx.ErrNoRows) {
				if capacityErr := s.failCapacityBlockedPreparations(ctx); capacityErr != nil && s.deps.Log != nil {
					s.deps.Log.Error("export preparation capacity check failed", "error", capacityErr)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func (s *Server) failCapacityBlockedPreparations(ctx context.Context) error {
	_, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE export_preparations p
		SET status='FAILED', attempts=3, retry_after=NULL,
		    error='prepared export cache is full; retry after older files expire', updated_at=now()
		WHERE p.status='PENDING'
		  AND EXISTS (SELECT 1 FROM download_jobs j WHERE j.id=p.job_id
		      AND j.status='COMPLETED' AND j.source='user_download')
		  AND (SELECT COALESCE(SUM(bytes),0) FROM export_preparations WHERE status='READY') >= $1`,
		maxPreparedExportBytes)
	return err
}

func (s *Server) claimExportPreparation(ctx context.Context) (exportPreparation, error) {
	var p exportPreparation
	err := s.deps.Store.Pool.QueryRow(ctx, `
		WITH next AS (
			SELECT p.job_id, p.format FROM export_preparations p
			JOIN download_jobs j ON j.id=p.job_id
			WHERE j.status='COMPLETED'
			  AND j.source='user_download'
			  AND (SELECT COALESCE(SUM(bytes),0) FROM export_preparations WHERE status='READY') < $1
			  AND p.attempts < 3
			  AND (p.status='PENDING'
			       OR (p.status='FAILED' AND p.retry_after <= now())
			       OR (p.status='RUNNING' AND p.lease_until < now()))
			ORDER BY p.updated_at, p.job_id, p.format
			FOR UPDATE OF p SKIP LOCKED LIMIT 1
		)
		UPDATE export_preparations p SET status='RUNNING', attempts=p.attempts+1,
			lease_until=now()+interval '2 minutes', retry_after=NULL, error='', updated_at=now()
		FROM next WHERE p.job_id=next.job_id AND p.format=next.format
		RETURNING p.job_id::text, p.format, p.data_version, p.attempts`, maxPreparedExportBytes).
		Scan(&p.jobID, &p.format, &p.dataVersion, &p.attempt)
	return p, err
}

func (s *Server) prepareExport(parent context.Context, p exportPreparation) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stopHeartbeat := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				tag, err := s.deps.Store.Pool.Exec(ctx, `
					UPDATE export_preparations SET lease_until=now()+interval '2 minutes'
					WHERE job_id=$1 AND format=$2 AND data_version=$3
					  AND status='RUNNING' AND attempts=$4`, p.jobID, p.format, p.dataVersion, p.attempt)
				if err != nil || tag.RowsAffected() != 1 {
					cancel()
					return
				}
			}
		}
	}()
	exportID, size, err := s.buildPreparedExport(ctx, p)
	close(stopHeartbeat)
	if parent.Err() != nil {
		return // Leave the lease for a restarted worker to reclaim.
	}
	if err != nil {
		if s.deps.Log != nil {
			s.deps.Log.Warn("export preparation failed", "job_id", p.jobID, "format", p.format, "attempt", p.attempt, "error", err)
		}
		backoff := time.Duration(15*p.attempt*p.attempt) * time.Second
		_, _ = s.deps.Store.Pool.Exec(parent, `
			UPDATE export_preparations SET status='FAILED', lease_until=NULL,
				retry_after=now()+$5::interval, error=$6, updated_at=now()
			WHERE job_id=$1 AND format=$2 AND data_version=$3
			  AND status='RUNNING' AND attempts=$4`, p.jobID, p.format, p.dataVersion, p.attempt,
			fmt.Sprintf("%d seconds", int(backoff.Seconds())), err.Error())
		return
	}
	_, _ = s.deps.Store.Pool.Exec(parent, `
		UPDATE export_preparations SET status='READY', export_id=$5, bytes=$6, lease_until=NULL,
			retry_after=NULL, error='', updated_at=now()
		WHERE job_id=$1 AND format=$2 AND data_version=$3
		  AND status='RUNNING' AND attempts=$4`, p.jobID, p.format, p.dataVersion, p.attempt, exportID, size)
}

func (s *Server) buildPreparedExport(ctx context.Context, p exportPreparation) (string, int64, error) {
	job, err := s.deps.Store.GetJob(ctx, p.jobID)
	if err != nil {
		return "", 0, err
	}
	if job.Status != model.JobCompleted || !s.jobExportVersion(ctx, job).Equal(p.dataVersion) {
		return "", 0, fmt.Errorf("job version changed while preparing export")
	}
	if job.OrganizationID == nil || job.WorkspaceID == nil {
		return "", 0, fmt.Errorf("job lacks tenant scope")
	}
	subjects, err := s.deps.Store.SubjectsByJob(ctx, job.ID)
	if err != nil {
		return "", 0, err
	}
	addresses := make([]string, 0, len(subjects))
	for _, sub := range subjects {
		addresses = append(addresses, sub.Subject)
	}
	addresses, err = dataset.NormalizeAndDedupeAddressesForChain(job.Chain, addresses)
	if err != nil {
		return "", 0, err
	}
	if len(addresses) > 100 {
		return "", 0, fmt.Errorf("job has %d addresses; task export limit is 100", len(addresses))
	}
	zipIt := len(addresses) > 1 || len(job.Datasets) > 1
	snapshot, err := json.Marshal(map[string]any{
		"chain": job.Chain, "addresses": addresses, "datasets": job.Datasets,
		"format": p.format, "include_meta": false, "zip": zipIt,
		"include_decoded_input": false, "job_id": job.ID,
		"partial": false, "job_status": model.JobCompleted,
	})
	if err != nil {
		return "", 0, err
	}
	// Reclaim a completed file if a process died after writing it but before
	// updating export_preparations. Recheck the physical object before reuse.
	var existingID, existingRef string
	err = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT id::text, object_ref FROM exports
		WHERE job_id=$1 AND format=$2 AND zip=$3 AND status='COMPLETED'
		  AND query_snapshot=$4::jsonb AND organization_id=$5 AND workspace_id=$6
		  AND object_ref<>'' AND (expires_at IS NULL OR expires_at>now())
		  AND COALESCE(finished_at,updated_at)>=$7
		ORDER BY finished_at DESC NULLS LAST LIMIT 1`,
		job.ID, p.format, zipIt, snapshot, *job.OrganizationID, *job.WorkspaceID, p.dataVersion).
		Scan(&existingID, &existingRef)
	if err == nil {
		if size, _, statErr := s.statExportObject(ctx, existingRef); statErr == nil {
			return existingID, size, nil
		} else if !errors.Is(statErr, errExportObjectMissing) {
			return "", 0, statErr
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", 0, err
	}
	exp, err := s.deps.Store.CreateExportV2(ctx, *job.WorkspaceID, *job.OrganizationID, nil, &job.ID,
		strings.Join(job.Datasets, "+"), p.format, zipIt, snapshot)
	if err != nil {
		return "", 0, err
	}
	s.runExportMulti(ctx, exp, job, job.Chain, addresses, job.Datasets, p.format, false, zipIt, false, exportBounds{})
	var status, objectRef, errorMsg string
	err = s.deps.Store.Pool.QueryRow(ctx, `SELECT status, object_ref, error FROM exports WHERE id=$1`, exp.ID).
		Scan(&status, &objectRef, &errorMsg)
	if err != nil {
		return "", 0, err
	}
	if status != "COMPLETED" || objectRef == "" {
		return "", 0, fmt.Errorf("export %s ended with %s: %s", exp.ID, status, errorMsg)
	}
	size, _, err := s.statExportObject(ctx, objectRef)
	if err != nil {
		return "", 0, err
	}
	jobAfter, err := s.deps.Store.GetJob(ctx, job.ID)
	if err != nil || jobAfter.Status != model.JobCompleted || !s.jobExportVersion(ctx, jobAfter).Equal(p.dataVersion) {
		return "", 0, fmt.Errorf("job changed before prepared file became ready")
	}
	return exp.ID, size, nil
}

func (s *Server) cleanExpiredPreparedExports(ctx context.Context) error {
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT p.job_id::text, p.format, p.export_id::text, e.object_ref
		FROM export_preparations p JOIN exports e ON e.id=p.export_id
		WHERE p.status='READY' AND e.expires_at < now() - interval '30 minutes'
		ORDER BY e.expires_at LIMIT 20`)
	if err != nil {
		return err
	}
	type expired struct{ jobID, format, exportID, objectRef string }
	items := make([]expired, 0, 20)
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.jobID, &item.format, &item.exportID, &item.objectRef); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	order, err := s.exportReadOrder()
	if err != nil {
		return err
	}
	for _, item := range items {
		// The object key contains the export UUID. Only system-prepared exports
		// selected above are removed; user-created exports are untouched.
		for _, bucket := range order {
			name, err := s.exportBucketName(bucket)
			if err != nil {
				return err
			}
			if err := s.deps.Raw.Client().RemoveObject(ctx, name, item.objectRef, minio.RemoveObjectOptions{}); err != nil {
				return err
			}
		}
		_, err = s.deps.Store.Pool.Exec(ctx, `
			UPDATE export_preparations SET status='EXPIRED', bytes=0, updated_at=now()
			WHERE job_id=$1 AND format=$2 AND export_id=$3 AND status='READY'`,
			item.jobID, item.format, item.exportID)
		if err != nil {
			return err
		}
		_, err = s.deps.Store.Pool.Exec(ctx, `UPDATE exports SET status='EXPIRED', updated_at=now() WHERE id=$1 AND status='COMPLETED'`, item.exportID)
		if err != nil {
			return err
		}
		s.writeAuditCtx(ctx, "export.preparation_expired", "export", "export", item.exportID,
			map[string]any{"job_id": item.jobID, "format": item.format, "object_ref": item.objectRef})
	}
	return nil
}
