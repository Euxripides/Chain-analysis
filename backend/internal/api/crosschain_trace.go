package api

import (
	"encoding/json"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"strings"
)

func normalizeBridgeTrace(raw json.RawMessage) (*chaintrace.CallFrame, error) {
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		var items []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 || len(items) > 400 {
			return nil, fmt.Errorf("parity trace exceeds bridge frame budget or is invalid")
		}
		for _, i := range items {
			switch strings.ToLower(i.Type) {
			case "call", "create", "suicide", "selfdestruct":
			default:
				return nil, fmt.Errorf("unknown parity trace type")
			}
		}
	}
	root, _, err := chaintrace.Normalize(raw)
	if err != nil {
		return nil, err
	}
	frames, depth := chaintrace.Summarize(root)
	if frames > 400 || depth > 65 {
		return nil, fmt.Errorf("complete trace exceeds bridge frame or depth budget")
	}
	return root, nil
}
