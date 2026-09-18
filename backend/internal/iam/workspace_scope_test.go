package iam

import (
	"errors"
	"testing"
)

// P1-20260913 回归测试：工作区选择只认该身份在该组织的有效成员资格，
// 且所有拒绝分支 fail closed —— 绝不回退"组织内第一个工作区"（旧 defaultWorkspace 缺陷）。
func TestDecideWorkspace(t *testing.T) {
	wsA := WorkspaceOption{ID: "8b41836b-0000-4000-8000-0000000000a1", Slug: "s7js-a", Name: "A"}
	wsB := WorkspaceOption{ID: "62a87634-0000-4000-8000-0000000000b2", Slug: "s7js-b", Name: "B"}
	wsX := WorkspaceOption{ID: "cccccccc-0000-4000-8000-0000000000c3", Slug: "s7js-c", Name: "C"}

	cases := []struct {
		name      string
		options   []WorkspaceOption
		requested string
		wantID    string
		wantErr   error
	}{
		{"零成员资格必须 fail closed", nil, "", "", ErrNoWorkspaceMembership},
		{"零成员资格即使显式指定也不放行", nil, wsA.ID, "", ErrNoWorkspaceMembership},
		{"零成员资格不得回退组织首个工作区", []WorkspaceOption{}, wsA.ID, "", ErrNoWorkspaceMembership},
		{"唯一成员资格直接绑定", []WorkspaceOption{wsB}, "", wsB.ID, nil},
		{"唯一成员资格显式匹配", []WorkspaceOption{wsB}, wsB.ID, wsB.ID, nil},
		{"唯一成员资格显式指向非成员工作区必须拒绝", []WorkspaceOption{wsB}, wsA.ID, "", ErrWorkspaceNotMember},
		{"多成员资格未选择必须拒绝（不得任取首行）", []WorkspaceOption{wsA, wsB}, "", "", ErrWorkspaceSelectionRequired},
		{"多成员资格空白选择必须拒绝", []WorkspaceOption{wsA, wsB}, "   ", "", ErrWorkspaceSelectionRequired},
		{"多成员资格显式选择生效", []WorkspaceOption{wsA, wsB}, wsB.ID, wsB.ID, nil},
		{"多成员资格显式选择越界必须拒绝", []WorkspaceOption{wsA, wsB}, wsX.ID, "", ErrWorkspaceNotMember},
		{"单成员不得被组织首个工作区覆盖", []WorkspaceOption{wsB}, wsA.ID, "", ErrWorkspaceNotMember},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decideWorkspace(tc.options, tc.requested)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if got != "" {
					t.Fatalf("bound workspace = %q, want empty on rejection", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.wantID {
				t.Fatalf("bound workspace = %q, want %q", got, tc.wantID)
			}
		})
	}
}

// 选择器契约：多成员资格必须返回可选列表，顺序稳定（上游 SQL 按 slug 排序）。
func TestDecideWorkspaceSelectionErrorCarriesOptions(t *testing.T) {
	wsA := WorkspaceOption{ID: "id-a", Slug: "s7js-a", Name: "A"}
	wsB := WorkspaceOption{ID: "id-b", Slug: "s7js-b", Name: "B"}
	_, err := decideWorkspace([]WorkspaceOption{wsA, wsB}, "")
	if !errors.Is(err, ErrWorkspaceSelectionRequired) {
		t.Fatalf("err = %v, want ErrWorkspaceSelectionRequired", err)
	}
	var sel *WorkspaceSelectionRequiredError
	if !errors.As(err, &sel) {
		t.Fatalf("err = %v, want *WorkspaceSelectionRequiredError", err)
	}
	if len(sel.Options) != 2 || sel.Options[0].ID != "id-a" || sel.Options[1].ID != "id-b" {
		t.Fatalf("options = %+v, want stable [id-a id-b]", sel.Options)
	}
}

// 错误码映射必须让每一类工作区拒绝都可被客户端区分（权限/作用域闸门分离）。
func TestStatusForErrorWorkspaceScope(t *testing.T) {
	cases := []struct {
		err      error
		wantCode string
	}{
		{ErrNoWorkspaceMembership, "no_workspace_membership"},
		{ErrWorkspaceSelectionRequired, "workspace_selection_required"},
		{ErrWorkspaceNotMember, "workspace_not_member"},
		{ErrWorkspaceAccessRevoked, "workspace_access_revoked"},
		{&WorkspaceSelectionRequiredError{}, "workspace_selection_required"},
	}
	for _, tc := range cases {
		status, code := StatusForError(tc.err)
		if code != tc.wantCode {
			t.Fatalf("StatusForError(%v) code = %q, want %q", tc.err, code, tc.wantCode)
		}
		if status < 400 || status >= 500 {
			t.Fatalf("StatusForError(%v) status = %d, want 4xx (fail closed)", tc.err, status)
		}
	}
}
