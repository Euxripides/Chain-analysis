package api

// P4 static contract (2026-09-15): no export object write may bypass the durable
// intent and live-scope revalidation that runs immediately before the Put.
//
// The measured defect was a refused request leaving exports/4a380557-....zip in
// MinIO with no row behind it: object storage does not participate in the request
// transaction, so the ONLY thing that may authorise a Put is a committed intent
// row plus a live scope, re-read in the same call as the Put. A start-of-pipeline
// check is not enough, and per-call-site guards drift. This test therefore pins
// the shape:
//
//   1. putExportStream REQUIRES the export id as its second parameter;
//   2. every call site in the package passes exactly six arguments, so a new
//      call site cannot be added without naming an export id;
//   3. the gate it calls defaults to exportIntentLive (the real check), and the
//      test-only override is the only alternative.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func apiGoSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = string(data)
	}
	return out
}

func TestEveryExportObjectWriteRequiresAnExportID(t *testing.T) {
	sources := apiGoSources(t)
	store, ok := sources["export_object_store.go"]
	if !ok {
		t.Fatalf("export_object_store.go is missing")
	}
	if !strings.Contains(store, "func (s *Server) putExportStream(ctx context.Context, exportID, key string, r io.Reader, size int64, contentType string)") {
		t.Fatalf("putExportStream must take the export id as a required parameter")
	}
	gateIdx := strings.Index(store, "s.exportIntentGate(ctx, exportID)")
	putIdx := strings.Index(store, "PutObject(")
	if gateIdx < 0 {
		t.Fatalf("putExportStream must call the intent gate")
	}
	if putIdx < 0 || gateIdx > putIdx {
		t.Fatalf("the intent gate must run before the Put, in the same call")
	}

	callSites := 0
	for name, src := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for i := 0; i < len(src); {
			idx := strings.Index(src[i:], "putExportStream(")
			if idx < 0 {
				break
			}
			abs := i + idx
			i = abs + len("putExportStream(")
			if strings.HasPrefix(src[abs:], "func (s *Server) putExportStream(") {
				continue
			}
			callSites++
			rest := src[i:]
			depth := 1
			commas := 0
			for j := 0; j < len(rest); j++ {
				switch rest[j] {
				case '(':
					depth++
				case ')':
					depth--
					if depth == 0 {
						j = len(rest)
					}
				case ',':
					if depth == 1 {
						commas++
					}
				}
			}
			if commas != 5 {
				t.Fatalf("%s: a putExportStream call site passes %d arguments, want 6 (ctx, exportID, key, reader, size, contentType)", name, commas+1)
			}
		}
	}
	if callSites < 6 {
		t.Fatalf("expected at least 6 export object write call sites, found %d", callSites)
	}
	t.Logf("checked %d export object write call sites", callSites)
}

func TestExportIntentGateDefaultsToTheRealCheck(t *testing.T) {
	sources := apiGoSources(t)
	exp, ok := sources["export.go"]
	if !ok {
		t.Fatalf("export.go is missing")
	}
	i := strings.Index(exp, "func (s *Server) exportIntentGate(")
	if i < 0 {
		t.Fatalf("the gate is missing")
	}
	body := exp[i:]
	if cut := strings.Index(body, "\n}\n"); cut > 0 {
		body = body[:cut]
	}
	// 2026-09-15: the gate now reaches the real check through exportIntentSettled,
	// which re-runs exactly exportIntentLive inside a bounded window so a
	// legitimate pipeline cannot lose a race with its own request commit. The
	// frozen literal moved with it; the property is unchanged and is pinned in
	// two steps below: the gate must call the settling wrapper, and that wrapper
	// must call the real exportIntentLive check.
	if !strings.Contains(body, "return s.exportIntentSettled(ctx, exportID)") {
		t.Fatalf("the gate must default to the real check through exportIntentSettled")
	}
	if !strings.Contains(body, "exportIntentOverride != nil && testing.Testing()") {
		t.Fatalf("the gate must divert only when BOTH the override is set and the binary is a test binary")
	}
	j := strings.Index(exp, "func (s *Server) exportIntentSettled(")
	if j < 0 {
		t.Fatalf("exportIntentSettled is missing")
	}
	settled := exp[j:]
	if cut := strings.Index(settled, "\n}\n"); cut > 0 {
		settled = settled[:cut]
	}
	if !strings.Contains(settled, "s.exportIntentLive(ctx, exportID)") {
		t.Fatalf("exportIntentSettled must re-run the real exportIntentLive check")
	}
	if !strings.Contains(settled, `reason == "intent_row_missing"`) {
		t.Fatalf("the settle window must retry ONLY a not-yet-visible intent row")
	}
	if !strings.Contains(settled, "exportIntentSettleWait") {
		t.Fatalf("the settle window must be bounded")
	}
}

// TestNoObjectWriteBypassesPutExportStream pins the other half of the contract:
// the package owns exactly one PutObject call, and it lives in the single guarded
// helper. A second call would be a new object write that no gate covers.
func TestNoObjectWriteBypassesPutExportStream(t *testing.T) {
	sources := apiGoSources(t)
	putSites := 0
	for name, src := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for i := 0; i < len(src); {
			idx := strings.Index(src[i:], "PutObject(")
			if idx < 0 {
				break
			}
			abs := i + idx
			i = abs + len("PutObject(")
			putSites++
			if name != "export_object_store.go" {
				t.Fatalf("%s: PutObject is called outside export_object_store.go", name)
			}
		}
	}
	if putSites != 1 {
		t.Fatalf("expected exactly one guarded PutObject call in the package, found %d", putSites)
	}
}

// TestIntentGateRevalidatesLiveAuthorization pins that the immediately-before-Put
// check reads the durable intent OUTSIDE the request transaction and revalidates
// the live scope and authorization rather than only the row's existence.
func TestIntentGateRevalidatesLiveAuthorization(t *testing.T) {
	sources := apiGoSources(t)
	exp := sources["export.go"]
	i := strings.Index(exp, "func (s *Server) exportIntentLive(")
	if i < 0 {
		t.Fatalf("exportIntentLive is missing")
	}
	body := exp[i:]
	if cut := strings.Index(body, "\n}\n"); cut > 0 {
		body = body[:cut]
	}
	for _, want := range []string{
		"dbtx.WithoutTx(ctx)",
		"workspace_memberships",
		"wm.status='active'",
		"authz_membership_revoked",
		"scope_org_changed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("exportIntentLive must revalidate %q immediately before the Put", want)
		}
	}
}
