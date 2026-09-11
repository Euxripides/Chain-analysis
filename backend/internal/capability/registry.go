// Package capability provides the version-aware registry used by agents and
// application services. It stores descriptors, never provider implementations.
package capability

import (
	"fmt"
	"sort"
	"sync"

	"github.com/blockchain-platform/blockchain-platform/internal/platform/contract"
)

type Registry struct {
	mu    sync.RWMutex
	items map[string]map[string]contract.Capability
}

func NewRegistry() *Registry {
	return &Registry{items: make(map[string]map[string]contract.Capability)}
}

func (r *Registry) Register(c contract.Capability) error {
	if c.Name == "" || c.Version == "" {
		return fmt.Errorf("capability name and version are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	versions := r.items[c.Name]
	if versions == nil {
		versions = make(map[string]contract.Capability)
		r.items[c.Name] = versions
	}
	if _, exists := versions[c.Version]; exists {
		return fmt.Errorf("capability %s@%s already registered", c.Name, c.Version)
	}
	versions[c.Version] = c
	return nil
}

func (r *Registry) Resolve(name, version string) (contract.Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	versions := r.items[name]
	if version != "" {
		c, ok := versions[version]
		return c, ok
	}
	var keys []string
	for key := range versions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return contract.Capability{}, false
	}
	c := versions[keys[len(keys)-1]]
	return c, true
}

func (r *Registry) List() []contract.Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []contract.Capability
	for _, versions := range r.items {
		for _, c := range versions {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Version < out[j].Version
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func RegisterBuiltins(r *Registry) {
	for _, c := range []contract.Capability{
		{Name: "data.ensure", Version: "1.0", Description: "Resolve coverage and acquire only missing data", Inputs: []string{"DataRequirement"}, Outputs: []string{"Job"}, Async: true, Available: true},
		{Name: "data.coverage", Version: "1.0", Description: "Inspect local dataset coverage", Inputs: []string{"DataRequirement"}, Outputs: []string{"Coverage"}, Available: true},
		{Name: "chain.read", Version: "1.0", Description: "Provider-neutral typed chain reads", Inputs: []string{"SubjectRef", "RequestContext"}, Outputs: []string{"ChainData"}, Available: true},
		{Name: "transaction.trace", Version: "1.0", Description: "Audited transaction trace query", Inputs: []string{"SubjectRef", "RequestContext"}, Outputs: []string{"SemanticEvent"}, Available: true},
		{Name: "protocol.semantic.decode", Version: "1.0", Description: "Rebuild provider-neutral protocol semantic events from indexed normalized data", Inputs: []string{"SubjectRef", "RequestContext"}, Outputs: []string{"SemanticEvent"}, Available: true},
		{Name: "analysis.advanced_patterns", Version: "1.0", Description: "Compute evidence-bounded advanced investigation patterns and net asset flows", Inputs: []string{"SubjectRef", "RequestContext"}, Outputs: []string{"AdvancedAnalysisResult", "Finding", "Evidence"}, Available: true},
	} {
		_ = r.Register(c)
	}
}
