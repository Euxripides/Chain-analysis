package api

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestScopeCommitGateSlowThreshold pins the critical-section warning threshold:
// the default is used unless SCOPE_COMMIT_GATE_SLOW_MS carries a positive
// integer, and a malformed or non-positive value never disables the warning.
func TestScopeCommitGateSlowThreshold(t *testing.T) {
	t.Setenv("SCOPE_COMMIT_GATE_SLOW_MS", "")
	if got := scopeCommitGateSlowMs(); got != scopeCommitGateSlowDefaultMS {
		t.Fatalf("default threshold = %d, want %d", got, scopeCommitGateSlowDefaultMS)
	}
	t.Setenv("SCOPE_COMMIT_GATE_SLOW_MS", "2500")
	if got := scopeCommitGateSlowMs(); got != 2500 {
		t.Fatalf("env threshold = %d, want 2500", got)
	}
	for _, raw := range []string{"not-a-number", "0", "-5", "  "} {
		t.Setenv("SCOPE_COMMIT_GATE_SLOW_MS", raw)
		if got := scopeCommitGateSlowMs(); got != scopeCommitGateSlowDefaultMS {
			t.Fatalf("threshold for %q = %d, want the default %d", raw, got, scopeCommitGateSlowDefaultMS)
		}
	}
}

// TestScopeCommitGateCriticalMetricIsPublished pins the AUTH-007 measurement:
// a gated request that commits publishes a critical_ms sample carrying exactly
// the permission key and the outcome, and nothing that could identify a user or
// a session.
func TestScopeCommitGateCriticalMetricIsPublished(t *testing.T) {
	const perm = "test:scope:metric"
	scopeCommitGateObserve(perm, "allowed", 3, 7)
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var hist *dto.MetricFamily
	for _, f := range families {
		if f.GetName() == "cs_scope_commit_gate_critical_ms" {
			hist = f
			break
		}
	}
	if hist == nil {
		t.Fatal("cs_scope_commit_gate_critical_ms is not registered on the default gatherer")
	}
	found := false
	for _, m := range hist.GetMetric() {
		labels := map[string]string{}
		for _, l := range m.GetLabel() {
			labels[l.GetName()] = l.GetValue()
		}
		if labels["permission"] != perm || labels["outcome"] != "allowed" {
			continue
		}
		found = true
		if m.GetHistogram().GetSampleCount() == 0 {
			t.Fatal("the critical_ms histogram carries no sample")
		}
		for name := range labels {
			if name != "permission" && name != "outcome" {
				t.Fatalf("unexpected label %q on the critical_ms histogram", name)
			}
		}
	}
	if !found {
		t.Fatal("no critical_ms series for the observed permission and outcome")
	}
}

// TestSSEScopeRecheckIntervalIsBoundedUnderAUTH007 pins the deterministic bound
// the cross-instance stream relies on: the default period stays strictly below
// the five second budget, and every override is clamped into the accepted range
// so no environment can silently push the bound back to the heartbeat.
func TestSSEScopeRecheckIntervalIsBoundedUnderAUTH007(t *testing.T) {
	t.Setenv("SSE_SCOPE_RECHECK_MS", "")
	if got := sseScopeRecheckInterval(); got != sseScopeRecheckDefaultMS*time.Millisecond {
		t.Fatalf("default re-check = %v, want %dms", got, sseScopeRecheckDefaultMS)
	}
	if sseScopeRecheckDefaultMS >= 5000 {
		t.Fatalf("the default re-check period %dms is not inside the 5s AUTH-007 budget", sseScopeRecheckDefaultMS)
	}
	cases := map[string]int{
		"1":            sseScopeRecheckMinMS,
		"250":          sseScopeRecheckMinMS,
		"2000":         2000,
		"99999":        sseScopeRecheckMaxMS,
		"not-a-number": sseScopeRecheckDefaultMS,
		"0":            sseScopeRecheckDefaultMS,
		"-10":          sseScopeRecheckDefaultMS,
	}
	for raw, want := range cases {
		t.Setenv("SSE_SCOPE_RECHECK_MS", raw)
		if got := sseScopeRecheckInterval(); got != time.Duration(want)*time.Millisecond {
			t.Fatalf("SSE_SCOPE_RECHECK_MS=%q -> %v, want %dms", raw, got, want)
		}
	}
}

// TestSSEScopeControlDisableIsExplicit pins the negative-calibration knob: only
// the literal "1" turns the bus control message off.
func TestSSEScopeControlDisableIsExplicit(t *testing.T) {
	for raw, want := range map[string]bool{"1": true, "": false, "0": false, "true": false, "yes": false} {
		t.Setenv("SSE_SCOPE_CONTROL_DISABLE", raw)
		if got := sseScopeControlDisabled(); got != want {
			t.Fatalf("SSE_SCOPE_CONTROL_DISABLE=%q -> %v, want %v", raw, got, want)
		}
	}
}
