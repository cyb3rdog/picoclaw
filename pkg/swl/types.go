package swl

// EntityType is an open string alias. KnownType* constants are conventions,
// not constraints — any string is valid in the DB schema.
type EntityType = string

const (
	KnownTypeFile           EntityType = "File"
	KnownTypeDirectory      EntityType = "Directory"
	KnownTypeSymbol         EntityType = "Symbol"
	KnownTypeDependency     EntityType = "Dependency"
	KnownTypeTask           EntityType = "Task"
	KnownTypeSection        EntityType = "Section"
	KnownTypeTopic          EntityType = "Topic"
	KnownTypeURL            EntityType = "URL"
	KnownTypeCommit         EntityType = "Commit"
	KnownTypeSession        EntityType = "Session"
	KnownTypeNote           EntityType = "Note"
	KnownTypeCommand        EntityType = "Command"
	KnownTypeIntent         EntityType = "Intent"
	KnownTypeSubAgent       EntityType = "SubAgent"
	KnownTypeTool           EntityType = "Tool"
	KnownTypeAnchorDocument EntityType = "AnchorDocument"
	KnownTypeSemanticArea   EntityType = "SemanticArea"
	KnownTypeAssertion      EntityType = "Assertion" // LLM-stated fact, linked to a real graph entity
)

// EdgeRel is an open string alias. KnownRel* constants are conventions,
// not constraints.
type EdgeRel = string

const (
	KnownRelDefines     EdgeRel = "defines"
	KnownRelImports     EdgeRel = "imports"
	KnownRelHasTask     EdgeRel = "has_task"
	KnownRelHasSection  EdgeRel = "has_section"
	KnownRelMentions    EdgeRel = "mentions"
	KnownRelDependsOn   EdgeRel = "depends_on"
	KnownRelTagged      EdgeRel = "tagged"
	KnownRelInDir       EdgeRel = "in_dir"
	KnownRelWrittenIn   EdgeRel = "written_in"
	KnownRelEditedIn    EdgeRel = "edited_in"
	KnownRelAppendedIn  EdgeRel = "appended_in"
	KnownRelRead        EdgeRel = "read"
	KnownRelFetched     EdgeRel = "fetched"
	KnownRelExecuted    EdgeRel = "executed"
	KnownRelDeleted     EdgeRel = "deleted"
	KnownRelDescribes   EdgeRel = "describes"
	KnownRelCommittedIn EdgeRel = "committed_in"
	KnownRelFound       EdgeRel = "found"
	KnownRelListed      EdgeRel = "listed"
	KnownRelSpawnedBy   EdgeRel = "spawned_by"
	KnownRelContextOf   EdgeRel = "context_of"
	KnownRelReasoned    EdgeRel = "reasoned"
	KnownRelIntendedFor EdgeRel = "intended_for"
	KnownRelUses        EdgeRel = "uses"
	KnownRelDocuments    EdgeRel = "documents"   // File(kind=anchor) → Directory it is in; marks the file as the anchor for that directory
	KnownRelHasArea      EdgeRel = "has_area"    // Directory → child Directory with is_semantic_area=true in metadata
	KnownRelCoOccursWith EdgeRel = "co_occurs_with" // Entities co-occurring in ≥4 sessions (autonomous loop)
)

// CascadeRels lists the ownership relations that propagate stale status from a
// parent entity to its direct children when the parent is deleted or its content
// changes.  These are "derived-from" relations: the child only makes sense in the
// context of the parent.  Add new ownership relations here when they are introduced.
var CascadeRels = []EdgeRel{
	KnownRelDefines,
	KnownRelHasTask,
	KnownRelHasSection,
	KnownRelMentions,
	KnownRelHasArea, // SemanticArea is owned by its parent Directory
}

// FactStatus governs correctness invariants — closed enum.
type FactStatus string

const (
	FactUnknown  FactStatus = "unknown"
	FactVerified FactStatus = "verified"
	FactStale    FactStatus = "stale"
	FactDeleted  FactStatus = "deleted"
)

// ExtractionMethod governs confidence precedence — closed enum.
type ExtractionMethod string

const (
	MethodObserved  ExtractionMethod = "observed"  // confidence 1.0
	MethodStated    ExtractionMethod = "stated"    // confidence 0.85
	MethodExtracted ExtractionMethod = "extracted" // confidence 0.9
	MethodInferred  ExtractionMethod = "inferred"  // confidence 0.8
)

// EntityTuple is the atomic unit for upsert operations.
type EntityTuple struct {
	ID               string
	Type             EntityType
	Name             string
	Metadata         map[string]any
	Confidence       float64
	ContentHash      string
	ExtractionMethod ExtractionMethod
	KnowledgeDepth   int
}

// EdgeTuple is the atomic unit for edge upsert operations.
type EdgeTuple struct {
	FromID    string
	Rel       EdgeRel
	ToID      string
	SessionID string
}

// GraphDelta is the atomic write unit produced by inference.
type GraphDelta struct {
	Entities []EntityTuple
	Edges    []EdgeTuple
}

// Merge appends other's entities and edges into d.
func (d *GraphDelta) Merge(other *GraphDelta) {
	if other == nil {
		return
	}
	d.Entities = append(d.Entities, other.Entities...)
	d.Edges = append(d.Edges, other.Edges...)
}

// IsEmpty reports whether the delta contains nothing to write.
func (d *GraphDelta) IsEmpty() bool {
	return len(d.Entities) == 0 && len(d.Edges) == 0
}
