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
		return summaryError(422, "EXPORT_BUDGET_EXCEEDED", fmt.Sprintf("Contract export exceeds 64 MiB: file=%s bytes=%d", filename, len(data)))
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

func contractScopePropertyProof(data []byte, originalID string, remap map[string]string) ([]byte, error) {
	var proof map[string]any
	if err := nextDecode(data, &proof); err != nil {
		return nil, fmt.Errorf("invalid property proof: %w", err)
	}
	property, propertyOK := proof["property"].(map[string]any)
	query, queryOK := proof["query"].(map[string]any)
	propertyID, propertyIDOK := property["property_id"].(string)
	queryID, queryIDOK := query["property_id"].(string)
	if proof["schema_version"] != "contract-property-proof-v1" || !propertyOK || !queryOK ||
		property["proof_artifact_id"] != originalID || remap[originalID] == "" ||
		!propertyIDOK || !queryIDOK || propertyID == "" || propertyID != queryID {
		return nil, fmt.Errorf("property proof identity mismatch")
	}
	// Apply the same scoped references as the published rows, after checking the
	// engine's original bytes. Source files and other opaque artifacts stay intact.
	for oldID, newID := range remap {
		data = bytes.ReplaceAll(data, []byte(`"`+oldID+`"`), []byte(`"`+newID+`"`))
	}
	return data, nil
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
			title = contractReportField(m, "selector", "name", "entry", "method_id")
		}
		fmt.Fprintf(&report, "## %s\n\n代码地址：%s\n\n状态地址：%s\n\n%s。\n\n", title, m["code_address"], m["storage_address"], m["purpose"])
		for _, key := range []string{"parameters", "outputs"} {
			var values []map[string]any
			if nextDecode(ca.Row(m[key]), &values) == nil && len(values) > 0 {
				label := "参数"
				if key == "outputs" {
					label = "返回字段"
				}
				fmt.Fprintf(&report, "### %s\n\n", label)
				for _, p := range values {
					fmt.Fprintf(&report, "- %s：%s；单位 %s。\n", contractReportField(p, "name", "index"), contractReportField(p, "type"), contractReportField(p, "unit", "unit_status"))
				}
				report.WriteString("\n")
			}
		}
		for _, part := range []struct{ section, label string }{{"permissions", "执行条件"}, {"steps", "执行步骤"}, {"funds", "资金作用"}, {"dataflow", "表达式推导"}, {"storage", "状态读写"}, {"control_effects", "配置与资金关联"}, {"calls", "外部调用"}, {"external_returns", "外部返回依赖"}, {"external_return_observations", "执行返回结果"}, {"property_obligations", "性质验证"}} {
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
					fmt.Fprintf(&report, "- %s\n", contractReportField(item, "expanded_condition", "condition"))
					var constraints []map[string]any
					if nextDecode(ca.Row(item["caller_constraints"]), &constraints) == nil {
						for _, c := range constraints {
							fmt.Fprintf(&report, "  调用条件：%s 等于 %s；已取得地址 %s；条件范围 %s；状态 %s。\n", c["principal"], c["target_expression"], contractReportField(c, "snapshot_target_address"), c["condition_scope"], contractReportField(c, "snapshot_resolution_status", "target_value_resolution"))
						}
					}
					var predicates []map[string]any
					if nextDecode(ca.Row(item["state_predicates"]), &predicates) == nil {
						for _, p := range predicates {
							fmt.Fprintf(&report, "  状态条件：%s；要求结果 %v；条件范围 %s；读取状态 %s。\n", p["expression"], p["expected"], p["condition_scope"], strings.Join(stringList(p["state_read_ids"]), "，"))
						}
					}
				case "steps":
					fmt.Fprintf(&report, "- %s\n", item["text"])
				case "funds":
					fmt.Fprintf(&report, "- 资产 %s；从 %s 到 %s；金额表达式 %s；单位 %s；条件 %s。\n", contractReportField(item, "expanded_asset_expression", "asset"), contractReportField(item, "expanded_from_expression", "from_expression"), contractReportField(item, "expanded_to_expression", "to_expression"), contractReportField(item, "expanded_amount_expression", "amount_expression"), contractReportField(item, "unit"), strings.Join(stringList(item["conditions"]), "，"))
					if item["token_id_expression"] != nil {
						fmt.Fprintf(&report, "  资产编号：%s。\n", item["token_id_expression"])
					}
				case "dataflow":
					fmt.Fprintf(&report, "- %s：%s；推导状态 %s。\n", item["kind"], item["symbolic_expression"], item["status"])
					if gaps := stringList(item["unresolved"]); len(gaps) > 0 {
						fmt.Fprintf(&report, "  待解析内容：%s。\n", strings.Join(gaps, "，"))
					}
					var paths []map[string]any
					if nextDecode(ca.Row(item["successful_return_paths"]), &paths) == nil {
						for _, p := range paths {
							fmt.Fprintf(&report, "  返回路径：%s；表达式 %s。\n", strings.Join(stringList(p["conditions"]), "，"), p["value"])
							var evaluations []map[string]any
							if nextDecode(ca.Row(p["execution_steps"]), &evaluations) == nil {
								for index, step := range evaluations {
									if step["kind"] == "guard" {
										fmt.Fprintf(&report, "  顺序 %d：检查 %s；要求结果 %v。\n", index+1, step["expression"], step["expected"])
									} else {
										fmt.Fprintf(&report, "  顺序 %d：执行计算 %s。\n", index+1, step["expression"])
									}
								}
							}
						}
					}
					var loops []map[string]any
					if nextDecode(ca.Row(item["loops"]), &loops) == nil {
						for _, loop := range loops {
							fmt.Fprintf(&report, "  已展开循环：AST %v；执行 %v 次；边界依据 %s。\n", loop["ast_id"], loop["iterations"], loop["bound_basis"])
						}
					}
				case "control_effects":
					fmt.Fprintf(&report, "- %s；修改 %s；新值 %s；影响类型 %s；受影响方法 %s。\n", item["interpretation"], item["configuration_key"], item["new_value_expression"], strings.Join(stringList(item["impact_types"]), "，"), strings.Join(stringList(item["affected_method_ids"]), "，"))
				case "storage":
					fmt.Fprintf(&report, "- %s %s；slot %v；表达式 %s。\n", item["access"], item["variable"], item["slot"], item["key_expression"])
					if item["snapshot_value"] != nil {
						fmt.Fprintf(&report, "  快照值：%v；状态槽类型：%v。\n", item["snapshot_value"], item["slot_role"])
					}
				case "calls":
					fmt.Fprintf(&report, "- %s %s；%s。\n", item["call_type"], item["target_expression"], item["input_expression"])
					if item["resolved_target_address"] != nil {
						fmt.Fprintf(&report, "  解析目标：%s；状态地址 %s；代码哈希 %s；依赖状态 %s。\n", item["resolved_target_address"], contractReportField(item, "dependency_storage_address"), contractReportField(item, "dependency_code_hash"), contractReportField(item, "dependency_status"))
					}
				case "external_returns":
					fmt.Fprintf(&report, "- %s；目标 %s；返回类型 %s；调用者 %s；状态 %s。\n", item["signature"], item["target_expression"], strings.Join(stringList(item["return_types"]), "，"), item["callee_msg_sender"], item["status"])
				case "external_return_observations":
					fmt.Fprintf(&report, "- 交易 %s；调用路径 %s；代码地址 %s；状态地址 %s；完整返回数据 %s；状态 %s。\n", item["tx_hash"], item["trace_path"], item["code_address"], item["storage_address"], item["return_data"], item["status"])
				case "property_obligations":
					fmt.Fprintf(&report, "- %s；命题 %s；状态 %s；假设 %s；证明文件 %s。\n", item["property"], contractReportField(item, "proposition", "amount_expression"), item["status"], strings.Join(stringList(item["assumptions"]), "，"), contractReportField(item, "proof_artifact_id"))
					if item["counterexample"] != nil {
						fmt.Fprintf(&report, "  反例：%s\n", ca.Row(item["counterexample"]))
					}
				}
				fmt.Fprintf(&report, "  证据：%s\n", strings.Join(stringList(item["evidence_ids"]), "，"))
			}
			report.WriteString("\n")
		}
	}
	if len(result.Sections["protocols"]) > 0 {
		report.WriteString("## 协议方法计算\n\n")
		for _, raw := range result.Sections["protocols"] {
			p := rowMap(raw)
			fmt.Fprintf(&report, "### %s\n\n计算分析状态：%s\n\n", p["profile"], contractReportField(p, "economic_model_status"))
			var models []map[string]any
			if nextDecode(ca.Row(p["method_models"]), &models) == nil {
				for _, model := range models {
					fmt.Fprintf(&report, "- %s；公式状态 %s。\n", model["signature"], model["formula_status"])
					var formulas []map[string]any
					if nextDecode(ca.Row(model["return_formulas"]), &formulas) == nil {
						for _, f := range formulas {
							fmt.Fprintf(&report, "  返回公式：%s；状态 %s。\n", f["expression"], f["status"])
						}
					}
					fmt.Fprintf(&report, "  证据：%s\n", strings.Join(stringList(model["evidence_ids"]), "，"))
				}
			}
			report.WriteString("\n")
		}
	}
	if report.Len() > 16<<20 {
		report.Reset()
		fmt.Fprintf(&report, "# %s %s 合约运行与方法分析\n\n区块：%d\n\n区块哈希：%s\n\n", result.Snapshot.Chain, result.Snapshot.EntryAddress, result.Snapshot.BlockNumber, result.Snapshot.BlockHash)
		report.WriteString("## 方法与完整数据\n\n")
		for _, raw := range result.Sections["methods"] {
			m := rowMap(raw)
			fmt.Fprintf(&report, "- %s；代码地址 %s；状态地址 %s；方法标识 %s。\n", contractReportField(m, "signature", "selector", "name", "entry"), m["code_address"], m["storage_address"], m["method_id"])
		}
		report.WriteString("\n## 数据文件\n\n完整条件、步骤、公式、状态及证据保存于结果分区文件；contract-analysis.json 列出全部文件、记录数及校验值。\n")
	}
	if e := s.contractAddArtifact(ctx, result, "analysis_report", "contract-analysis.md", "text/markdown; charset=utf-8", []byte(report.String())); e != nil {
		return e
	}
	if e := s.contractMachineArtifact(ctx, result); e != nil {
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

func (s *Server) contractMachineArtifact(ctx context.Context, result *ca.Result) error {
	raw := ca.Row(result)
	if len(raw) <= 32<<20 {
		return s.contractAddArtifact(ctx, result, "machine_result", "contract-analysis.json", "application/json", raw)
	}
	names := make([]string, 0, len(result.Sections))
	for name := range result.Sections {
		if name != "artifacts" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	index := map[string][]map[string]any{}
	for _, name := range names {
		rows := result.Sections[name]
		part := 0
		start := 0
		for start < len(rows) {
			end := start
			size := 2
			for end < len(rows) && size+len(rows[end])+1 <= 16<<20 {
				size += len(rows[end]) + 1
				end++
			}
			if end == start {
				return fmt.Errorf("section export row exceeds chunk budget")
			}
			data := ca.Row(rows[start:end])
			filename := fmt.Sprintf("sections/%s-%04d.json", name, part)
			if err := s.contractAddArtifact(ctx, result, "result_section", filename, "application/json", data); err != nil {
				return err
			}
			artifact := rowMap(result.Sections["artifacts"][len(result.Sections["artifacts"])-1])
			index[name] = append(index[name], map[string]any{"artifact_id": artifact["artifact_id"], "filename": filename, "sha256": artifact["sha256"], "byte_size": len(data), "from_ordinal": start, "record_count": end - start})
			part++
			start = end
		}
		if len(rows) == 0 {
			index[name] = []map[string]any{}
		}
	}
	data := ca.Row(map[string]any{"version": result.Version, "rule_version": result.RuleVersion, "snapshot": result.Snapshot, "metrics": result.Metrics, "representation": "complete_section_artifacts_v1", "section_artifacts": index, "artifacts": result.Sections["artifacts"]})
	return s.contractAddArtifact(ctx, result, "machine_result", "contract-analysis.json", "application/json", data)
}

func contractReportField(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := row[key]; value != nil && fmt.Sprint(value) != "" {
			return fmt.Sprint(value)
		}
	}
	return "未确定"
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
	if lm.Snapshot.ChainID != rm.Snapshot.ChainID || lm.Snapshot.VMFamily != rm.Snapshot.VMFamily || (lm.Snapshot.Chain == "solana" && lm.Snapshot.EntryAddress != rm.Snapshot.EntryAddress) || (lm.Snapshot.Chain != "solana" && !strings.EqualFold(lm.Snapshot.EntryAddress, rm.Snapshot.EntryAddress)) {
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
