
package audit

// 哈希载荷的"可复算性"回归（ADR-019 / 2026-09-13 seq 52394、52399 真实缺陷）。
//
// 不变量：写入时哈希的载荷，必须能由「落库后读回的行」逐字节重建。
// UUID 列落库走 nullUUID（不可解析 -> NULL），校验器读回是 COALESCE(col::text,'') -> ""，
// 因此载荷侧必须用同一个归一化；否则该行永远复算不出来，链上留下永久无法解释的断点。

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUUIDPayloadValueMatchesStoredRepresentation(t *testing.T) {
	cases := map[string]string{
		"":   "",
		"v4": "48607d15-bc7c-4406-bee6-ba1c07ab8e0f",
		"大写": "48607D15-BC7C-4406-BEE6-BA1C07AB8E0F",
		"非 UUID 文本（客户端可控输入）": "not-a-uuid",
		"截断的 UUID": "48607d15-bc7c-4406",
		"纯数字":      "12345",
	}
	for name, in := range cases {
		got := UUIDPayloadValue(in)
		// 期望：能落库的值原样保留，不能落库的值收敛为空串（= 读回值）。
		want := in
		if in == "" {
			want = ""
		} else if _, err := uuid.Parse(in); err != nil {
			want = ""
		} else {
			want = in
		}
		if got != want {
			t.Fatalf("%s: UUIDPayloadValue(%q) = %q, want %q", name, in, got, want)
		}
	}
}

// 同一逻辑事件，无论 workspace_id 是不可落库的文本还是空串，载荷必须相同。
func TestChainPayloadEqualsReadBackPayloadForUnstorableUUID(t *testing.T) {
	base := Event{
		OccurredAt:     time.Date(2026, 9, 13, 16, 3, 0, 146439000, time.UTC),
		Environment:    "development",
		ActorType:      "user",
		ActorID:        "48607d15-bc7c-4406-bee6-ba1c07ab8e0f",
		ActorEmail:     "p1gate-multi@fixture.local",
		OrganizationID: "5edc0f75-a7fb-4b0a-9750-2aec1ac737f7",
		WorkspaceID:    "8b41836b-6d53-45e4-9a8a-3248a6e24e28",
		SessionID:      "59d969b7-c901-4ce3-a863-cd0f2fa82a9e",
		DeviceID:       "9d9bcfed-78f8-4851-9301-90205d7354cd",
		Action:         "authorization.workspace_switched",
		Category:       "authorization",
		ResourceType:   "workspace",
		Result:         "deny",
		HTTPStatus:     403,
		RiskLevel:      "medium",
	}
	w := &Writer{}

	// 正向：合法 UUID 原样进入载荷（不能把正常事件也归一化掉）。
	validPayload, err := Canonicalize(w.chainPayload(base))
	if err != nil {
		t.Fatalf("canonicalize valid: %v", err)
	}
	if !bytes.Contains(validPayload, []byte(base.WorkspaceID)) {
		t.Fatalf("合法 workspace_id 必须原样出现在载荷里: %s", validPayload)
	}

	// 反向：客户端传入不可落库的 workspace_id 时，
	// 载荷必须等于「该列落库为 NULL 后读回」的样子（空串），否则永久无法复算。
	bad := base
	bad.WorkspaceID = "not-a-uuid"
	badPayload, err := Canonicalize(w.chainPayload(bad))
	if err != nil {
		t.Fatalf("canonicalize bad: %v", err)
	}
	readBack := base
	readBack.WorkspaceID = ""
	readBackPayload, err := Canonicalize(w.chainPayload(readBack))
	if err != nil {
		t.Fatalf("canonicalize readback: %v", err)
	}
	if !bytes.Equal(badPayload, readBackPayload) {
		t.Fatalf("不可落库的 UUID 必须与读回值一致\n bad      = %s\n readback = %s", badPayload, readBackPayload)
	}
	if bytes.Contains(badPayload, []byte("not-a-uuid")) {
		t.Fatalf("不可落库的文本不得进入哈希载荷: %s", badPayload)
	}
}
