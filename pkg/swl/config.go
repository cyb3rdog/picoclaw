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
