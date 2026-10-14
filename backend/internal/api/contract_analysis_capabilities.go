package api

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/google/uuid"
)

var contractCapabilityCache sync.Map

func (s *Server) contractCapabilities(ctx context.Context) (any, error) {
	image := os.Getenv("CONTRACT_ENGINE_IMAGE")
	if !((ca.Runner{Image: image}).Ready()) {
		return nil, summaryError(503, "CONTRACT_ENGINE_UNAVAILABLE", "Digest-pinned analysis runtime unavailable")
	}
	if cached, ok := contractCapabilityCache.Load(image); ok {
		return cached, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	r, err := (ca.Runner{Image: image}).Run(bounded, uuid.NewString(), 1, ca.Input{Operation: "capabilities"})
	if err != nil {
		return nil, err
	}
	rows := r.Sections["capabilities"]
	if len(rows) != 1 {
		return nil, summaryError(503, "CAPABILITY_PROTOCOL_UNAVAILABLE", "Configured runtime does not expose a capability manifest")
	}
	var data map[string]any
	if err = json.Unmarshal(rows[0], &data); err != nil {
		return nil, err
	}
	data["engine_digest"] = image
	data["task_submission_enabled"] = os.Getenv("CONTRACT_ANALYSIS_ENABLED") == "1"
	contractCapabilityCache.Store(image, data)
	return data, nil
}
