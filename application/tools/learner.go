package tools

// LearnerResultToolName is the dedicated learner commit tool, analogous to
// compaction's summary(): the typed catalog result lives in the tool-call
// arguments (separate from reasoning and assistant text). The learner still
// advertises the full conversation toolbox for inspection and profile writes.
const LearnerResultToolName = "learn"

// LearnerResultTool is advertised only to learner agent kinds.
var LearnerResultTool = ToolInfo{
	Name:        LearnerResultToolName,
	Description: "Submit the typed learner result for catalog records. Call this exactly once when Stage 1 (and optional Stage 2/3) is finished. Do not put this object in assistant text. Profile documents still use file_patch/file_write.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"stage_reached": map[string]any{
				"type":        "string",
				"enum":        []any{"consolidate", "evaluate", "evolve"},
				"description": "Highest stage completed in this run.",
			},
			"consolidate": map[string]any{
				"type":        "object",
				"description": "Stage 1 result.",
				"properties": map[string]any{
					"stage": map[string]any{"type": "string", "enum": []any{"consolidate"}},
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
						},
					},
					"reason_for_no_op": map[string]any{"type": "string"},
				},
				"required": []string{"action"},
			},
			"evaluate": map[string]any{
				"type":        "object",
				"description": "Stage 2 result; omit unless trigger is repeated_procedure with count ≥ 3.",
				"properties": map[string]any{
					"stage":    map[string]any{"type": "string", "enum": []any{"evaluate"}},
					"approved": map[string]any{"type": "boolean"},
					"reason":   map[string]any{"type": "string"},
					"proposed_skill_shape": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":                map[string]any{"type": "string"},
							"trigger_description": map[string]any{"type": "string"},
							"steps_summary":       map[string]any{"type": "string"},
						},
					},
				},
			},
			"evolve": map[string]any{
				"type":        "object",
				"description": "Stage 3 result; omit unless evaluate.approved is true.",
				"properties": map[string]any{
					"stage":        map[string]any{"type": "string", "enum": []any{"evolve"}},
					"action":       map[string]any{"type": "string", "enum": []any{"create", "update"}},
					"skill_id":     map[string]any{"type": "string"},
					"diff_summary": map[string]any{"type": "string"},
				},
			},
		},
		"required": []string{"stage_reached", "consolidate"},
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
