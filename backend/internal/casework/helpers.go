package casework

import (
	"encoding/json"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/platform/contract"
)

func jsonUnmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

// auditEvent 构造一条 audit v2 事件；与 auditTx 共用主体字段映射。
func auditEvent(actor Actor, action, category, resourceType, resourceID, result, risk string, detail map[string]any) audit.Event {
	if risk == "" {
		risk = "low"
	}
	return audit.Event{
		ActorType: "user", ActorID: actor.Principal.IdentityID,
		ActorEmail: actor.Principal.Email, ActorName: actor.Principal.DisplayName,
		ActorRoles:     actor.Principal.Roles,
		OrganizationID: actor.Principal.OrganizationID, WorkspaceID: actor.Principal.WorkspaceID,
		SessionID: actor.Principal.SessionID, DeviceID: actor.Principal.DeviceID,
		Action: action, Category: category, ResourceType: resourceType, ResourceID: resourceID,
		HTTPMethod: actor.Method, Route: actor.Route, RequestID: actor.RequestID, TraceID: actor.TraceID,
		Result: result, PermissionKey: action, PolicyDecision: "allow",
		RiskLevel: risk, Sensitive: risk == "critical" || risk == "high",
		ClientIP: actor.ClientIP, PeerIP: actor.PeerIP, UserAgent: actor.UserAgent,
		Detail: detail,
	}
}

func contractSubject(chain, value string) contract.SubjectRef {
	return contract.SubjectRef{Type: "address", Chain: chain, Value: value}
}
