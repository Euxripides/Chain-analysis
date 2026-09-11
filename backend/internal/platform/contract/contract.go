// Package contract defines the versioned, provider-neutral contracts shared by
// the control, intelligence, and data planes. These types are intentionally
// implementation-free: business modules may depend on them, adapters may
// implement them, and neither side needs to import the other's internals.
package contract

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const Version = "1.0"

type SubjectRef struct {
	Type      string            `json:"type"`
	Chain     string            `json:"chain,omitempty"`
	Value     string            `json:"value"`
	Namespace string            `json:"namespace,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func (s SubjectRef) Validate() error {
	if strings.TrimSpace(s.Type) == "" || strings.TrimSpace(s.Value) == "" {
		return fmt.Errorf("subject type and value are required")
	}
	if s.Type == "address" && strings.TrimSpace(s.Chain) == "" {
		return fmt.Errorf("address subject requires chain")
	}
	return nil
}

type RequestContext struct {
	RequestID       string `json:"request_id"`
	TraceID         string `json:"trace_id"`
	OrganizationID  string `json:"organization_id,omitempty"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
	CaseID          string `json:"case_id,omitempty"`
	InvestigationID string `json:"investigation_id,omitempty"`
	JobID           string `json:"job_id,omitempty"`
	ActorID         string `json:"actor_id,omitempty"`
}

type DataRequirement struct {
	ID          string     `json:"id"`
	Subject     SubjectRef `json:"subject"`
	Datasets    []string   `json:"datasets"`
	FromBlock   *int64     `json:"from_block,omitempty"`
	ToBlock     *int64     `json:"to_block,omitempty"`
	FromTime    *time.Time `json:"from_time,omitempty"`
	ToTime      *time.Time `json:"to_time,omitempty"`
	Freshness   string     `json:"freshness,omitempty"`
	RequestedBy string     `json:"requested_by,omitempty"`
	Version     string     `json:"version"`
}

type SemanticEvent struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Version    string          `json:"version"`
	Chain      string          `json:"chain,omitempty"`
	Subjects   []SubjectRef    `json:"subjects,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
	Provenance Provenance      `json:"provenance"`
}

type Provenance struct {
	SourceType string    `json:"source_type"`
	SourceRefs []string  `json:"source_refs,omitempty"`
	Processor  string    `json:"processor,omitempty"`
	Version    string    `json:"version,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
}

type GraphNode struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Subject    *SubjectRef    `json:"subject,omitempty"`
	Label      string         `json:"label,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
	Version    string         `json:"version"`
}

type GraphEdge struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Source      string         `json:"source"`
	Target      string         `json:"target"`
	Directed    bool           `json:"directed"`
	Weight      float64        `json:"weight,omitempty"`
	Properties  map[string]any `json:"properties,omitempty"`
	EvidenceIDs []string       `json:"evidence_ids,omitempty"`
	Version     string         `json:"version"`
}

type GraphPatch struct {
	ID            string      `json:"id"`
	CanvasID      string      `json:"canvas_id"`
	BaseRevision  int64       `json:"base_revision"`
	Revision      int64       `json:"revision"`
	UpsertNodes   []GraphNode `json:"upsert_nodes,omitempty"`
	UpsertEdges   []GraphEdge `json:"upsert_edges,omitempty"`
	RemoveNodeIDs []string    `json:"remove_node_ids,omitempty"`
	RemoveEdgeIDs []string    `json:"remove_edge_ids,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
	Version       string      `json:"version"`
}

type Finding struct {
	ID               string         `json:"id"`
	InvestigationID  string         `json:"investigation_id"`
	Type             string         `json:"type"`
	Title            string         `json:"title"`
	Summary          string         `json:"summary"`
	Confidence       float64        `json:"confidence"`
	Status           string         `json:"status"`
	EvidenceIDs      []string       `json:"evidence_ids"`
	Properties       map[string]any `json:"properties,omitempty"`
	Algorithm        string         `json:"algorithm,omitempty"`
	AlgorithmVersion string         `json:"algorithm_version,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	Version          string         `json:"version"`
}

type Evidence struct {
	ID              string     `json:"id"`
	InvestigationID string     `json:"investigation_id"`
	Type            string     `json:"type"`
	Title           string     `json:"title"`
	SourceRefs      []string   `json:"source_refs"`
	ContentHash     string     `json:"content_hash,omitempty"`
	ObjectRef       string     `json:"object_ref,omitempty"`
	Provenance      Provenance `json:"provenance"`
	CreatedAt       time.Time  `json:"created_at"`
	Version         string     `json:"version"`
}

type Capability struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Inputs      []string `json:"inputs,omitempty"`
	Outputs     []string `json:"outputs,omitempty"`
	Async       bool     `json:"async"`
	Available   bool     `json:"available"`
}

type Job struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Status         string         `json:"status"`
	Priority       int            `json:"priority"`
	ResourceClass  string         `json:"resource_class"`
	Attempt        int            `json:"attempt"`
	MaxAttempts    int            `json:"max_attempts"`
	Checkpoint     map[string]any `json:"checkpoint,omitempty"`
	RequestContext RequestContext `json:"request_context"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Version        string         `json:"version"`
}

type EventEnvelope struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Version        string          `json:"version"`
	AggregateType  string          `json:"aggregate_type"`
	AggregateID    string          `json:"aggregate_id"`
	Sequence       int64           `json:"sequence"`
	OccurredAt     time.Time       `json:"occurred_at"`
	RequestContext RequestContext  `json:"request_context"`
	Payload        json.RawMessage `json:"payload"`
}

var FrozenTypes = []string{
	"SubjectRef", "ChainAdapter", "DataRequirement", "SemanticEvent",
	"GraphNode", "GraphEdge", "GraphPatch", "Finding", "Evidence",
	"Capability", "Job", "EventEnvelope", "RequestContext",
}
