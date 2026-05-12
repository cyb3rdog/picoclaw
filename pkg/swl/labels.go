package swl

import (
	"path/filepath"
	"strings"
)

// LabelResult holds the semantic labels derived for an entity.
type LabelResult struct {
	Role        string // functional role: "authentication", "api", "logging"
	Domain      string // business/technical domain: "security", "networking", "data-access"
	Kind        string // structural kind: "entry-point", "test", "mock", "configuration"
	ContentType string // technical content type: "sql", "hcl", "protobuf", "markdown"
	Visibility  string // visibility scope: "internal", "public"
}

// --- Label Derivation Rules ---

// pathPrefixRules maps path prefixes to role/domain labels.
// Order matters: most specific first. All paths are workspace-relative.
var pathPrefixRules = []struct {
	prefix  string
	role    string
	domain  string
	kind    string
	visible string
}{
	// Authentication / security
	{prefix: "pkg/auth/", role: "authentication", domain: "security"},
	{prefix: "auth/", role: "authentication", domain: "security"},
	{prefix: "security/", role: "security", domain: "security"},
	{prefix: "pkg/crypto/", role: "cryptography", domain: "security"},

	// Data access layer
	{prefix: "pkg/db/", role: "database", domain: "data-access"},
	{prefix: "pkg/database/", role: "database", domain: "data-access"},
	{prefix: "pkg/data/", role: "data-processing", domain: "data-access"},
	{prefix: "pkg/repo/", role: "repository", domain: "data-access"},

	// Networking / API layer
	{prefix: "pkg/api/", role: "api", domain: "networking"},
	{prefix: "pkg/http/", role: "http", domain: "networking"},
	{prefix: "pkg/rest/", role: "rest-api", domain: "networking"},
	{prefix: "pkg/grpc/", role: "grpc-api", domain: "networking"},
	{prefix: "pkg/websocket/", role: "websocket", domain: "networking"},

	// Business logic / service layer
	{prefix: "pkg/service/", role: "service", domain: "business"},
	{prefix: "pkg/biz/", role: "business-logic", domain: "business"},
	{prefix: "pkg/core/", role: "core", domain: "business"},
	{prefix: "pkg/domain/", role: "domain", domain: "business"},

	// Entry points
	{prefix: "cmd/", role: "entry-point", kind: "entry-point", domain: "infrastructure"},
	{prefix: "cmd/", role: "command", domain: "infrastructure"},
	{prefix: "main.go", role: "entry-point", kind: "entry-point"},

	// Configuration
	{prefix: "config/", role: "configuration", domain: "infrastructure"},
	{prefix: "configs/", role: "configuration", domain: "infrastructure"},
	{prefix: "etc/", role: "configuration", domain: "infrastructure"},
	{prefix: "conf/", role: "configuration", domain: "infrastructure"},

	// Infrastructure / deployment
	{prefix: "pkg/deploy/", role: "deployment", domain: "infrastructure"},
	{prefix: "pkg/infra/", role: "infrastructure", domain: "infrastructure"},
	{prefix: "scripts/", role: "script", domain: "infrastructure"},
	{prefix: "tools/", role: "tool", domain: "infrastructure"},

	// Internal packages
	{prefix: "internal/", visible: "internal"},
	{prefix: "pkg/internal/", visible: "internal"},

	// Tests
	{prefix: "test/", role: "test", domain: "testing"},
	{prefix: "tests/", role: "test", domain: "testing"},

	// Documentation
	{prefix: "docs/", role: "documentation", domain: "meta"},
	{prefix: "doc/", role: "documentation", domain: "meta"},

	// Models / schemas
	{prefix: "pkg/model/", role: "model", domain: "data-access"},
	{prefix: "pkg/models/", role: "model", domain: "data-access"},
	{prefix: "pkg/schema/", role: "schema", domain: "data-access"},

	// Middleware
	{prefix: "middleware/", role: "middleware", domain: "networking"},
	{prefix: "pkg/middleware/", role: "middleware", domain: "networking"},

	// Utilities / helpers
	{prefix: "pkg/util/", role: "utility", domain: "support"},
	{prefix: "pkg/helpers/", role: "helper", domain: "support"},
	{prefix: "pkg/utils/", role: "utility", domain: "support"},

	// Metrics / observability
	{prefix: "pkg/metrics/", role: "metrics", domain: "observability"},
	{prefix: "pkg/telemetry/", role: "telemetry", domain: "observability"},
	{prefix: "pkg/tracing/", role: "tracing", domain: "observability"},
	{prefix: "pkg/logging/", role: "logging", domain: "observability"},

	// Queue / messaging
	{prefix: "pkg/queue/", role: "messaging", domain: "messaging"},
	{prefix: "pkg/msg/", role: "messaging", domain: "messaging"},
	{prefix: "pkg/events/", role: "event-handling", domain: "messaging"},

	// Cache
	{prefix: "pkg/cache/", role: "caching", domain: "data-access"},

	// Validation
	{prefix: "pkg/valid/", role: "validation", domain: "security"},
	{prefix: "pkg/validator/", role: "validation", domain: "security"},

	// Monitoring / health
	{prefix: "pkg/health/", role: "health-check", domain: "observability"},

	// Web / frontend
	{prefix: "web/", role: "web", domain: "frontend"},
	{prefix: "frontend/", role: "frontend", domain: "frontend"},
	{prefix: "static/", role: "static-assets", domain: "frontend"},
	{prefix: "assets/", role: "assets", domain: "frontend"},

	// gRPC / protobuf
	{prefix: "proto/", role: "protobuf", domain: "networking"},
	{prefix: "api/", role: "api-definition", domain: "networking"},
}

// namePatternRules match file name patterns to labels.
// These are checked after path prefix rules.
var namePatternRules = []struct {
	pattern string // simple glob-style: *, ?, [abc]
	role    string
	kind    string
	domain  string
}{
	{pattern: "*_test.go", role: "test", kind: "test"},
	{pattern: "*_tests.go", role: "test", kind: "test"},
	{pattern: "*_mock.go", role: "mock", kind: "mock"},
	{pattern: "*_fake.go", role: "fake", kind: "mock"},
	{pattern: "*_stub.go", role: "stub", kind: "mock"},
	{pattern: "middleware*.go", role: "middleware", domain: "networking"},
	{pattern: "config*.go", role: "configuration", domain: "infrastructure"},
	{pattern: "config*.yaml", role: "configuration", domain: "infrastructure"},
	{pattern: "config*.yml", role: "configuration", domain: "infrastructure"},
	{pattern: "config*.json", role: "configuration", domain: "infrastructure"},
	{pattern: "*.config.*", role: "configuration", domain: "infrastructure"},
	{pattern: "Makefile", role: "build", kind: "build-target", domain: "infrastructure"},
	{pattern: "docker-compose*.yml", role: "infrastructure", kind: "infrastructure"},
	{pattern: "docker-compose*.yaml", role: "infrastructure", kind: "infrastructure"},
	{pattern: "Dockerfile", role: "containerization", kind: "infrastructure", domain: "infrastructure"},
	{pattern: ".env*", role: "configuration", kind: "configuration", domain: "security"},
	{pattern: "*.proto", role: "protobuf", domain: "networking"},
	{pattern: "*.sql", role: "database-query", kind: "data-access", domain: "data-access"},
	{pattern: "*.tf", role: "infrastructure-as-code", kind: "iac", domain: "infrastructure"},
	{pattern: "*.tpl", role: "template", kind: "template"},
	{pattern: "*.tmpl", role: "template", kind: "template"},
}

// contentTypeRules map file extensions to content_type labels.
var contentTypeRules = map[string]string{
	".go":         "go",
	".py":         "python",
	".js":         "javascript",
	".ts":         "typescript",
	".tsx":        "typescript",
	".jsx":        "javascript",
	".rs":         "rust",
	".java":       "java",
	".cs":         "csharp",
	".cpp":        "cpp",
	".c":          "c",
	".h":          "c-header",
	".rb":         "ruby",
	".php":        "php",
	".swift":      "swift",
	".kt":         "kotlin",
	".md":         "markdown",
	".rst":        "rst",
	".yaml":       "yaml",
	".yml":        "yaml",
	".toml":       "toml",
	".json":       "json",
	".xml":        "xml",
	".sql":        "sql",
	".tf":         "hcl",
	".sh":         "shell",
	".bash":       "shell",
	".zsh":        "shell",
	".fish":       "shell",
	".ps1":        "powershell",
	".bat":        "batch",
	".dockerfile": "dockerfile",
	".proto":      "protobuf",
	".html":       "html",
	".css":        "css",
	".scss":       "scss",
	".less":       "less",
}

// DeriveLabels computes semantic labels for an entity based on its type and name/path.
// entityType must be KnownTypeFile or KnownTypeDirectory.
// name is the workspace-relative path for files, or directory path for directories.
func DeriveLabels(entityType EntityType, name string) LabelResult {
	var lr LabelResult

	// 1. Path prefix rules — highest priority
	lr.applyPathPrefix(name)

	// 2. Name pattern rules — for files only
	if entityType == KnownTypeFile {
		base := filepath.Base(name)
		lr.applyNamePattern(base)
	}

	// 3. Content type from extension — for files only
	if entityType == KnownTypeFile {
		lr.applyContentType(name)
	}

	return lr
}

// ToMetadata converts LabelResult to a metadata map, excluding zero-value fields.
func (lr *LabelResult) ToMetadata() map[string]any {
	m := map[string]any{}
	if lr.Role != "" {
		m["role"] = lr.Role
	}
	if lr.Domain != "" {
		m["domain"] = lr.Domain
	}
	if lr.Kind != "" {
		m["kind"] = lr.Kind
	}
	if lr.ContentType != "" {
		m["content_type"] = lr.ContentType
	}
	if lr.Visibility != "" {
		m["visibility"] = lr.Visibility
	}
	return m
}

func (lr *LabelResult) applyPathPrefix(path string) {
	// Normalize to forward slashes for consistent matching
	norm := filepath.ToSlash(path)
	// Also try with / appended for directory prefix matching
	normSlash := norm + "/"

	for _, rule := range pathPrefixRules {
		if strings.HasPrefix(norm, rule.prefix) || strings.HasPrefix(normSlash, rule.prefix) {
			if rule.role != "" && lr.Role == "" {
				lr.Role = rule.role
			}
			if rule.domain != "" && lr.Domain == "" {
				lr.Domain = rule.domain
			}
			if rule.kind != "" && lr.Kind == "" {
				lr.Kind = rule.kind
			}
			if rule.visible != "" && lr.Visibility == "" {
				lr.Visibility = rule.visible
			}
		}
	}
}

func (lr *LabelResult) applyNamePattern(baseName string) {
	for _, rule := range namePatternRules {
		if matchLabelPattern(baseName, rule.pattern) {
			if rule.role != "" && lr.Role == "" {
				lr.Role = rule.role
			}
			if rule.kind != "" && lr.Kind == "" {
				lr.Kind = rule.kind
			}
			// Domain only from name pattern if not already set
			if rule.domain != "" && lr.Domain == "" {
				lr.Domain = rule.domain
			}
			break // first match wins
		}
	}
}

func (lr *LabelResult) applyContentType(name string) {
	ext := strings.ToLower(filepath.Ext(name))
	if ct, ok := contentTypeRules[ext]; ok && lr.ContentType == "" {
		lr.ContentType = ct
	}
}

// matchLabelPattern matches baseName against a simple glob pattern.
// Supports: * (any chars), ? (single char), [abc] (char class).
func matchLabelPattern(name, pattern string) bool {
	return matchGlob([]byte(name), []byte(pattern))
}
