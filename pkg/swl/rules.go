package swl

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed swl.rules.default.yaml
var defaultRulesFS embed.FS

//go:embed swl.query.default.yaml
var defaultQueryFS embed.FS

// QueryIntent maps a regex pattern to a handler method name.
// Serialized from swl.query.default.yaml via LoadQueryConfig.
type QueryIntent struct {
	ID        string   `yaml:"id"`
	Desc      string   `yaml:"description"`
	Patterns  []string `yaml:"patterns"`
	Handler   string   `yaml:"handler"`
	HintGroup int      `yaml:"hint_group"` // capture group index (0 = no hint)
}

// LabelSearchWeights controls scoring weights for the labelSearch handler.
// Loaded from swl.query.default.yaml; workspace-overridable.
type LabelSearchWeights struct {
	Role        int `yaml:"role"`
	Domain      int `yaml:"domain"`
	Kind        int `yaml:"kind"`
	ContentType int `yaml:"content_type"`
	Name        int `yaml:"name"`
	Path        int `yaml:"path"`
}

// LabelSearchCfg holds label_search section from swl.query.default.yaml.
type LabelSearchCfg struct {
	Weights    LabelSearchWeights `yaml:"weights"`
	MaxResults int                `yaml:"max_results"`
}

// QueryConfig holds the parsed swl.query.default.yaml intent list + Tier 2 SQL templates.
type QueryConfig struct {
	Version     string          `yaml:"version"`
	Intents     []QueryIntent   `yaml:"intents"`
	SQLTmpls    []SQLTemplate   `yaml:"tier2_templates"`
	LabelSearch LabelSearchCfg  `yaml:"label_search"`
}

// SQLTemplate is a Tier 2 named query bound by keyword.
type SQLTemplate struct {
	ID       string   `yaml:"id"`
	Desc     string   `yaml:"description"`
	Keywords []string `yaml:"keywords"`
	Query    string   `yaml:"query"`
}

// RulesEngine loads and manages swl.rules.yaml with deep-merge overrides.
// Implements Phase B externalization: all label derivation rules are driven
// by configuration, with workspace-level overrides deep-merged on top of defaults.
type RulesEngine struct {
	cfg              RulesConfig // raw parsed config
	PathPrefixRules  []PathPrefixRule
	NamePatternRules []NamePatternRule
	ContentTypeRules map[string]string
	SymbolPatterns   []string
	NoiseSymbols     map[string]bool
	IgnoreDirs       map[string]bool
	IgnoreExtensions map[string]bool

	// Extraction limits (loaded from file_rules in swl.rules.yaml).
	MaxSymbols  int
	MaxImports  int
	MaxTasks    int
	MaxSections int
	MaxURLs     int
	MaxTopics   int
	SkipHosts   []string

	// Snapshot configuration (Phase B — moved from hardcoded constants).
	AnchorPatterns      []string // file base-name patterns qualifying as anchor docs
	SnapshotMaxDepth    int      // max directory depth for BuildSnapshot walk
	AreaMinExtensionPct int      // min % of files sharing one extension to qualify as a SemanticArea

	// Per-extension extraction overrides (Phase B).
	ExtractionOverrides []ExtractionOverride

	// Query engine config (Phase B query externalization).
	QueryIntents []CompiledIntent // Tier 1: pattern → handler
	SQLTemplates []SQLTemplate    // Tier 2: keyword → SQL

	// LabelSearch scoring weights (from swl.query.default.yaml, workspace-overridable).
	LabelSearchWeights LabelSearchWeights

	// AutoApplySuggestions enables writing gap-derived rules to swl.rules.auto.yaml.
	AutoApplySuggestions bool
}

// PathPrefixRule maps a path prefix to semantic labels.
type PathPrefixRule struct {
	Prefix     string `yaml:"prefix"`
	Role       string `yaml:"role,omitempty"`
	Domain     string `yaml:"domain,omitempty"`
	Kind       string `yaml:"kind,omitempty"`
	Visibility string `yaml:"visibility,omitempty"`
}

// NamePatternRule matches a file name pattern and assigns labels.
type NamePatternRule struct {
	Pattern string `yaml:"pattern"`
	Role    string `yaml:"role,omitempty"`
	Domain  string `yaml:"domain,omitempty"`
	Kind    string `yaml:"kind,omitempty"`
}

// ContentTypeRule maps file extension to content_type.
type ContentTypeRule struct {
	Extension   string `yaml:"extension"`
	ContentType string `yaml:"content_type"`
}

// RulesConfig is the full YAML config structure.
type RulesConfig struct {
	Version              string               `yaml:"version"`
	LabelRules           LabelRulesBlock      `yaml:"label_rules"`
	FileRules            FileRulesBlock       `yaml:"file_rules"`
	Snapshot             SnapshotBlock        `yaml:"snapshot"`
	ExtractionOverrides  []ExtractionOverride `yaml:"extraction_overrides"`
	AutoApplySuggestions bool                 `yaml:"auto_apply_suggestions"`
}

// LabelRulesBlock contains label derivation rules.
type LabelRulesBlock struct {
	PathPrefixes []PathPrefixRule  `yaml:"path_prefixes"`
	NamePatterns []NamePatternRule `yaml:"name_patterns"`
	ContentTypes []ContentTypeRule `yaml:"content_types"`
}

// FileRulesBlock contains extraction configuration.
type FileRulesBlock struct {
	Symbols         SymbolsBlock  `yaml:"symbols"`
	Imports         ImportsBlock  `yaml:"imports"`
	Tasks           TasksBlock    `yaml:"tasks"`
	URLs            URLsBlock     `yaml:"urls"`
	Sections        SectionsBlock `yaml:"sections"`
	IgnoreDirs      []string      `yaml:"ignore_dirs"`
	IgnoreExtension []string      `yaml:"ignore_extensions"`
}

type SymbolsBlock struct {
	MaxPerFile int      `yaml:"max_per_file"`
	Noise      []string `yaml:"noise"`
	Patterns   []string `yaml:"patterns"`
}

type ImportsBlock struct {
	MaxPerFile int `yaml:"max_per_file"`
}

type TasksBlock struct {
	MaxPerFile int      `yaml:"max_per_file"`
	Patterns   []string `yaml:"patterns"`
}

type URLsBlock struct {
	MaxPerFile int      `yaml:"max_per_file"`
	SkipHosts  []string `yaml:"skip_hosts"`
}

type SectionsBlock struct {
	MaxPerFile int `yaml:"max_per_file"`
}

// SnapshotBlock holds snapshot-related configuration.
type SnapshotBlock struct {
	MaxDepth           int      `yaml:"max_depth"`
	AreaMinExtensionPct int     `yaml:"area_min_extension_pct"`
	AnchorPatterns     []string `yaml:"anchor_patterns"`
}

// ExtractionOverride specifies per-extension extraction settings.
type ExtractionOverride struct {
	Extensions      []string `yaml:"extensions"`
	ExtractSymbols  *bool    `yaml:"extract_symbols,omitempty"`
	ExtractImports  *bool    `yaml:"extract_imports,omitempty"`
	ExtractTasks    *bool    `yaml:"extract_tasks,omitempty"`
	ExtractSections *bool    `yaml:"extract_sections,omitempty"`
	ExtractURLs     *bool    `yaml:"extract_urls,omitempty"`
}

// LoadRules loads the rules engine from a workspace-level swl.rules.yaml,
// deep-merging it on top of the embedded defaults.
// workspace is the absolute workspace path. rulesPath is optional (auto-detected).
func LoadRules(workspace, rulesPath string) (*RulesEngine, error) {
	r := &RulesEngine{}

	// Load defaults from embedded asset
	defaultData, err := defaultRulesFS.ReadFile("swl.rules.default.yaml")
	if err != nil {
		return nil, err
	}
	if unmarshalErr := yaml.Unmarshal(defaultData, &r.cfg); unmarshalErr != nil {
		return nil, unmarshalErr
	}

	// Load workspace-level overrides if present
	if rulesPath == "" {
		rulesPath = filepath.Join(workspace, ".swl", "swl.rules.yaml")
	}
	overrideData, err := loadOptionalFile(rulesPath)
	if err != nil {
		return nil, err
	}
	if len(overrideData) > 0 {
		var overrideCfg RulesConfig
		if unmarshalErr := yaml.Unmarshal(overrideData, &overrideCfg); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		deepMergeRules(&r.cfg, &overrideCfg)
	}

	// Load auto-generated rules from .swl/swl.rules.auto.yaml (third merge layer).
	// This file is written by ApplyPendingSuggestions when auto_apply_suggestions is true.
	// Malformed auto file is silently skipped (it was machine-generated; if corrupt, delete it).
	autoPath := filepath.Join(workspace, ".swl", "swl.rules.auto.yaml")
	autoData, _ := loadOptionalFile(autoPath)
	if len(autoData) > 0 {
		var autoCfg RulesConfig
		if yaml.Unmarshal(autoData, &autoCfg) == nil {
			deepMergeRules(&r.cfg, &autoCfg)
		}
	}

	// Compile runtime structures from config
	r.compileFromConfig()

	return r, nil
}

func (r *RulesEngine) compileFromConfig() {
	// Path prefix rules — from config (defaults + overrides)
	r.PathPrefixRules = r.cfg.LabelRules.PathPrefixes

	// Name pattern rules
	r.NamePatternRules = r.cfg.LabelRules.NamePatterns

	// Content type rules — map for O(1) lookup
	r.ContentTypeRules = make(map[string]string)
	for _, ct := range r.cfg.LabelRules.ContentTypes {
		r.ContentTypeRules[ct.Extension] = ct.ContentType
	}

	// Noise symbols
	r.NoiseSymbols = make(map[string]bool)
	for _, s := range r.cfg.FileRules.Symbols.Noise {
		r.NoiseSymbols[s] = true
	}

	// Symbol patterns (for future callsites; currently supplementary)
	r.SymbolPatterns = r.cfg.FileRules.Symbols.Patterns

	// Ignore dirs/extensions from config
	r.IgnoreDirs = make(map[string]bool)
	for _, d := range r.cfg.FileRules.IgnoreDirs {
		r.IgnoreDirs[d] = true
	}

	r.IgnoreExtensions = make(map[string]bool)
	for _, ext := range r.cfg.FileRules.IgnoreExtension {
		r.IgnoreExtensions[ext] = true
	}

	// Limits
	if r.cfg.FileRules.Symbols.MaxPerFile > 0 {
		r.MaxSymbols = r.cfg.FileRules.Symbols.MaxPerFile
	} else {
		r.MaxSymbols = maxSymbols
	}
	if r.cfg.FileRules.Imports.MaxPerFile > 0 {
		r.MaxImports = r.cfg.FileRules.Imports.MaxPerFile
	} else {
		r.MaxImports = maxImports
	}
	if r.cfg.FileRules.Tasks.MaxPerFile > 0 {
		r.MaxTasks = r.cfg.FileRules.Tasks.MaxPerFile
	} else {
		r.MaxTasks = maxTasks
	}
	if r.cfg.FileRules.URLs.MaxPerFile > 0 {
		r.MaxURLs = r.cfg.FileRules.URLs.MaxPerFile
	} else {
		r.MaxURLs = maxURLs
	}
	r.SkipHosts = r.cfg.FileRules.URLs.SkipHosts

	// Sections limit
	if r.cfg.FileRules.Sections.MaxPerFile > 0 {
		r.MaxSections = r.cfg.FileRules.Sections.MaxPerFile
	} else {
		r.MaxSections = maxSections
	}
	// Topics limit (from section rules, section extraction is the proxy for topics)
	r.MaxTopics = maxTopics

	// Snapshot configuration
	if r.cfg.Snapshot.MaxDepth > 0 {
		r.SnapshotMaxDepth = r.cfg.Snapshot.MaxDepth
	} else {
		r.SnapshotMaxDepth = snapshotMaxDepth
	}
	if r.cfg.Snapshot.AreaMinExtensionPct > 0 {
		r.AreaMinExtensionPct = r.cfg.Snapshot.AreaMinExtensionPct
	} else {
		r.AreaMinExtensionPct = 60
	}
	r.AnchorPatterns = r.cfg.Snapshot.AnchorPatterns

	// Per-extension extraction overrides
	r.ExtractionOverrides = r.cfg.ExtractionOverrides

	// Autonomous gap analysis auto-apply
	r.AutoApplySuggestions = r.cfg.AutoApplySuggestions
}

// DeriveLabels computes semantic labels using config-driven rules, replacing the
// hardcoded DeriveLabels() in labels.go when Phase B is active.
func (r *RulesEngine) DeriveLabels(entityType EntityType, name string) LabelResult {
	var lr LabelResult

	// 1. Path prefix rules
	norm := filepath.ToSlash(name)
	normSlash := norm + "/"
	for _, rule := range r.PathPrefixRules {
		if strings.HasPrefix(norm, rule.Prefix) || strings.HasPrefix(normSlash, rule.Prefix) {
			if rule.Role != "" && lr.Role == "" {
				lr.Role = rule.Role
			}
			if rule.Domain != "" && lr.Domain == "" {
				lr.Domain = rule.Domain
			}
			if rule.Kind != "" && lr.Kind == "" {
				lr.Kind = rule.Kind
			}
			if rule.Visibility != "" && lr.Visibility == "" {
				lr.Visibility = rule.Visibility
			}
		}
	}

	// 2. Name pattern rules — files only
	if entityType == KnownTypeFile {
		base := filepath.Base(name)
		for _, rule := range r.NamePatternRules {
			if matchLabelPattern(base, rule.Pattern) {
				if rule.Role != "" && lr.Role == "" {
					lr.Role = rule.Role
				}
				if rule.Kind != "" && lr.Kind == "" {
					lr.Kind = rule.Kind
				}
				if rule.Domain != "" && lr.Domain == "" {
					lr.Domain = rule.Domain
				}
				break
			}
		}
	}

	// 3. Content type from extension — files only
	if entityType == KnownTypeFile {
		ext := strings.ToLower(filepath.Ext(name))
		if ct, ok := r.ContentTypeRules[ext]; ok && lr.ContentType == "" {
			lr.ContentType = ct
		}
	}

	return lr
}

// CompiledIntent pairs a compiled regex with handler metadata for Tier 1 dispatch.
type CompiledIntent struct {
	ID        string
	Handler   string // method name on Manager
	HintGroup int    // capture group for hint (0 = no hint)
	RE        *regexp.Regexp
}

// LoadQueryConfig loads query intents and SQL templates from swl.query.default.yaml.
// Returns compiled intents with regexes ready for matching.
// Workspace-level swl.query.yaml overrides are deep-merged if present.
func LoadQueryConfig(workspace, queryPath string) (*QueryConfig, error) {
	cfg := &QueryConfig{}

	// Load embedded defaults
	data, err := defaultQueryFS.ReadFile("swl.query.default.yaml")
	if err != nil {
		return nil, err
	}
	if unmarshalErr := yaml.Unmarshal(data, cfg); unmarshalErr != nil {
		return nil, unmarshalErr
	}

	// Override with workspace-level if present
	if queryPath == "" {
		queryPath = filepath.Join(workspace, ".swl", "swl.query.yaml")
	}
	overrideData, err := loadOptionalFile(queryPath)
	if err != nil {
		return nil, err
	}
	if len(overrideData) > 0 {
		var overrideCfg QueryConfig
		if unmarshalErr := yaml.Unmarshal(overrideData, &overrideCfg); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		mergeQueryConfig(cfg, &overrideCfg)
	}

	return cfg, nil
}

// CompileQueryConfig loads intents + templates from a QueryConfig struct,
// compiling all regexes into CompiledIntent for the Tier 1 dispatcher.
// Handler names that are not present in handlerRegistry are logged to stderr
// and skipped so that typos in swl.query.yaml do not silently produce dead intents.
func CompileQueryConfig(cfg *QueryConfig) []CompiledIntent {
	intents := make([]CompiledIntent, 0, len(cfg.Intents))
	for _, intent := range cfg.Intents {
		if !ValidateHandler(intent.Handler) {
			fmt.Fprintf(os.Stderr,
				"[SWL] warning: intent %q references unknown handler %q — skipping\n",
				intent.ID, intent.Handler,
			)
			continue
		}
		for _, pattern := range intent.Patterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				fmt.Fprintf(os.Stderr,
					"[SWL] warning: intent %q has invalid pattern %q: %v — skipping\n",
					intent.ID, pattern, err,
				)
				continue
			}
			intents = append(intents, CompiledIntent{
				ID:        intent.ID,
				Handler:   intent.Handler,
				HintGroup: intent.HintGroup,
				RE:        re,
			})
		}
	}
	return intents
}

// IsNoiseSymbol returns true if sym is in the configured noise symbol list.
func (r *RulesEngine) IsNoiseSymbol(sym string) bool {
	if r == nil {
		return false
	}
	return r.NoiseSymbols[sym]
}

// OverrideForExt returns the ExtractionOverride for a given file extension,
// or nil if no override matches.
func (r *RulesEngine) OverrideForExt(ext string) *ExtractionOverride {
	if r == nil {
		return nil
	}
	ext = strings.ToLower(ext)
	for i := range r.ExtractionOverrides {
		for _, e := range r.ExtractionOverrides[i].Extensions {
			if strings.ToLower(e) == ext {
				return &r.ExtractionOverrides[i]
			}
		}
	}
	return nil
}

// InitRulesWith is deprecated — rules are accessed via Manager.rules.
// Kept for backward compatibility with any callers.
func InitRulesWith(r *RulesEngine) {}

// InitRules is deprecated — rules are loaded via Manager initialization.
// Kept for backward compatibility with any callers.
func InitRules(workspace, rulesPath string) error { return nil }

// DeriveLabels delegates to the rules engine if available,
// otherwise falls back to package-level DeriveLabels().
func (m *Manager) DeriveLabels(entityType EntityType, name string) LabelResult {
	if m.rules != nil {
		return m.rules.DeriveLabels(entityType, name)
	}
	return DeriveLabels(entityType, name)
}

// deepMergeRules performs a deep merge of override into base.
// Arrays (path_prefixes, name_patterns, content_types) are replaced, not appended.
// File rules scalar fields are overwritten.
func deepMergeRules(base, override *RulesConfig) {
	if override.Version != "" {
		base.Version = override.Version
	}

	// Label rules: arrays replaced entirely (no append)
	if len(override.LabelRules.PathPrefixes) > 0 {
		base.LabelRules.PathPrefixes = override.LabelRules.PathPrefixes
	}
	if len(override.LabelRules.NamePatterns) > 0 {
		base.LabelRules.NamePatterns = override.LabelRules.NamePatterns
	}
	if len(override.LabelRules.ContentTypes) > 0 {
		base.LabelRules.ContentTypes = override.LabelRules.ContentTypes
	}

	// File rules: scalar overrides
	if override.FileRules.Symbols.MaxPerFile > 0 {
		base.FileRules.Symbols.MaxPerFile = override.FileRules.Symbols.MaxPerFile
	}
	if len(override.FileRules.Symbols.Noise) > 0 {
		base.FileRules.Symbols.Noise = override.FileRules.Symbols.Noise
	}
	if len(override.FileRules.Symbols.Patterns) > 0 {
		base.FileRules.Symbols.Patterns = override.FileRules.Symbols.Patterns
	}
	if override.FileRules.Imports.MaxPerFile > 0 {
		base.FileRules.Imports.MaxPerFile = override.FileRules.Imports.MaxPerFile
	}
	if override.FileRules.Tasks.MaxPerFile > 0 {
		base.FileRules.Tasks.MaxPerFile = override.FileRules.Tasks.MaxPerFile
	}
	if len(override.FileRules.Tasks.Patterns) > 0 {
		base.FileRules.Tasks.Patterns = override.FileRules.Tasks.Patterns
	}
	if override.FileRules.URLs.MaxPerFile > 0 {
		base.FileRules.URLs.MaxPerFile = override.FileRules.URLs.MaxPerFile
	}
	if len(override.FileRules.URLs.SkipHosts) > 0 {
		base.FileRules.URLs.SkipHosts = append(base.FileRules.URLs.SkipHosts, override.FileRules.URLs.SkipHosts...)
	}
	if override.FileRules.Sections.MaxPerFile > 0 {
		base.FileRules.Sections.MaxPerFile = override.FileRules.Sections.MaxPerFile
	}
	if len(override.FileRules.IgnoreDirs) > 0 {
		base.FileRules.IgnoreDirs = override.FileRules.IgnoreDirs
	}
	if len(override.FileRules.IgnoreExtension) > 0 {
		base.FileRules.IgnoreExtension = override.FileRules.IgnoreExtension
	}

	// Snapshot block
	if override.Snapshot.MaxDepth > 0 {
		base.Snapshot.MaxDepth = override.Snapshot.MaxDepth
	}
	if override.Snapshot.AreaMinExtensionPct > 0 {
		base.Snapshot.AreaMinExtensionPct = override.Snapshot.AreaMinExtensionPct
	}
	if len(override.Snapshot.AnchorPatterns) > 0 {
		base.Snapshot.AnchorPatterns = override.Snapshot.AnchorPatterns
	}

	// Per-extension extraction overrides (append)
	if len(override.ExtractionOverrides) > 0 {
		base.ExtractionOverrides = append(base.ExtractionOverrides, override.ExtractionOverrides...)
	}
}

// loadOptionalFile reads a workspace-level override file if it exists.
// Returns nil bytes if the file is not found (not an error).
func loadOptionalFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
} // mergeQueryConfig deep-merges overrideCfg into cfg (intents + SQL templates).
func mergeQueryConfig(cfg, override *QueryConfig) {
	// Intents: append override intents (no deduplication; user manages their own)
	if len(override.Intents) > 0 {
		cfg.Intents = append(cfg.Intents, override.Intents...)
	}
	// SQL templates: append override templates
	if len(override.SQLTmpls) > 0 {
		cfg.SQLTmpls = append(cfg.SQLTmpls, override.SQLTmpls...)
	}
}
