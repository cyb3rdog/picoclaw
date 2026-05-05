package config

// SWLToolConfig holds configuration for the Semantic Workspace Layer (SWL).
// It mirrors pkg/swl.Config and is decoded from the JSON tools config.
type SWLToolConfig struct {
	Enabled                bool     `json:"enabled"`
	DBPath                 string   `json:"db_path,omitempty"`
	MaxFileSizeBytes       int64    `json:"max_file_size_bytes,omitempty"`
	InjectSessionHint      *bool    `json:"inject_session_hint,omitempty"`
	ExtractSymbols         *bool    `json:"extract_symbols,omitempty"`
	ExtractImports         *bool    `json:"extract_imports,omitempty"`
	ExtractTasks           *bool    `json:"extract_tasks,omitempty"`
	ExtractSections        *bool    `json:"extract_sections,omitempty"`
	ExtractURLs            *bool    `json:"extract_urls,omitempty"`
	ExtractLLMContent      *bool    `json:"extract_llm_content,omitempty"`
	ReasoningConfidenceCap *float64 `json:"reasoning_confidence_cap,omitempty"`
}
