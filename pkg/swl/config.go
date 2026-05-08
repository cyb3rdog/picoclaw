package swl

// Config holds SWL-specific configuration decoded from the builtin hook's
// Config JSON field (config.BuiltinHookConfig.Config).
type Config struct {
	// DBPath overrides the default {workspace}/.swl/swl.db location.
	DBPath string `json:"db_path,omitempty"`

	// MaxFileSizeBytes caps the content passed to the extractor.
	// Default: 512 * 1024 (512KB).
	MaxFileSizeBytes int64 `json:"max_file_size_bytes,omitempty"`

	// InjectSessionHint controls whether the 60-token session hint is
	// prepended to the system prompt. Default: true.
	InjectSessionHint *bool `json:"inject_session_hint,omitempty"`

	// ExtractSymbols enables symbol extraction from source files. Default: true.
	ExtractSymbols *bool `json:"extract_symbols,omitempty"`
	// ExtractImports enables import/dependency extraction. Default: true.
	ExtractImports *bool `json:"extract_imports,omitempty"`
	// ExtractTasks enables TODO/FIXME/HACK comment extraction. Default: true.
	ExtractTasks *bool `json:"extract_tasks,omitempty"`
	// ExtractSections enables heading extraction from markdown/docs. Default: true.
	ExtractSections *bool `json:"extract_sections,omitempty"`
	// ExtractURLs enables URL extraction. Default: true.
	ExtractURLs *bool `json:"extract_urls,omitempty"`
	// ExtractLLMContent enables extraction from LLM response text. Default: true.
	ExtractLLMContent *bool `json:"extract_llm_content,omitempty"`

	// ReasoningConfidenceCap is the maximum confidence assigned to entities
	// extracted from LLM reasoning/thinking blocks. Default: 0.75.
	ReasoningConfidenceCap *float64 `json:"reasoning_confidence_cap,omitempty"`

	// ExtractSymbolPatterns is an optional list of RE2-syntax regular expressions
	// used to extract symbol names from source files.  Each pattern must have
	// exactly one capturing group whose match is the symbol name.
	// When nil or empty, the built-in default patterns are used.
	// Operators can replace or extend this list without recompiling picoclaw.
	ExtractSymbolPatterns []string `json:"extract_symbol_patterns,omitempty"`
}

func boolDefault(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

func (c *Config) effectiveMaxFileSize() int64 {
	if c == nil || c.MaxFileSizeBytes <= 0 {
		return 512 * 1024
	}
	return c.MaxFileSizeBytes
}

func (c *Config) InjectSessionHintEnabled() bool {
	if c == nil {
		return true
	}
	return boolDefault(c.InjectSessionHint, true)
}

func (c *Config) effectiveExtractSymbols() bool {
	if c == nil {
		return true
	}
	return boolDefault(c.ExtractSymbols, true)
}

func (c *Config) effectiveExtractImports() bool {
	if c == nil {
		return true
	}
	return boolDefault(c.ExtractImports, true)
}

func (c *Config) effectiveExtractTasks() bool {
	if c == nil {
		return true
	}
	return boolDefault(c.ExtractTasks, true)
}

func (c *Config) effectiveExtractSections() bool {
	if c == nil {
		return true
	}
	return boolDefault(c.ExtractSections, true)
}

func (c *Config) effectiveExtractURLs() bool {
	if c == nil {
		return true
	}
	return boolDefault(c.ExtractURLs, true)
}

func (c *Config) effectiveExtractLLMContent() bool {
	if c == nil {
		return true
	}
	return boolDefault(c.ExtractLLMContent, true)
}

// EffectiveReasoningConfidenceCap returns the maximum confidence for entities
// extracted from LLM reasoning/thinking blocks (default 0.75).
func (c *Config) EffectiveReasoningConfidenceCap() float64 {
	if c == nil || c.ReasoningConfidenceCap == nil {
		return 0.75
	}
	v := *c.ReasoningConfidenceCap
	if v <= 0 || v > 1.0 {
		return 0.75
	}
	return v
}

// EffectiveMaxSymbols returns the max symbol entities per file extraction.
// Consumes RulesEngine limit if available, else hardcoded default.
func (c *Config) EffectiveMaxSymbols(r *RulesEngine) int {
	if r != nil && r.MaxSymbols > 0 {
		return r.MaxSymbols
	}
	return maxSymbols
}

// EffectiveMaxImports returns the max import entities per file extraction.
func (c *Config) EffectiveMaxImports(r *RulesEngine) int {
	if r != nil && r.MaxImports > 0 {
		return r.MaxImports
	}
	return maxImports
}

// EffectiveMaxTasks returns the max task entities per file extraction.
func (c *Config) EffectiveMaxTasks(r *RulesEngine) int {
	if r != nil && r.MaxTasks > 0 {
		return r.MaxTasks
	}
	return maxTasks
}

// EffectiveMaxSections returns the max section entities per file extraction.
func (c *Config) EffectiveMaxSections(r *RulesEngine) int {
	if r != nil && r.MaxSections > 0 {
		return r.MaxSections
	}
	return maxSections
}

// EffectiveMaxURLs returns the max URL entities per file extraction.
func (c *Config) EffectiveMaxURLs(r *RulesEngine) int {
	if r != nil && r.MaxURLs > 0 {
		return r.MaxURLs
	}
	return maxURLs
}

// --- Extraction limit helpers ---
// These read from the Manager's rules engine (YAML-loaded) when available,
// falling back to hardcoded defaults. Call on Manager, not Config.

// MaxSymbols returns the effective max symbols per file.
func (m *Manager) MaxSymbols() int {
	return m.cfg.EffectiveMaxSymbols(m.rules)
}

// MaxImports returns the effective max imports per file.
func (m *Manager) MaxImports() int {
	return m.cfg.EffectiveMaxImports(m.rules)
}

// MaxTasks returns the effective max tasks per file.
func (m *Manager) MaxTasks() int {
	return m.cfg.EffectiveMaxTasks(m.rules)
}

// MaxSections returns the effective max sections per file.
func (m *Manager) MaxSections() int {
	return m.cfg.EffectiveMaxSections(m.rules)
}

// MaxURLs returns the effective max URLs per file.
func (m *Manager) MaxURLs() int {
	return m.cfg.EffectiveMaxURLs(m.rules)
}

// MaxUses returns the noise symbol threshold (not externalized — constant).
func MaxUses() int { return maxUses }
