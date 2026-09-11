// Package canvas owns durable, case-scoped graph workspaces.
package canvas

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

var ErrNotFound = errors.New("canvas not found")
var ErrRevisionConflict = errors.New("canvas revision conflict")

type Canvas struct {
	ID              string           `json:"id"`
	OrganizationID  string           `json:"organization_id"`
	WorkspaceID     string           `json:"workspace_id"`
	CaseID          *string          `json:"case_id,omitempty"`
	InvestigationID *string          `json:"investigation_id,omitempty"`
	ParentCanvasID  *string          `json:"parent_canvas_id,omitempty"`
	Name            string           `json:"name"`
	NodeRefs        []string         `json:"node_refs"`
	EdgeRefs        []string         `json:"edge_refs"`
	Groups          []map[string]any `json:"groups"`
	Annotations     []map[string]any `json:"annotations"`
	Filters         map[string]any   `json:"filters"`
	Layout          map[string]any   `json:"layout"`
	Camera          map[string]any   `json:"camera"`
	ViewState       map[string]any   `json:"view_state"`
	Revision        int64            `json:"revision"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

type State struct {
	NodeRefs    []string         `json:"node_refs"`
	EdgeRefs    []string         `json:"edge_refs"`
	Groups      []map[string]any `json:"groups"`
	Annotations []map[string]any `json:"annotations"`
	Filters     map[string]any   `json:"filters"`
	Layout      map[string]any   `json:"layout"`
	Camera      map[string]any   `json:"camera"`
	ViewState   map[string]any   `json:"view_state"`
}

type CreateRequest struct {
	Name            string  `json:"name"`
	InvestigationID *string `json:"investigation_id,omitempty"`
	ParentCanvasID  *string `json:"parent_canvas_id,omitempty"`
	State
}
type UpdateRequest struct {
	Name *string `json:"name,omitempty"`
	State
}
type Snapshot struct {
	ID             string         `json:"id"`
	CanvasID       string         `json:"canvas_id"`
	CanvasRevision int64          `json:"canvas_revision"`
	Name           string         `json:"name"`
	State          map[string]any `json:"state"`
	CreatedAt      time.Time      `json:"created_at"`
}

type Service struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Create(ctx context.Context, caseID string, req CreateRequest) (Canvas, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return Canvas{}, fmt.Errorf("name is required")
	}
	var orgID, workspaceID string
	if err := s.pool.QueryRow(ctx, `SELECT organization_id::text,workspace_id::text FROM cases WHERE id=$1`, caseID).Scan(&orgID, &workspaceID); errors.Is(err, pgx.ErrNoRows) {
		return Canvas{}, fmt.Errorf("case not found")
	} else if err != nil {
		return Canvas{}, err
	}
	return s.insert(ctx, orgID, workspaceID, &caseID, req)
}

func (s *Service) insert(ctx context.Context, orgID, workspaceID string, caseID *string, req CreateRequest) (Canvas, error) {
	id := uuid.NewString()
	state := normalize(req.State)
	row := s.pool.QueryRow(ctx, `INSERT INTO investigation_canvases(id,organization_id,workspace_id,case_id,investigation_id,parent_canvas_id,name,node_refs,edge_refs,groups,annotations,filters,layout,camera,view_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING `+columns,
		id, orgID, workspaceID, caseID, req.InvestigationID, req.ParentCanvasID, req.Name, j(state.NodeRefs), j(state.EdgeRefs), j(state.Groups), j(state.Annotations), j(state.Filters), j(state.Layout), j(state.Camera), j(state.ViewState))
	return scan(row)
}

func (s *Service) List(ctx context.Context, caseID string) ([]Canvas, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+columns+` FROM investigation_canvases WHERE case_id=$1 ORDER BY updated_at DESC`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Canvas{}
	for rows.Next() {
		c, e := scan(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

func (s *Service) Get(ctx context.Context, id string) (Canvas, error) {
	c, err := scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM investigation_canvases WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Canvas{}, ErrNotFound
	}
	return c, err
}

func (s *Service) Update(ctx context.Context, id string, expected int64, req UpdateRequest) (Canvas, error) {
	if expected <= 0 {
		return Canvas{}, fmt.Errorf("If-Match revision is required")
	}
	current, err := s.Get(ctx, id)
	if err != nil {
		return Canvas{}, err
	}
	st := normalize(req.State)
	if req.Name == nil {
		req.Name = &current.Name
	}
	if strings.TrimSpace(*req.Name) == "" {
		return Canvas{}, fmt.Errorf("name is required")
	}
	row := s.pool.QueryRow(ctx, `UPDATE investigation_canvases SET name=$3,node_refs=$4,edge_refs=$5,groups=$6,annotations=$7,filters=$8,layout=$9,camera=$10,view_state=$11,revision=revision+1,updated_at=now() WHERE id=$1 AND revision=$2 RETURNING `+columns, id, expected, strings.TrimSpace(*req.Name), j(st.NodeRefs), j(st.EdgeRefs), j(st.Groups), j(st.Annotations), j(st.Filters), j(st.Layout), j(st.Camera), j(st.ViewState))
	c, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Canvas{}, ErrRevisionConflict
	}
	return c, err
}

func (s *Service) Branch(ctx context.Context, id string, req CreateRequest) (Canvas, error) {
	parent, err := s.Get(ctx, id)
	if err != nil {
		return Canvas{}, err
	}
	req.ParentCanvasID = &parent.ID
	if len(req.NodeRefs) == 0 {
		req.State = parent.state()
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = parent.Name + " · 分支"
	}
	return s.insert(ctx, parent.OrganizationID, parent.WorkspaceID, parent.CaseID, req)
}

func (s *Service) CreateSnapshot(ctx context.Context, id, name string) (Snapshot, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("修订 %d", c.Revision)
	}
	state := map[string]any{"name": c.Name, "node_refs": c.NodeRefs, "edge_refs": c.EdgeRefs, "groups": c.Groups, "annotations": c.Annotations, "filters": c.Filters, "layout": c.Layout, "camera": c.Camera, "view_state": c.ViewState}
	var out Snapshot
	err = s.pool.QueryRow(ctx, `INSERT INTO canvas_snapshots(id,canvas_id,canvas_revision,name,state) VALUES($1,$2,$3,$4,$5) RETURNING id::text,canvas_id::text,canvas_revision,name,state,created_at`, uuid.NewString(), id, c.Revision, name, j(state)).Scan(&out.ID, &out.CanvasID, &out.CanvasRevision, &out.Name, &state, &out.CreatedAt)
	out.State = state
	return out, err
}

func (s *Service) ListSnapshots(ctx context.Context, id string) ([]Snapshot, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,canvas_id::text,canvas_revision,name,state,created_at FROM canvas_snapshots WHERE canvas_id=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		var x Snapshot
		var raw []byte
		if err = rows.Scan(&x.ID, &x.CanvasID, &x.CanvasRevision, &x.Name, &raw, &x.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &x.State)
		out = append(out, x)
	}
	return out, rows.Err()
}

const columns = `id::text,organization_id::text,workspace_id::text,case_id::text,investigation_id::text,parent_canvas_id::text,name,node_refs,edge_refs,groups,annotations,filters,layout,camera,view_state,revision,created_at,updated_at`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Canvas, error) {
	var c Canvas
	var n, e, g, a, f, l, cam, v []byte
	err := row.Scan(&c.ID, &c.OrganizationID, &c.WorkspaceID, &c.CaseID, &c.InvestigationID, &c.ParentCanvasID, &c.Name, &n, &e, &g, &a, &f, &l, &cam, &v, &c.Revision, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	_ = json.Unmarshal(n, &c.NodeRefs)
	_ = json.Unmarshal(e, &c.EdgeRefs)
	_ = json.Unmarshal(g, &c.Groups)
	_ = json.Unmarshal(a, &c.Annotations)
	_ = json.Unmarshal(f, &c.Filters)
	_ = json.Unmarshal(l, &c.Layout)
	_ = json.Unmarshal(cam, &c.Camera)
	_ = json.Unmarshal(v, &c.ViewState)
	return c, nil
}
func (c Canvas) state() State {
	return State{c.NodeRefs, c.EdgeRefs, c.Groups, c.Annotations, c.Filters, c.Layout, c.Camera, c.ViewState}
}
func normalize(s State) State {
	if s.NodeRefs == nil {
		s.NodeRefs = []string{}
	}
	if s.EdgeRefs == nil {
		s.EdgeRefs = []string{}
	}
	if s.Groups == nil {
		s.Groups = []map[string]any{}
	}
	if s.Annotations == nil {
		s.Annotations = []map[string]any{}
	}
	if s.Filters == nil {
		s.Filters = map[string]any{}
	}
	if s.Layout == nil {
		s.Layout = map[string]any{}
	}
	if s.Camera == nil {
		s.Camera = map[string]any{}
	}
	if s.ViewState == nil {
		s.ViewState = map[string]any{}
	}
	return s
}
func j(v any) []byte { b, _ := json.Marshal(v); return b }
