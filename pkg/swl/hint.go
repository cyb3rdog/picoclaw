package swl

// SessionHint is a ~60-token injection that tells the LLM about SWL.
// It is prepended to the system prompt when InjectSessionHint is true.
// The full manual is the query_swl tool description itself.
const sessionHint = `Your workspace has a persistent semantic knowledge graph (SWL).
It updates automatically from all tool calls and LLM responses.
Start each session with: query_swl {"resume":true}
Use query_swl before re-reading files — it likely already knows what you need.`

// SessionHint returns the constant hint string.
func SessionHint() string { return sessionHint }
