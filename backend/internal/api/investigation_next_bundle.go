package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

type evidenceEntry struct {
	Name string `json:"name"`
	SHA  string `json:"sha256"`
	Size int    `json:"size_bytes"`
}
type evidenceManifest struct {
	Version        string          `json:"schema_version"`
	AnalysisID     string          `json:"analysis_id"`
	Processor      string          `json:"processor_version"`
	Operation      string          `json:"operation"`
	RequestSHA     string          `json:"semantic_request_sha256"`
	Entries        []evidenceEntry `json:"entries"`
	Interpretation string          `json:"interpretation"`
}

var nextBundleGate = make(chan struct{}, 2)

func bytesSHA(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func buildEvidenceZip(job nextJob, files map[string][]byte) ([]byte, evidenceManifest, error) {
	copyFiles := map[string][]byte{}
	for name, b := range files {
		copyFiles[name] = b
	}
	files = copyFiles
	m := evidenceManifest{Version: "investigation-evidence-v1", AnalysisID: job.ID, Processor: job.Version, Operation: job.Operation, Interpretation: "Hashes prove integrity of saved inputs/results, not correctness of providers, completeness of chain history, or beneficial ownership"}
	m.RequestSHA = job.RequestSHA
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m.Entries = append(m.Entries, evidenceEntry{name, bytesSHA(files[name]), len(files[name])})
	}
	manifest, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return nil, m, e
	}
	files["manifest.json"] = manifest
	names = append(names, "manifest.json")
	sort.Strings(names)
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		entry, e := writer.CreateHeader(header)
		if e != nil {
			return nil, m, e
		}
		if _, e = entry.Write(files[name]); e != nil {
			return nil, m, e
		}
		if buf.Len() > nextObjectLimit {
			return nil, m, fmt.Errorf("bundle exceeds 32 MiB")
		}
	}
	if e = writer.Close(); e != nil {
		return nil, m, e
	}
	if buf.Len() > nextObjectLimit {
		return nil, m, fmt.Errorf("bundle exceeds 32 MiB")
	}
	return buf.Bytes(), m, nil
}
func verifyEvidenceZip(raw []byte) (evidenceManifest, error) {
	var m evidenceManifest
	r, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return m, e
	}
	if len(r.File) < 2 || len(r.File) > 4 {
		return m, fmt.Errorf("invalid bundle entry count")
	}
	files := map[string][]byte{}
	total := int64(0)
	for _, entry := range r.File {
		if entry.Name != "manifest.json" && entry.Name != "request.json" && entry.Name != "input.json" && entry.Name != "result.json" {
			return m, fmt.Errorf("unexpected evidence entry")
		}
		if _, ok := files[entry.Name]; ok {
			return m, fmt.Errorf("duplicate evidence entry")
		}
		if entry.UncompressedSize64 > nextObjectLimit {
			return m, fmt.Errorf("evidence entry exceeds budget")
		}
		total += int64(entry.UncompressedSize64)
		if total > 3*nextObjectLimit+(2<<20) {
			return m, fmt.Errorf("bundle expansion exceeds budget")
		}
		f, e := entry.Open()
		if e != nil {
			return m, e
		}
		b, e := io.ReadAll(io.LimitReader(f, nextObjectLimit+1))
		f.Close()
		if e != nil || len(b) > nextObjectLimit || uint64(len(b)) != entry.UncompressedSize64 {
			return m, fmt.Errorf("invalid evidence entry length or CRC")
		}
		files[entry.Name] = b
	}
	if e = json.Unmarshal(files["manifest.json"], &m); e != nil {
		return m, e
	}
	if m.Version != "investigation-evidence-v1" || len(m.Entries) != len(files)-1 {
		return m, fmt.Errorf("invalid manifest")
	}
	seen := map[string]bool{}
	for _, entry := range m.Entries {
		b, ok := files[entry.Name]
		if !ok || seen[entry.Name] || entry.Name == "manifest.json" || len(b) != entry.Size || bytesSHA(b) != entry.SHA {
			return m, fmt.Errorf("evidence entry checksum mismatch")
		}
		seen[entry.Name] = true
	}
	if !seen["request.json"] || !seen["result.json"] {
		return m, fmt.Errorf("required evidence missing")
	}
	return m, nil
}
func (s *Server) nextCachedBundle(ctx context.Context, scope tenantScope, analysisID string) (any, bool, error) {
	var id, key, digest string
	var size int64
	var raw []byte
	e := s.deps.Store.Pool.QueryRow(ctx, `SELECT id::text,object_key,sha256,size_bytes,manifest FROM investigation_evidence_bundles WHERE analysis_id=$1 AND organization_id=$2 AND workspace_id=$3 ORDER BY created_at DESC LIMIT 1`, analysisID, scope.OrganizationID, scope.WorkspaceID).Scan(&id, &key, &digest, &size, &raw)
	if e == pgx.ErrNoRows {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, e
	}
	if s.deps.Raw == nil {
		return nil, false, summaryError(503, "OBJECT_STORE_UNAVAILABLE", "Evidence storage unavailable")
	}
	actual, e := s.deps.Raw.Stat(ctx, key)
	if e != nil {
		return nil, false, summaryError(503, "EVIDENCE_UNAVAILABLE", "Saved bundle unavailable")
	}
	if actual != size {
		return nil, false, summaryError(422, "EVIDENCE_INTEGRITY_FAILED", "Saved bundle length mismatch")
	}
	var manifest any
	if e = nextDecode(raw, &manifest); e != nil {
		return nil, false, e
	}
	return map[string]any{"bundle_id": id, "analysis_id": analysisID, "sha256": digest, "size_bytes": size, "manifest": manifest, "reused": true, "resource_uri": fmt.Sprintf("evidence://%s/0/1048576", id)}, true, nil
}

func (s *Server) nextBundle(ctx context.Context, q nextRequest, op string) (any, error) {
	scope, e := s.nextTenant(ctx)
	if e != nil {
		return nil, e
	}
	if op == "create_evidence_bundle" {
		if q.AnalysisID == "" {
			return nil, fmt.Errorf("analysis_id is required")
		}
		job, e := s.nextGetJob(ctx, q.AnalysisID, false)
		if e != nil {
			return nil, e
		}
		if job.Status != "completed" {
			return nil, summaryError(409, "ANALYSIS_NOT_COMPLETED", "Bundle requires completed analysis")
		}
		if job.Version == "contract-analysis-v1" {
			return nil, summaryError(422, "CONTRACT_ARTIFACT_TOOL_REQUIRED", "Read the saved contract evidence_bundle artifact with get_contract_analysis_artifact")
		}
		if reused, found, err := s.nextCachedBundle(ctx, scope, job.ID); err != nil || found {
			return reused, err
		}
		select {
		case nextBundleGate <- struct{}{}:
			defer func() { <-nextBundleGate }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// Another caller may have finished while this one waited for capacity.
		if reused, found, err := s.nextCachedBundle(ctx, scope, job.ID); err != nil || found {
			return reused, err
		}
		result, e := s.nextObject(ctx, job.ResultObject, job.ResultSHA)
		if e != nil {
			return nil, e
		}
		files := map[string][]byte{"request.json": job.Request, "result.json": result}
		if job.InputObject != "" {
			input, e := s.nextObject(ctx, job.InputObject, job.InputSHA)
			if e != nil {
				return nil, e
			}
			files["input.json"] = input
		}
		raw, manifest, e := buildEvidenceZip(job, files)
		if e != nil {
			return nil, e
		}
		if _, e = verifyEvidenceZip(raw); e != nil {
			return nil, e
		}
		digest := bytesSHA(raw)
		key := fmt.Sprintf("investigation/%s/%s/%s/bundle-%s.zip", scope.OrganizationID, scope.WorkspaceID, job.ID, digest)
		if _, e = s.deps.Raw.Put(ctx, key, raw, "application/zip"); e != nil {
			return nil, e
		}
		encoded, _ := json.Marshal(manifest)
		var id string
		e = s.deps.Store.Pool.QueryRow(ctx, `INSERT INTO investigation_evidence_bundles(organization_id,workspace_id,analysis_id,object_key,sha256,size_bytes,manifest) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(organization_id,workspace_id,analysis_id,sha256) DO UPDATE SET sha256=EXCLUDED.sha256 RETURNING id::text`, scope.OrganizationID, scope.WorkspaceID, job.ID, key, digest, len(raw), encoded).Scan(&id)
		if e != nil {
			return nil, e
		}
		return map[string]any{"bundle_id": id, "analysis_id": job.ID, "sha256": digest, "size_bytes": len(raw), "manifest": manifest, "reused": false, "resource_uri": fmt.Sprintf("evidence://%s/0/1048576", id)}, nil
	}
	if q.BundleID == "" {
		return nil, fmt.Errorf("bundle_id is required")
	}
	var key, digest string
	var size int64
	var manifest []byte
	e = s.deps.Store.Pool.QueryRow(ctx, `SELECT object_key,sha256,size_bytes,manifest FROM investigation_evidence_bundles WHERE id=$1 AND organization_id=$2 AND workspace_id=$3`, q.BundleID, scope.OrganizationID, scope.WorkspaceID).Scan(&key, &digest, &size, &manifest)
	if e == pgx.ErrNoRows {
		return nil, summaryError(404, "BUNDLE_NOT_FOUND", "Evidence bundle not found")
	}
	if e != nil {
		return nil, e
	}
	if s.deps.Raw == nil {
		return nil, summaryError(503, "OBJECT_STORE_UNAVAILABLE", "Evidence storage unavailable")
	}
	if op == "verify_evidence_bundle" {
		select {
		case nextBundleGate <- struct{}{}:
			defer func() { <-nextBundleGate }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		raw, e := s.nextObject(ctx, key, digest)
		if e != nil {
			return nil, e
		}
		m, e := verifyEvidenceZip(raw)
		if e != nil {
			return nil, summaryError(422, "EVIDENCE_INTEGRITY_FAILED", "Bundle entry validation failed")
		}
		return map[string]any{"bundle_id": q.BundleID, "integrity_verified": true, "sha256": digest, "size_bytes": len(raw), "manifest": m, "verification_scope": "Saved archive and each declared entry checksum/length/CRC; does not attest provider correctness"}, nil
	}
	if q.ChunkSize == 0 {
		q.ChunkSize = 1 << 20
	}
	if q.Offset < 0 || q.Offset > size || q.ChunkSize < 1 || q.ChunkSize > 4<<20 {
		return nil, fmt.Errorf("invalid bounded file range")
	}
	raw := []byte{}
	if q.Offset < size {
		r, _, e := s.deps.Raw.OpenRange(ctx, key, q.Offset, q.Offset+int64(q.ChunkSize)-1)
		if e != nil {
			return nil, e
		}
		defer r.Close()
		raw, e = io.ReadAll(io.LimitReader(r, int64(q.ChunkSize)))
		if e != nil {
			return nil, e
		}
	}
	if int64(len(raw)) != min(int64(q.ChunkSize), size-q.Offset) {
		return nil, summaryError(422, "EVIDENCE_INTEGRITY_FAILED", "Truncated bundle chunk")
	}
	whole := q.Offset == 0 && int64(len(raw)) == size
	if whole && bytesSHA(raw) != digest {
		return nil, summaryError(422, "EVIDENCE_INTEGRITY_FAILED", "Whole bundle checksum mismatch")
	}
	var next any
	if q.Offset+int64(len(raw)) < size {
		next = q.Offset + int64(len(raw))
	}
	return map[string]any{"bundle_id": q.BundleID, "filename": "investigation-" + q.BundleID + ".zip", "mime_type": "application/zip", "size_bytes": size, "sha256": digest, "offset": q.Offset, "chunk_bytes": len(raw), "chunk_sha256": bytesSHA(raw), "next_offset": next, "complete_file": whole, "full_integrity_verified": whole, "content_base64": base64.StdEncoding.EncodeToString(raw), "resource_uri": fmt.Sprintf("evidence://%s/%d/%d", q.BundleID, q.Offset, q.ChunkSize)}, nil
}
