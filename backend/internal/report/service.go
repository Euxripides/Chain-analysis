// Package report creates deterministic, evidence-indexed investigation reports.
package report

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const Bucket = "blockchain-reports"
const AlgorithmVersion = "evidence-report-v1.0"

type Service struct {
	pool    *pgxpool.Pool
	objects *rawstore.Store
}

func New(pool *pgxpool.Pool, objects *rawstore.Store) *Service {
	return &Service{pool: pool, objects: objects}
}

type Report struct {
	ID              string         `json:"id"`
	InvestigationID string         `json:"investigation_id"`
	Revision        int64          `json:"revision"`
	Format          string         `json:"format"`
	ObjectKey       string         `json:"object_key"`
	ContentHash     string         `json:"content_hash"`
	SizeBytes       int64          `json:"size_bytes"`
	Metadata        map[string]any `json:"metadata"`
	GeneratedAt     time.Time      `json:"generated_at"`
}

func (s *Service) Generate(ctx context.Context, investigationID string) (Report, error) {
	return s.GenerateFor(ctx, investigationID, "")
}

// GenerateFor 生成新的不可变报告 revision。generatedBy 来自服务端会话，绝不接受客户端。
// 0044 起 investigation_reports 的 organization_id/workspace_id 为 NOT NULL，
// 因此生成时从 investigation 推导作用域并一并写入。
func (s *Service) GenerateFor(ctx context.Context, investigationID, generatedBy string) (Report, error) {
	var goal, status, organizationID, workspaceID string
	var caseID *string
	var subjectRaw []byte
	if err := s.pool.QueryRow(ctx, `SELECT goal,status,subject,organization_id::text,workspace_id::text,case_id::text FROM investigations WHERE id=$1`, investigationID).Scan(&goal, &status, &subjectRaw, &organizationID, &workspaceID, &caseID); err != nil {
		return Report{}, err
	}
	var subject map[string]any
	_ = json.Unmarshal(subjectRaw, &subject)
	findings, err := s.findings(ctx, investigationID)
	if err != nil {
		return Report{}, err
	}
	evidence, err := s.evidence(ctx, investigationID)
	if err != nil {
		return Report{}, err
	}
	var patchCount, snapshotCount int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM graph_patches WHERE investigation_id=$1`, investigationID).Scan(&patchCount)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM canvas_snapshots cs JOIN investigation_canvases c ON c.id=cs.canvas_id WHERE c.investigation_id=$1`, investigationID).Scan(&snapshotCount)
	generated := time.Now().UTC()
	body := render(goal, status, subject, findings, evidence, patchCount, snapshotCount, generated)
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	id := uuid.NewString()
	var revision int64
	if err = s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM investigation_reports WHERE investigation_id=$1`, investigationID).Scan(&revision); err != nil {
		return Report{}, err
	}
	key := fmt.Sprintf("investigations/%s/report-r%d-%s.md", investigationID, revision, hash[:12])
	size, err := s.objects.PutNamed(ctx, Bucket, key, body, "text/markdown; charset=utf-8")
	if err != nil {
		return Report{}, err
	}
	metadata := map[string]any{"algorithm_version": AlgorithmVersion, "finding_count": len(findings), "evidence_count": len(evidence), "graph_patch_count": patchCount, "graph_snapshot_count": snapshotCount, "generated_from": "durable_control_plane"}
	meta, _ := json.Marshal(metadata)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var created time.Time
	err = tx.QueryRow(ctx, `INSERT INTO investigation_reports
		(id,investigation_id,revision,format,object_key,content_hash,size_bytes,metadata,organization_id,workspace_id,case_id,generated_by,status)
		VALUES($1,$2,$3,'markdown',$4,$5,$6,$7,$8::uuid,$9::uuid,$10::uuid,NULLIF($11,'')::uuid,'generated') RETURNING generated_at`,
		id, investigationID, revision, key, hash, size, meta, organizationID, workspaceID, caseID, generatedBy).Scan(&created)
	if err != nil {
		return Report{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,action,resource_type,resource_id,detail) VALUES($1,'investigation.report.generate','investigation',$2,$3)`, uuid.NewString(), investigationID, meta)
	if err != nil {
		return Report{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return Report{ID: id, InvestigationID: investigationID, Revision: revision, Format: "markdown", ObjectKey: key, ContentHash: hash, SizeBytes: size, Metadata: metadata, GeneratedAt: created}, nil
}

func (s *Service) List(ctx context.Context, investigationID string) ([]Report, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,investigation_id::text,revision,format,object_key,content_hash,size_bytes,metadata,generated_at FROM investigation_reports WHERE investigation_id=$1 ORDER BY revision DESC`, investigationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Report{}
	for rows.Next() {
		var r Report
		var meta []byte
		if err = rows.Scan(&r.ID, &r.InvestigationID, &r.Revision, &r.Format, &r.ObjectKey, &r.ContentHash, &r.SizeBytes, &meta, &r.GeneratedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(meta, &r.Metadata)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Verify 重新读取对象并校验 SHA-256 / 长度；artifact 与 DB 元数据不一致时拒绝审批。
func (s *Service) Verify(ctx context.Context, id string) (Report, error) {
	reader, report, err := s.Open(ctx, id)
	if err != nil {
		return Report{}, err
	}
	defer reader.Close()
	h := sha256.New()
	n, err := io.Copy(h, reader)
	if err != nil {
		return Report{}, err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != report.ContentHash {
		return Report{}, fmt.Errorf("report %s content hash mismatch", id)
	}
	if report.SizeBytes > 0 && n != report.SizeBytes {
		return Report{}, fmt.Errorf("report %s size mismatch", id)
	}
	return report, nil
}

// ReadAll 读取完整 artifact（报告体积受控，Range/HEAD 由 HTTP 层处理）。
func (s *Service) ReadAll(ctx context.Context, id string) ([]byte, Report, error) {
	reader, report, err := s.Open(ctx, id)
	if err != nil {
		return nil, Report{}, err
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, Report{}, err
	}
	return body, report, nil
}

func (s *Service) Open(ctx context.Context, id string) (io.ReadCloser, Report, error) {
	var r Report
	var meta []byte
	err := s.pool.QueryRow(ctx, `SELECT id::text,investigation_id::text,revision,format,object_key,content_hash,size_bytes,metadata,generated_at FROM investigation_reports WHERE id=$1`, id).Scan(&r.ID, &r.InvestigationID, &r.Revision, &r.Format, &r.ObjectKey, &r.ContentHash, &r.SizeBytes, &meta, &r.GeneratedAt)
	if err != nil {
		return nil, Report{}, err
	}
	_ = json.Unmarshal(meta, &r.Metadata)
	reader, size, err := s.objects.OpenNamed(ctx, Bucket, r.ObjectKey)
	if err != nil {
		return nil, Report{}, err
	}
	r.SizeBytes = size
	return reader, r, nil
}

func (s *Service) findings(ctx context.Context, id string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT finding_type,title,summary,confidence,status,algorithm,algorithm_version,created_at FROM investigation_findings WHERE investigation_id=$1 ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var kind, title, summary, status, algorithm, version string
		var confidence float64
		var created time.Time
		if err = rows.Scan(&kind, &title, &summary, &confidence, &status, &algorithm, &version, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"type": kind, "title": title, "summary": summary, "confidence": confidence, "status": status, "algorithm": algorithm, "algorithm_version": version, "created_at": created})
	}
	return out, rows.Err()
}
func (s *Service) evidence(ctx context.Context, id string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT evidence_type,title,source_refs,content_hash,provenance,created_at FROM investigation_evidence WHERE investigation_id=$1 ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var kind, title, hash string
		var refsRaw, provRaw []byte
		var created time.Time
		if err = rows.Scan(&kind, &title, &refsRaw, &hash, &provRaw, &created); err != nil {
			return nil, err
		}
		var refs []string
		var prov map[string]any
		_ = json.Unmarshal(refsRaw, &refs)
		_ = json.Unmarshal(provRaw, &prov)
		out = append(out, map[string]any{"type": kind, "title": title, "source_refs": refs, "content_hash": hash, "provenance": prov, "created_at": created})
	}
	return out, rows.Err()
}

func render(goal, status string, subject map[string]any, findings, evidence []map[string]any, patches, snapshots int, generated time.Time) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# 调查报告\n\n- 调查目标：%s\n- 主体：%v / %v\n- 状态：%s\n- 生成时间：%s\n- 报告算法：%s\n\n", goal, subject["chain"], subject["value"], status, generated.Format(time.RFC3339), AlgorithmVersion)
	fmt.Fprintf(&b, "## 摘要\n\n共固化 %d 条 Finding、%d 组 Evidence、%d 个 Graph Patch、%d 个 Graph Snapshot。\n\n", len(findings), len(evidence), patches, snapshots)
	b.WriteString("## Findings\n\n")
	for i, f := range findings {
		fmt.Fprintf(&b, "### %d. %v\n\n- 状态：%v\n- 置信度：%.0f%%\n- 算法：%v@%v\n\n%v\n\n", i+1, f["title"], f["status"], f["confidence"].(float64)*100, f["algorithm"], f["algorithm_version"], f["summary"])
	}
	types := map[string]int{}
	for _, e := range evidence {
		types[fmt.Sprint(e["type"])]++
	}
	names := make([]string, 0, len(types))
	for n := range types {
		names = append(names, n)
	}
	sort.Strings(names)
	b.WriteString("## Evidence Index\n\n")
	for _, n := range names {
		fmt.Fprintf(&b, "- %s：%d\n", n, types[n])
	}
	b.WriteString("\n## 证据边界\n\n- 候选与推断不等于已核实身份或共同控制。\n- 代币净流量不等于已实现利润；利润结论需要价格与成本基础。\n- 本报告只引用生成时已持久化的 Finding、Evidence、Graph Patch 与 Snapshot。\n")
	return []byte(b.String())
}
