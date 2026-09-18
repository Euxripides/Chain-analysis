package api

// AUTH-007 bounded-publish contract (2026-09-15).
//
// The watchdog-protected SIGSTOP drill proved the defect this file pins: the
// request path published scope-control and job events with context.Background()
// and no deadline, go-nats waits for the JetStream acknowledgement, and with the
// bus paused the workspace switch, the logout and the admin force revoke all
// exceeded the 12 s client bound. The fix is a bounded, fail-contained helper, so
// the contract that has to survive future edits is:
//
//   * every request-path publish goes through publishBestEffort;
//   * publishBestEffort carries a deadline derived from busPublishTimeout;
//   * no publish in this package is bound to a raw background context again;
//   * busPublishTimeout stays a var (a test or a deployment can pin it) and stays
//     well inside the AUTH-007 budget.
//
// The last test is a negative calibration: the unpatched shape of the file must
// be rejected by the same inspection.

import (
	"os"
	"strings"
	"testing"
)

func readAPISource(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// funcBody returns the body of the first function whose header matches, up to the
// line that closes it at column 0.
func funcBody(src, header string) string {
	i := strings.Index(src, header)
	if i < 0 {
		return ""
	}
	rest := src[i:]
	end := strings.Index(rest, "\\n}\\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

type publishContract struct {
	funnel      string
	scopeBody   string
	eventBody   string
	rawPublish  bool
	timeoutVar  bool
	timeoutMs   int
	boundInBody bool
}

func inspectPublishContract(src string) publishContract {
	c := publishContract{}
	c.funnel = funcBody(src, "func (s *Server) publishBestEffort(")
	c.scopeBody = funcBody(src, "func (s *Server) publishSSEScopeControl(")
	c.eventBody = funcBody(src, "func (s *Server) publishEvent(")
	c.rawPublish = strings.Contains(src, "s.deps.Bus.Publish(context.Background()")
	c.boundInBody = strings.Contains(c.funnel, "context.WithTimeout(context.Background(), busPublishTimeout)")
	c.timeoutVar = strings.Contains(src, "var busPublishTimeout = ")
	if c.timeoutVar {
		line := src[strings.Index(src, "var busPublishTimeout = "):]
		if nl := strings.Index(line, "\\n"); nl >= 0 {
			line = line[:nl]
		}
		switch {
		case strings.Contains(line, "Millisecond"):
			c.timeoutMs = 1
		case strings.Contains(line, "= 1 * time.Second"):
			c.timeoutMs = 1000
		case strings.Contains(line, "Second"):
			c.timeoutMs = 2000
		}
	}
	return c
}

func TestScopeControlPublishIsBounded(t *testing.T) {
	src := readAPISource(t, "sse.go")
	c := inspectPublishContract(src)

	if c.funnel == "" {
		t.Fatalf("publishBestEffort is missing")
	}
	if !c.boundInBody {
		t.Fatalf("publishBestEffort must derive its deadline from busPublishTimeout")
	}
	if !strings.Contains(c.scopeBody, "s.publishBestEffort(") {
		t.Fatalf("publishSSEScopeControl must publish through publishBestEffort")
	}
	if !strings.Contains(c.eventBody, "s.publishBestEffort(") {
		t.Fatalf("publishEvent must publish through publishBestEffort")
	}
	if c.rawPublish {
		t.Fatalf("a request-path publish is still bound to a raw context.Background()")
	}
	if !c.timeoutVar {
		t.Fatalf("busPublishTimeout must stay a var so a test or a deployment can pin it")
	}
	if c.timeoutMs <= 0 || c.timeoutMs > 1000 {
		t.Fatalf("busPublishTimeout must stay a bound between 1 ms and 1 s, got %d", c.timeoutMs)
	}
}

// TestPublishContractDetectsUnboundedRegression is the negative calibration: the
// unpatched shape of this file must be rejected by the same inspection.
func TestPublishContractDetectsUnboundedRegression(t *testing.T) {
	unpatched := strings.Join([]string{
		"func (s *Server) publishSSEScopeControl(sessionID, identityID, reason, requestID string) {",
		"\tif s.deps.Bus == nil || !s.deps.Bus.Enabled() {",
		"\t\treturn",
		"\t}",
		"\ts.deps.Bus.Publish(context.Background(), sseScopeControlSubject, sseScopeControlMsg{",
		"\t\tSessionID: sessionID,",
		"\t})",
		"}",
		"",
		"func (s *Server) publishEvent(subject string, payload any) {",
		"\tif s.deps.Bus == nil {",
		"\t\treturn",
		"\t}",
		"\ts.deps.Bus.Publish(context.Background(), subject, payload)",
		"}",
	}, "\\n")
	c := inspectPublishContract(unpatched)
	if c.funnel != "" {
		t.Fatalf("the unpatched source must not be seen as having the bounded funnel")
	}
	if !c.rawPublish {
		t.Fatalf("the unpatched source must be detected as publishing on a raw background context")
	}
	if c.timeoutVar {
		t.Fatalf("the unpatched source must not be seen as carrying the timeout var")
	}
}
