package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

type contractChunkWriter struct {
	Position, Offset, End int64
	Data                  bytes.Buffer
}

func (w *contractChunkWriter) Write(p []byte) (int, error) {
	n := len(p)
	from := max(w.Offset-w.Position, 0)
	to := min(w.End-w.Position, int64(n))
	if from < to {
		w.Data.Write(p[from:to])
	}
	w.Position += int64(n)
	return n, nil
}
func (s *Server) contractReadArtifact(ctx context.Context, q ca.Request, preview ...bool) (any, error) {
	job, e := s.contractJob(ctx, q.AnalysisID)
	if e != nil {
		return nil, e
	}
	if job.Status != "completed" {
		return nil, summaryError(409, "ANALYSIS_NOT_COMPLETED", "Artifacts publish with completed analysis")
	}
	scope, e := s.nextTenant(ctx)
	if e != nil {
		return nil, e
	}
	var artifact ca.Artifact
	e = s.deps.Store.Pool.QueryRow(ctx, `SELECT artifact_id,kind,filename,media_type,object_key,byte_size,sha256,method_id FROM contract_analysis_artifacts WHERE analysis_id=$1 AND artifact_id=$2 AND organization_id=$3 AND workspace_id=$4`, job.ID, q.ArtifactID, scope.OrganizationID, scope.WorkspaceID).Scan(&artifact.ArtifactID, &artifact.Kind, &artifact.Filename, &artifact.MediaType, &artifact.ObjectKey, &artifact.Bytes, &artifact.SHA256, &artifact.MethodID)
	if e != nil {
		return nil, summaryError(404, "ARTIFACT_NOT_FOUND", "Contract artifact not found")
	}
	if len(preview) > 0 && preview[0] {
		allowed := map[string]bool{"verified_source": true, "pseudocode": true, "disassembly": true, "symbolic_ir": true, "storage_layout": true, "bytecode": true, "heimdall_output": true}
		if !allowed[artifact.Kind] {
			return nil, summaryError(403, "ARTIFACT_PREVIEW_DENIED", "This artifact requires export permission")
		}
		if q.ChunkSize > 64<<10 {
			return nil, fmt.Errorf("preview chunk_size must not exceed 65536")
		}
	}
	if q.Offset > artifact.Bytes {
		return nil, fmt.Errorf("offset exceeds artifact length")
	}
	reader, n, e := s.deps.Raw.OpenNamed(ctx, s.deps.Raw.Bucket(), artifact.ObjectKey)
	if e != nil {
		return nil, summaryError(503, "EVIDENCE_UNAVAILABLE", "Saved artifact unavailable")
	}
	defer reader.Close()
	if n != artifact.Bytes || n > 64<<20 {
		return nil, summaryError(422, "EVIDENCE_INTEGRITY_FAILED", "Artifact length mismatch")
	}
	capture := &contractChunkWriter{Offset: q.Offset, End: min(q.Offset+int64(q.ChunkSize), n)}
	h := sha256.New()
	size, e := io.Copy(io.MultiWriter(h, capture), io.LimitReader(reader, n+1))
	if e != nil {
		return nil, e
	}
	if size != n || hex.EncodeToString(h.Sum(nil)) != artifact.SHA256 {
		return nil, summaryError(422, "EVIDENCE_INTEGRITY_FAILED", "Artifact checksum mismatch")
	}
	chunk := capture.Data.Bytes()
	var next any
	if capture.End < n {
		next = capture.End
	}
	artifact.ObjectKey = ""
	return map[string]any{"analysis_id": job.ID, "artifact": artifact, "offset": q.Offset, "byte_length": len(chunk), "sha256": artifact.SHA256, "chunk_sha256": ca.SHA256(chunk), "content_base64": base64.StdEncoding.EncodeToString(chunk), "next_offset": next, "complete": capture.End == n, "resource_uri": fmt.Sprintf("contract-analysis://%s/%s/%d/%d", job.ID, artifact.ArtifactID, q.Offset, q.ChunkSize)}, nil
}
func (s *Server) contractAddArtifact(ctx context.Context, result *ca.Result, kind, filename, media string, data []byte) error {
	if len(data) > 64<<20 {
		return summaryError(422, "EXPORT_BUDGET_EXCEEDED", "Contract export exceeds 64 MiB")
	}
	obj, e := s.contractSaveObject(ctx, "artifact", data, media)
	if e != nil {
		return e
	}
	a := ca.Artifact{ArtifactID: ca.ID(kind, obj.SHA256), Kind: kind, Filename: filename, MediaType: media, SHA256: obj.SHA256, Bytes: obj.Bytes, ObjectKey: obj.Key}
	result.Sections["artifacts"] = append(result.Sections["artifacts"], ca.Row(a))
	return nil
}
func rowMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	_ = nextDecode(raw, &m)
	return m
}
func stringList(v any) []string {
	values := []string{}
	if a, ok := v.([]any); ok {
		for _, s := range a {
			values = append(values, fmt.Sprint(s))
		}
	}
	return values
}
func (s *Server) contractExportArtifacts(ctx context.Context, result *ca.Result, frozen ca.FrozenInput) error {
	// Plain CSV starts directly at the header. Exact raw values remain strings.
	var csvData bytes.Buffer
	c := csv.NewWriter(&csvData)
	c.Write([]string{"method_id", "code_address", "storage_address", "selector", "signature", "verified_signature", "purpose", "behaviors", "complete", "evidence_ids"})
	for _, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		complete := rowMap(ca.Row(m["completeness"]))
		c.Write([]string{fmt.Sprint(m["method_id"]), fmt.Sprint(m["code_address"]), fmt.Sprint(m["storage_address"]), fmt.Sprint(m["selector"]), fmt.Sprint(m["signature"]), fmt.Sprint(m["verified_signature"]), fmt.Sprint(m["purpose"]), strings.Join(stringList(m["behaviors"]), ";"), fmt.Sprint(complete["complete"]), strings.Join(stringList(m["evidence_ids"]), ";")})
	}
	c.Flush()
	if e := c.Error(); e != nil {
		return e
	}
	if e := s.contractAddArtifact(ctx, result, "method_csv", "contract-methods.csv", "text/csv; charset=utf-8", csvData.Bytes()); e != nil {
		return e
	}
	graph := map[string]any{"schema": "contract-flow-1", "snapshot": result.Snapshot, "edges": result.Sections["flows"], "execution_calls": result.Sections["execution_calls"], "executed_funds": result.Sections["executed_funds"]}
	if e := s.contractAddArtifact(ctx, result, "editable_graph", "contract-flow.json", "application/json", ca.Row(graph)); e != nil {
		return e
	}
	var dot strings.Builder
	dot.WriteString("digraph contract_analysis {\n")
	for _, raw := range result.Sections["flows"] {
		edge := rowMap(raw)
		from, _ := json.Marshal(fmt.Sprint(edge["from"]))
		to, _ := json.Marshal(fmt.Sprint(edge["to"]))
		label, _ := json.Marshal(fmt.Sprint(edge["edge_type"]))
		fmt.Fprintf(&dot, "  %s -> %s [label=%s];\n", from, to, label)
	}
	dot.WriteString("}\n")
	if e := s.contractAddArtifact(ctx, result, "editable_graph", "contract-flow.dot", "text/vnd.graphviz", []byte(dot.String())); e != nil {
		return e
	}
	var report strings.Builder
	fmt.Fprintf(&report, "# %s %s 合约运行与方法分析\n\n区块：%d\n\n区块哈希：%s\n\n", result.Snapshot.Chain, result.Snapshot.EntryAddress, result.Snapshot.BlockNumber, result.Snapshot.BlockHash)
	for _, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		mid := fmt.Sprint(m["method_id"])
		title := fmt.Sprint(m["signature"])
		if title == "" {
			title = fmt.Sprint(m["selector"])
		}
		fmt.Fprintf(&report, "## %s\n\n代码地址：%s\n\n状态地址：%s\n\n%s。\n\n", title, m["code_address"], m["storage_address"], m["purpose"])
		for _, part := range []struct{ section, label string }{{"permissions", "执行条件"}, {"steps", "执行步骤"}, {"funds", "资金作用"}, {"storage", "状态读写"}, {"calls", "外部调用"}} {
			items := []map[string]any{}
			for _, row := range result.Sections[part.section] {
				item := rowMap(row)
				if item["method_id"] == mid {
					items = append(items, item)
				}
			}
			if len(items) == 0 {
				continue
			}
			fmt.Fprintf(&report, "### %s\n\n", part.label)
			for _, item := range items {
				switch part.section {
				case "permissions":
					fmt.Fprintf(&report, "- %s\n", item["condition"])
				case "steps":
					fmt.Fprintf(&report, "- %s\n", item["text"])
				case "funds":
					fmt.Fprintf(&report, "- 资产 %s；从 %s 到 %s；金额表达式 %s；条件 %s。\n", item["asset"], item["from_expression"], item["to_expression"], item["amount_expression"], strings.Join(stringList(item["conditions"]), "，"))
				case "storage":
					fmt.Fprintf(&report, "- %s %s；slot %v；表达式 %s。\n", item["access"], item["variable"], item["slot"], item["key_expression"])
				case "calls":
					fmt.Fprintf(&report, "- %s %s；%s。\n", item["call_type"], item["target_expression"], item["input_expression"])
				}
				fmt.Fprintf(&report, "  证据：%s\n", strings.Join(stringList(item["evidence_ids"]), "，"))
			}
			report.WriteString("\n")
		}
	}
	if e := s.contractAddArtifact(ctx, result, "analysis_report", "contract-analysis.md", "text/markdown; charset=utf-8", []byte(report.String())); e != nil {
		return e
	}
	if e := s.contractAddArtifact(ctx, result, "machine_result", "contract-analysis.json", "application/json", ca.Row(result)); e != nil {
		return e
	}
	// A deterministic package includes every original artifact and a SHA256 manifest.
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	entries := []map[string]any{}
	artifacts := map[string]ca.Artifact{}
	for _, raw := range result.Sections["artifacts"] {
		var a ca.Artifact
		json.Unmarshal(raw, &a)
		if old, ok := artifacts[a.ArtifactID]; ok && old.SHA256 != a.SHA256 {
			return fmt.Errorf("artifact identity conflict")
		}
		artifacts[a.ArtifactID] = a
	}
	ids := make([]string, 0, len(artifacts))
	for key := range artifacts {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	for _, key := range ids {
		a := artifacts[key]
		data, e := s.contractObject(ctx, ca.Object{Key: a.ObjectKey, SHA256: a.SHA256, Bytes: a.Bytes}, 64<<20)
		if e != nil {
			return e
		}
		name := "artifacts/" + a.ArtifactID + "/" + path.Base(strings.ReplaceAll(a.Filename, "\\", "/"))
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0600)
		writer, e := z.CreateHeader(header)
		if e != nil {
			return e
		}
		if _, e = writer.Write(data); e != nil {
			return e
		}
		entries = append(entries, map[string]any{"path": name, "sha256": a.SHA256, "byte_size": len(data), "artifact_id": a.ArtifactID})
		if archive.Len() > 64<<20 {
			return summaryError(422, "EXPORT_BUDGET_EXCEEDED", "Evidence package exceeds 64 MiB")
		}
	}
	manifest := ca.Row(map[string]any{"version": ca.Version, "snapshot": result.Snapshot, "input_version": frozen.Version, "entries": entries})
	writer, e := z.Create("manifest.json")
	if e != nil {
		return e
	}
	writer.Write(manifest)
	if e = z.Close(); e != nil {
		return e
	}
	return s.contractAddArtifact(ctx, result, "evidence_bundle", "contract-evidence.zip", "application/zip", archive.Bytes())
}
func (s *Server) contractCompare(ctx context.Context, q ca.Request) (any, error) {
	left, e := s.contractJob(ctx, q.AnalysisID)
	if e != nil {
		return nil, e
	}
	right, e := s.contractJob(ctx, q.CompareAnalysisID)
	if e != nil {
		return nil, e
	}
	if left.Status != "completed" || right.Status != "completed" {
		return nil, summaryError(409, "ANALYSIS_NOT_COMPLETED", "Both versions must be completed")
	}
	var lm, rm struct {
		Snapshot ca.Snapshot `json:"snapshot"`
	}
	if e = json.Unmarshal(left.Meta, &lm); e != nil {
		return nil, e
	}
	if e = json.Unmarshal(right.Meta, &rm); e != nil {
		return nil, e
	}
	if lm.Snapshot.ChainID != rm.Snapshot.ChainID || !strings.EqualFold(lm.Snapshot.EntryAddress, rm.Snapshot.EntryAddress) {
		return nil, fmt.Errorf("versions must name the same chain and entry address")
	}
	load := func(jobID string) (map[string]map[string]any, error) {
		rows, e := s.deps.Store.Pool.Query(ctx, `SELECT data FROM investigation_analysis_rows WHERE analysis_id=$1 AND section='methods' ORDER BY ordinal`, jobID)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := map[string]map[string]any{}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				return nil, e
			}
			m := rowMap(raw)
			key := fmt.Sprint(m["selector"])
			if key == "" {
				key = fmt.Sprint(m["signature"])
			}
			key = fmt.Sprint(m["code_address"]) + ":" + fmt.Sprint(m["storage_address"]) + ":" + key + ":" + fmt.Sprint(m["entry"])
			out[key] = m
		}
		return out, rows.Err()
	}
	a, e := load(left.ID)
	if e != nil {
		return nil, e
	}
	b, e := load(right.ID)
	if e != nil {
		return nil, e
	}
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	ordered := []string{}
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	binding := "compare:" + right.ID + ":" + right.ResultSHA
	after := int64(-1)
	if q.Cursor != "" {
		after, e = parseNextCursor(q.Cursor, left, binding)
		if e != nil {
			return nil, e
		}
	}
	changes := []any{}
	for _, key := range ordered {
		old, now := a[key], b[key]
		kind := "changed"
		if old == nil {
			kind = "added"
		}
		if now == nil {
			kind = "removed"
		}
		if old != nil && now != nil && old["code_hash"] == now["code_hash"] {
			continue
		}
		changes = append(changes, map[string]any{"identity": key, "change": kind, "before": old, "after": now})
	}
	loadState := func(jobID string) (map[string]map[string]any, error) {
		rows, err := s.deps.Store.Pool.Query(ctx, `SELECT data FROM investigation_analysis_rows WHERE analysis_id=$1 AND section='storage' AND data->>'snapshot_value' IS NOT NULL ORDER BY ordinal`, jobID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := map[string]map[string]any{}
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				return nil, err
			}
			item := rowMap(raw)
			key := fmt.Sprint(item["storage_address"]) + ":" + fmt.Sprint(item["slot"]) + ":" + fmt.Sprint(item["offset"]) + ":" + fmt.Sprint(item["type"])
			out[key] = item
		}
		return out, rows.Err()
	}
	beforeState, e := loadState(left.ID)
	if e != nil {
		return nil, e
	}
	afterState, e := loadState(right.ID)
	if e != nil {
		return nil, e
	}
	stateKeys := []string{}
	for k := range beforeState {
		if afterState[k] != nil {
			stateKeys = append(stateKeys, k)
		}
	}
	sort.Strings(stateKeys)
	for _, k := range stateKeys {
		before, after := beforeState[k], afterState[k]
		if before["snapshot_value"] != after["snapshot_value"] {
			changes = append(changes, map[string]any{"identity": k, "change": "configuration_value_changed", "before": before, "after": after})
		}
	}
	end := min(int(after+1)+q.PageSize, len(changes))
	start := min(int(after+1), len(changes))
	var cursor any
	if end < len(changes) {
		cursor = signNextCursor(nextCursor{left.ID, binding, left.ResultSHA, int64(end - 1)}, left.CursorKey)
	}
	return map[string]any{"analysis_id": left.ID, "compare_analysis_id": right.ID, "before_snapshot": lm.Snapshot, "after_snapshot": rm.Snapshot, "items": changes[start:end], "record_count": len(changes), "has_more": end < len(changes), "next_cursor": cursor, "comparison_basis": "code_identity_method_entry_and_observed_typed_configuration", "configuration_slots_compared": len(stateKeys)}, nil
}
