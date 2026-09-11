package report

import (
	"strings"
	"testing"
	"time"
)

func TestRenderKeepsEvidenceAndProfitBoundaries(t *testing.T) {
	body := string(render("追踪资金", "waiting_review", map[string]any{"chain": "tron", "value": "TTarget"}, []map[string]any{{"title": "RAPID_FORWARD", "status": "candidate", "confidence": .78, "algorithm": "analysis.advanced_patterns", "algorithm_version": "1.0", "summary": "近额转移"}}, []map[string]any{{"type": "deterministic_pattern"}}, 1, 1, time.Unix(0, 0).UTC()))
	for _, expected := range []string{"RAPID_FORWARD", "deterministic_pattern：1", "净流量不等于已实现利润", "Graph Patch、1 个 Graph Snapshot"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("report missing %q:\n%s", expected, body)
		}
	}
}
