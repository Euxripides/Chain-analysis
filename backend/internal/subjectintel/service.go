// Package subjectintel stores evidence-bounded off-chain subjects and their
// relationships without coupling them to the graph renderer or a provider.
package subjectintel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var validTypes = map[string]bool{"ADDRESS": true, "DOMAIN": true, "IP": true, "SOCIAL_ACCOUNT": true, "APK": true, "ORGANIZATION": true, "FILE": true, "CONTRACT": true, "TRANSACTION": true}
var validSources = map[string]bool{"SYSTEM": true, "USER": true, "PROVIDER": true, "OSINT": true, "HEURISTIC": true, "AGENT": true, "CASE": true}

type Service struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type Subject struct {
	ID              string         `json:"id"`
	InvestigationID string         `json:"investigation_id"`
	Type            string         `json:"type"`
	Value           string         `json:"value"`
	Label           string         `json:"label"`
	Source          string         `json:"source"`
	Confidence      float64        `json:"confidence"`
	EvidenceRefs    []string       `json:"evidence_refs"`
	Provenance      map[string]any `json:"provenance"`
	CreatedAt       time.Time      `json:"created_at"`
}
type Relationship struct {
	ID              string         `json:"id"`
	InvestigationID string         `json:"investigation_id"`
	SourceSubjectID string         `json:"source_subject_id"`
	TargetSubjectID string         `json:"target_subject_id"`
	Type            string         `json:"type"`
	Source          string         `json:"source"`
	Confidence      float64        `json:"confidence"`
	EvidenceRefs    []string       `json:"evidence_refs"`
	Provenance      map[string]any `json:"provenance"`
	CreatedAt       time.Time      `json:"created_at"`
}
type CreateSubject struct {
	Type         string         `json:"type"`
	Value        string         `json:"value"`
	Label        string         `json:"label"`
	Source       string         `json:"source"`
	Confidence   float64        `json:"confidence"`
	EvidenceRefs []string       `json:"evidence_refs"`
	Provenance   map[string]any `json:"provenance"`
}
type CreateRelationship struct {
	SourceSubjectID string         `json:"source_subject_id"`
	TargetSubjectID string         `json:"target_subject_id"`
	Type            string         `json:"type"`
	Source          string         `json:"source"`
	Confidence      float64        `json:"confidence"`
	EvidenceRefs    []string       `json:"evidence_refs"`
	Provenance      map[string]any `json:"provenance"`
}

func (s *Service) AddSubject(ctx context.Context, investigationID string, in CreateSubject) (Subject, error) {
	in.Type = strings.ToUpper(strings.TrimSpace(in.Type))
	in.Source = strings.ToUpper(strings.TrimSpace(in.Source))
	in.Value = strings.TrimSpace(in.Value)
	if err := validateSubject(in); err != nil {
		return Subject{}, err
	}
	if in.Provenance == nil {
		in.Provenance = map[string]any{}
	}
	refs, _ := json.Marshal(in.EvidenceRefs)
	prov, _ := json.Marshal(in.Provenance)
	var out Subject
	out.ID = uuid.NewString()
	out.InvestigationID = investigationID
	err := s.pool.QueryRow(ctx, `INSERT INTO investigation_subjects(id,investigation_id,subject_type,subject_value,label,source,confidence,evidence_refs,provenance) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(investigation_id,subject_type,subject_value) DO UPDATE SET label=EXCLUDED.label,source=EXCLUDED.source,confidence=EXCLUDED.confidence,evidence_refs=EXCLUDED.evidence_refs,provenance=EXCLUDED.provenance RETURNING id::text,subject_type,subject_value,label,source,confidence,evidence_refs,provenance,created_at`, out.ID, investigationID, in.Type, in.Value, in.Label, in.Source, in.Confidence, refs, prov).Scan(&out.ID, &out.Type, &out.Value, &out.Label, &out.Source, &out.Confidence, &refs, &prov, &out.CreatedAt)
	if err != nil {
		return Subject{}, err
	}
	_ = json.Unmarshal(refs, &out.EvidenceRefs)
	_ = json.Unmarshal(prov, &out.Provenance)
	return out, nil
}
func (s *Service) AddRelationship(ctx context.Context, investigationID string, in CreateRelationship) (Relationship, error) {
	in.Type = strings.ToUpper(strings.TrimSpace(in.Type))
	in.Source = strings.ToUpper(strings.TrimSpace(in.Source))
	if in.Source == "" {
		in.Source = "USER"
	}
	if in.Type == "" || in.SourceSubjectID == "" || in.TargetSubjectID == "" || in.SourceSubjectID == in.TargetSubjectID {
		return Relationship{}, errors.New("distinct source, target and relationship type are required")
	}
	if !validSources[in.Source] || in.Confidence < 0 || in.Confidence > 1 {
		return Relationship{}, errors.New("invalid source or confidence")
	}
	if in.Provenance == nil {
		in.Provenance = map[string]any{}
	}
	var owned int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM investigation_subjects WHERE investigation_id=$1 AND id IN ($2,$3)`, investigationID, in.SourceSubjectID, in.TargetSubjectID).Scan(&owned); err != nil || owned != 2 {
		if err == nil {
			err = errors.New("subjects must belong to the investigation")
		}
		return Relationship{}, err
	}
	refs, _ := json.Marshal(in.EvidenceRefs)
	prov, _ := json.Marshal(in.Provenance)
	var out Relationship
	out.ID = uuid.NewString()
	out.InvestigationID = investigationID
	err := s.pool.QueryRow(ctx, `INSERT INTO entity_relationships(id,investigation_id,source_subject_id,target_subject_id,relation_type,source,confidence,evidence_refs,provenance) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(investigation_id,source_subject_id,target_subject_id,relation_type) DO UPDATE SET source=EXCLUDED.source,confidence=EXCLUDED.confidence,evidence_refs=EXCLUDED.evidence_refs,provenance=EXCLUDED.provenance RETURNING id::text,source_subject_id::text,target_subject_id::text,relation_type,source,confidence,evidence_refs,provenance,created_at`, out.ID, investigationID, in.SourceSubjectID, in.TargetSubjectID, in.Type, in.Source, in.Confidence, refs, prov).Scan(&out.ID, &out.SourceSubjectID, &out.TargetSubjectID, &out.Type, &out.Source, &out.Confidence, &refs, &prov, &out.CreatedAt)
	if err != nil {
		return Relationship{}, err
	}
	_ = json.Unmarshal(refs, &out.EvidenceRefs)
	_ = json.Unmarshal(prov, &out.Provenance)
	return out, nil
}
func (s *Service) List(ctx context.Context, id string) ([]Subject, []Relationship, error) {
	subjects := []Subject{}
	rows, err := s.pool.Query(ctx, `SELECT id::text,investigation_id::text,subject_type,subject_value,label,source,confidence,evidence_refs,provenance,created_at FROM investigation_subjects WHERE investigation_id=$1 ORDER BY created_at`, id)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var x Subject
		var refs, prov []byte
		if err = rows.Scan(&x.ID, &x.InvestigationID, &x.Type, &x.Value, &x.Label, &x.Source, &x.Confidence, &refs, &prov, &x.CreatedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		_ = json.Unmarshal(refs, &x.EvidenceRefs)
		_ = json.Unmarshal(prov, &x.Provenance)
		subjects = append(subjects, x)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	rels := []Relationship{}
	rows, err = s.pool.Query(ctx, `SELECT id::text,investigation_id::text,source_subject_id::text,target_subject_id::text,relation_type,source,confidence,evidence_refs,provenance,created_at FROM entity_relationships WHERE investigation_id=$1 ORDER BY created_at`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x Relationship
		var refs, prov []byte
		if err = rows.Scan(&x.ID, &x.InvestigationID, &x.SourceSubjectID, &x.TargetSubjectID, &x.Type, &x.Source, &x.Confidence, &refs, &prov, &x.CreatedAt); err != nil {
			return nil, nil, err
		}
		_ = json.Unmarshal(refs, &x.EvidenceRefs)
		_ = json.Unmarshal(prov, &x.Provenance)
		rels = append(rels, x)
	}
	return subjects, rels, rows.Err()
}
func validateSubject(in CreateSubject) error {
	if !validTypes[in.Type] {
		return fmt.Errorf("unsupported subject type %s", in.Type)
	}
	if in.Value == "" {
		return errors.New("subject value is required")
	}
	if !validSources[in.Source] {
		return fmt.Errorf("unsupported source %s", in.Source)
	}
	if in.Confidence < 0 || in.Confidence > 1 {
		return errors.New("confidence must be between 0 and 1")
	}
	return nil
}
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
