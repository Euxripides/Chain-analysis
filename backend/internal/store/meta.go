package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// DefaultOrgWorkspace V1.2 单用户：默认 org/workspace。
func (s *Store) DefaultOrgWorkspace(ctx context.Context) (orgID, wsID string, err error) {
	err = s.Pool.QueryRow(ctx, "SELECT o.id, w.id FROM organizations o JOIN workspaces w ON w.organization_id = o.id "+
		"WHERE o.slug='default' AND w.slug='default'").Scan(&orgID, &wsID)
	return
}

// ---- Cases / Tags ----

func (s *Store) CreateCase(ctx context.Context, wsID, orgID, caseNo, name, description, color string) (model.CaseInfo, error) {
	c := model.CaseInfo{ID: uuid.NewString(), OrganizationID: orgID, WorkspaceID: wsID,
		CaseNo: caseNo, Name: name, Description: description, Color: color, Status: "active"}
	_, err := s.Pool.Exec(ctx, "INSERT INTO cases (id, organization_id, workspace_id, case_no, name, description, color, status) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,'active')", c.ID, orgID, wsID, caseNo, name, description, color)
	return c, err
}

func (s *Store) ListCases(ctx context.Context, wsID string) ([]model.CaseInfo, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, organization_id, workspace_id, case_no, name, description, status, color, created_at, updated_at "+
		"FROM cases WHERE workspace_id=$1 ORDER BY name", wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CaseInfo
	for rows.Next() {
		var c model.CaseInfo
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.WorkspaceID, &c.CaseNo, &c.Name, &c.Description, &c.Status, &c.Color, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCaseByName(ctx context.Context, wsID, name string) (*model.CaseInfo, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, organization_id, workspace_id, case_no, name, description, status, color, created_at, updated_at "+
		"FROM cases WHERE workspace_id=$1 AND name=$2", wsID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	var c model.CaseInfo
	err = rows.Scan(&c.ID, &c.OrganizationID, &c.WorkspaceID, &c.CaseNo, &c.Name, &c.Description, &c.Status, &c.Color, &c.CreatedAt, &c.UpdatedAt)
	return &c, err
}

func (s *Store) DeleteCase(ctx context.Context, wsID, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Avoid leaving a dangling primary_case_id when a case is removed.
	if _, err := tx.Exec(ctx, "UPDATE workspace_addresses wa SET primary_case_id=("+
		"SELECT l.case_id FROM address_case_links l WHERE l.workspace_id=wa.workspace_id AND l.chain=wa.chain "+
		"AND l.normalized_address=wa.normalized_address AND l.case_id<>$1 ORDER BY l.assigned_at LIMIT 1) "+
		"WHERE wa.workspace_id=$2 AND wa.primary_case_id=$1", id, wsID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM address_case_links WHERE case_id=$1 AND workspace_id=$2", id, wsID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM cases WHERE id=$1 AND workspace_id=$2", id, wsID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateTag(ctx context.Context, wsID, orgID, name, description, color, source string) (model.TagInfo, error) {
	if source == "" {
		source = "user"
	}
	t := model.TagInfo{ID: uuid.NewString(), OrganizationID: orgID, WorkspaceID: wsID,
		Name: name, Description: description, Color: color, Source: source}
	_, err := s.Pool.Exec(ctx, "INSERT INTO tags (id, organization_id, workspace_id, name, description, color, source) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7)", t.ID, orgID, wsID, name, description, color, source)
	return t, err
}

func (s *Store) ListTags(ctx context.Context, wsID string) ([]model.TagInfo, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, organization_id, workspace_id, name, description, color, source, created_at, updated_at "+
		"FROM tags WHERE workspace_id=$1 ORDER BY name", wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TagInfo
	for rows.Next() {
		var t model.TagInfo
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.WorkspaceID, &t.Name, &t.Description, &t.Color, &t.Source, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTagByName(ctx context.Context, wsID, name string) (*model.TagInfo, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, organization_id, workspace_id, name, description, color, source, created_at, updated_at "+
		"FROM tags WHERE workspace_id=$1 AND name=$2", wsID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	var t model.TagInfo
	err = rows.Scan(&t.ID, &t.OrganizationID, &t.WorkspaceID, &t.Name, &t.Description, &t.Color, &t.Source, &t.CreatedAt, &t.UpdatedAt)
	return &t, err
}

func (s *Store) DeleteTag(ctx context.Context, wsID, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "DELETE FROM address_tag_links WHERE tag_id=$1 AND workspace_id=$2", id, wsID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM tags WHERE id=$1 AND workspace_id=$2", id, wsID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- Address links ----

func (s *Store) AddCaseLink(ctx context.Context, wsID, chain, addr, caseID string) error {
	addr = strings.ToLower(addr)
	var valid bool
	if err := s.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM workspace_addresses WHERE workspace_id=$1 AND chain=$2 AND normalized_address=$3) "+
		"AND EXISTS(SELECT 1 FROM cases WHERE workspace_id=$1 AND id=$4)", wsID, chain, addr, caseID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("address or case not found in workspace")
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO address_case_links (workspace_id, chain, normalized_address, case_id) "+
		"VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING", wsID, chain, addr, caseID)
	return err
}

func (s *Store) RemoveCaseLink(ctx context.Context, wsID, chain, addr, caseID string) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM address_case_links WHERE workspace_id=$1 AND chain=$2 AND normalized_address=$3 AND case_id=$4",
		wsID, chain, strings.ToLower(addr), caseID)
	return err
}

func (s *Store) AddTagLink(ctx context.Context, wsID, chain, addr, tagID string) error {
	addr = strings.ToLower(addr)
	var valid bool
	if err := s.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM workspace_addresses WHERE workspace_id=$1 AND chain=$2 AND normalized_address=$3) "+
		"AND EXISTS(SELECT 1 FROM tags WHERE workspace_id=$1 AND id=$4)", wsID, chain, addr, tagID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("address or tag not found in workspace")
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO address_tag_links (workspace_id, chain, normalized_address, tag_id) "+
		"VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING", wsID, chain, addr, tagID)
	return err
}

func (s *Store) RemoveTagLink(ctx context.Context, wsID, chain, addr, tagID string) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM address_tag_links WHERE workspace_id=$1 AND chain=$2 AND normalized_address=$3 AND tag_id=$4",
		wsID, chain, strings.ToLower(addr), tagID)
	return err
}

func (s *Store) SetPrimaryCase(ctx context.Context, wsID, chain, addr, caseID string) error {
	tag, err := s.Pool.Exec(ctx, "UPDATE workspace_addresses SET primary_case_id=$4, updated_at=now() "+
		"WHERE workspace_id=$1 AND chain=$2 AND normalized_address=$3 AND EXISTS ("+
		"SELECT 1 FROM address_case_links l WHERE l.workspace_id=$1 AND l.chain=$2 AND l.normalized_address=$3 AND l.case_id=$4)",
		wsID, chain, strings.ToLower(addr), caseID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address or linked case not found in workspace")
	}
	return nil
}

func (s *Store) SetDisplayName(ctx context.Context, wsID, chain, addr, name string) error {
	tag, err := s.Pool.Exec(ctx, "UPDATE workspace_addresses SET display_name=$4, updated_at=now() "+
		"WHERE workspace_id=$1 AND chain=$2 AND normalized_address=$3",
		wsID, chain, strings.ToLower(addr), name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address not found in workspace")
	}
	return nil
}

func (s *Store) AddressCaseNames(ctx context.Context, wsID, chain, addr string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, "SELECT c.name FROM address_case_links l JOIN cases c ON c.id = l.case_id "+
		"WHERE l.workspace_id=$1 AND l.chain=$2 AND l.normalized_address=$3 ORDER BY c.name",
		wsID, chain, strings.ToLower(addr))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) AddressTagNames(ctx context.Context, wsID, chain, addr string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, "SELECT t.name FROM address_tag_links l JOIN tags t ON t.id = l.tag_id "+
		"WHERE l.workspace_id=$1 AND l.chain=$2 AND l.normalized_address=$3 ORDER BY t.name",
		wsID, chain, strings.ToLower(addr))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
