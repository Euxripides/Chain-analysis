package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"path/filepath"
)

func (s *Server) investigationExport(ctx context.Context, q investigationRequest) (map[string]any, error) {
	if _, e := uuid.Parse(q.JobID); e != nil {
		return nil, fmt.Errorf("invalid job_id")
	}
	if q.Offset < 0 {
		return nil, fmt.Errorf("offset must be nonnegative")
	}
	if q.ChunkSize == 0 {
		q.ChunkSize = 1 << 20
	}
	if q.ChunkSize < 1 || q.ChunkSize > 4<<20 {
		return nil, fmt.Errorf("chunk_size must be 1-4194304 bytes")
	}
	scope, e := s.systemScope(ctx)
	if e != nil {
		return nil, summaryError(500, "INTERNAL_ERROR", "Unable to resolve workspace")
	}
	var org, workspace string
	e = s.deps.Store.Pool.QueryRow(ctx, `SELECT COALESCE(organization_id::text,''),COALESCE(workspace_id::text,'') FROM exports WHERE id=$1`, q.JobID).Scan(&org, &workspace)
	if errors.Is(e, pgx.ErrNoRows) || (e == nil && (org != scope.OrganizationID || workspace != scope.WorkspaceID)) {
		return nil, summaryError(404, "JOB_NOT_FOUND", "Export job not found")
	}
	if e != nil {
		return nil, summaryError(500, "INTERNAL_ERROR", "Unable to read export metadata")
	}
	exp, e := s.deps.Store.GetExport(ctx, q.JobID)
	if e != nil || exp == nil {
		return nil, summaryError(500, "INTERNAL_ERROR", "Unable to read export metadata")
	}
	if exp.Status != "COMPLETED" {
		return nil, summaryError(409, "EXPORT_NOT_COMPLETED", "Only completed exports are readable")
	}
	size, _, e := s.statExportObject(ctx, exp.ObjectRef)
	if e != nil {
		return nil, summaryError(503, "EXPORT_FILE_UNAVAILABLE", "Unable to read export file")
	}
	if q.Offset > size {
		return nil, fmt.Errorf("offset exceeds file size")
	}
	data := []byte{}
	if q.Offset < size {
		reader, _, _, e := s.openExportObjectRange(ctx, exp.ObjectRef, q.Offset, q.Offset+int64(q.ChunkSize)-1)
		if e != nil {
			return nil, summaryError(503, "EXPORT_FILE_UNAVAILABLE", "Unable to read export range")
		}
		defer reader.Close()
		data, e = io.ReadAll(io.LimitReader(reader, int64(q.ChunkSize)))
		if e != nil {
			return nil, summaryError(503, "EXPORT_FILE_UNAVAILABLE", "Unable to read file chunk")
		}
	}
	if int64(len(data)) != min(int64(q.ChunkSize), size-q.Offset) {
		return nil, summaryError(422, "EXPORT_INTEGRITY_FAILED", "Export file is truncated")
	}
	digest := sha256.Sum256(data)
	chunkSHA := hex.EncodeToString(digest[:])
	whole := q.Offset == 0 && int64(len(data)) == size
	verified := false
	if whole && exp.SHA256 != "" {
		if chunkSHA != exp.SHA256 {
			return nil, summaryError(422, "EXPORT_INTEGRITY_FAILED", "Export checksum mismatch")
		}
		verified = true
	}
	next := q.Offset + int64(len(data))
	var nextOffset any
	if next < size {
		nextOffset = next
	}
	name := filepath.Base(exp.ObjectRef)
	mime := "application/octet-stream"
	if filepath.Ext(name) == ".zip" {
		mime = "application/zip"
	}
	if filepath.Ext(name) == ".csv" {
		mime = "text/csv"
	}
	if filepath.Ext(name) == ".parquet" {
		mime = "application/vnd.apache.parquet"
	}
	return map[string]any{"job_id": q.JobID, "filename": name, "mime_type": mime, "size_bytes": size, "sha256": exp.SHA256, "chunk_sha256": chunkSHA, "offset": q.Offset, "chunk_bytes": len(data), "next_offset": nextOffset, "complete_file": whole, "full_integrity_verified": verified, "content_base64": base64.StdEncoding.EncodeToString(data), "resource_uri": fmt.Sprintf("export://%s/%d/%d", q.JobID, q.Offset, q.ChunkSize)}, nil
}
