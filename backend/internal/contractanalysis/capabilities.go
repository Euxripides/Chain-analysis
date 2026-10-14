package contractanalysis

// StructuralEngines lists accepted EVM analysis adapters. The capability probe
// reports the engines actually installed in the digest-pinned runtime.
var StructuralEngines = map[string]bool{
	"whatsabi": true, "sevm": true, "solc-ast": true, "heimdall": true,
	"slither": true, "gigahorse": true, "vyper-ast": true,
}

type EngineCapability struct {
	Name             string   `json:"name"`
	Status           string   `json:"status"`
	Version          *string  `json:"version,omitempty"`
	CompilerVersions []string `json:"compiler_versions,omitempty"`
}

type ProgramCapability struct {
	VMFamily              string   `json:"vm_family"`
	CodeAnalysis          string   `json:"code_analysis"`
	SourceLanguages       []string `json:"source_languages,omitempty"`
	TransactionAnalysis   string   `json:"transaction_analysis,omitempty"`
	ExactHistoricalReplay string   `json:"exact_historical_replay"`
	UnsupportedState      string   `json:"unsupported_state_status,omitempty"`
}
