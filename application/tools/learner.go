package tools

// LearnerResultToolName is the dedicated periodic learner commit tool,
// analogous to compaction's summary(): the typed catalog result lives in the
// tool-call arguments (separate from reasoning and assistant text). The
// learner advertises a pruned toolbox and profile writes still use file_*.
const LearnerResultToolName = "learn"

// LearnerResultTool is advertised only to learner agent kinds.
var LearnerResultTool = ToolInfo{
	Name:        LearnerResultToolName,
	Description: "Submit the typed periodic learner result for memory catalog records. Call this exactly once after reviewing the source range. Do not put this object in assistant text. Profile documents still use file_patch/file_write.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"consolidate": map[string]any{
				"type":        "object",
				"description": "Periodic memory consolidation result.",
				"properties": map[string]any{
					"action": map[string]any{
						"type": "string",
						"enum": []any{"write", "update", "supersede", "no_op"},
					},
					"entry": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": map[string]any{
								"type": "string",
								"enum": []any{"fact", "preference", "procedure", "correction_of_prior_memory"},
							},
							"content":    map[string]any{"type": "string"},
							"evidence":   map[string]any{"type": "string"},
							"supersedes": map[string]any{"type": "string", "description": "Existing memory id; omit when none."},
							"scope": map[string]any{
								"type":        "string",
								"enum":        []any{"user", "project"},
								"description": "Use user for cross-project knowledge; use project for facts tied to the source project.",
							},
							"project": map[string]any{
								"type":        "string",
								"description": "Exact source project label; include only when scope is project.",
							},
						},
					},
					"reason_for_no_op": map[string]any{"type": "string"},
				},
				"required": []string{"action"},
			},
		},
		"required": []string{"consolidate"},
	},
}

// IsLearnerKind reports whether kind is the unified learner or a legacy alias.
func IsLearnerKind(kind AgentKind) bool {
	switch kind {
	case AgentLearner, AgentMemoryConsolidator, AgentSkillEvolver, AgentSkillEvaluator:
		return true
	default:
		return false
	}
}

// WithLearnerResultTool appends learn() when it is not already in defs.
func WithLearnerResultTool(defs []ToolInfo) []ToolInfo {
	if defs == nil {
		return nil
	}
	for _, d := range defs {
		if d.Name == LearnerResultToolName {
			return defs
		}
	}
	out := make([]ToolInfo, len(defs)+1)
	copy(out, defs)
	out[len(defs)] = LearnerResultTool
	return out
}
