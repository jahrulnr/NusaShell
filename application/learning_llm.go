package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
	"nusashell/resources"
)

// learningCallTimeout is the max wall-clock time for a single background
// learning LLM call. Background jobs are fire-and-forget; a hung provider
// must not leak a goroutine forever.
const learningCallTimeout = 90 * time.Second

// learningMaxRelatedMemories is the cap on related memory records passed to
// the consolidator packet. Keeps the packet small and the token cost bounded.
const learningMaxRelatedMemories = 20

// llmProposedOp is the JSON shape the LLM returns for each typed operation.
// It maps directly to domain.LearningOperation after validation.
type llmProposedOp struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
	Reason  string         `json:"reason,omitempty"`
	Risk    string         `json:"risk,omitempty"`
}

// llmSkillProposal is the JSON shape the LLM returns for a skill creation
// or revision. It carries the minimum RFC skill schema fields.
type llmSkillProposal struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Purpose       string `json:"purpose"`
	Trigger       string `json:"trigger"`
	Preconditions string `json:"preconditions,omitempty"`
	Steps         string `json:"steps"`
	Verification  string `json:"verification,omitempty"`
	Recovery      string `json:"recovery,omitempty"`
	AntiPatterns  string `json:"anti_patterns,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Risk          string `json:"risk,omitempty"`
}

// learningModelID returns the configured learning-job model override. An
// empty string means "let the headless turn resolve the first enabled
// provider", which is also the behavior when no override is set.
func (a *App) learningModelID() string {
	if a.Settings == nil {
		return ""
	}
	return strings.TrimSpace(a.Settings.Get().ReviewModel)
}

// runLearningTurn executes one learning-job LLM call as a headless agent turn
// and returns the final assistant text plus the id of the conversation that
// now holds its transcript.
//
// Routing the call through the agent turn loop (instead of a bare
// non-streaming completion) is what makes a background job auditable: the
// packet, every tool round, and the final answer are persisted as a
// background conversation, so the Learning log can show exactly what the
// model saw and did. The conversation id is returned even on failure — a
// failed call is precisely when the transcript matters most, as long as the
// turn got far enough to persist one.
// learningTurnAvailable reports whether the pieces a headless run needs are
// wired: a provider to resolve, a factory to build it, a conversation store
// to persist the transcript into, and the run registry tracking in-flight
// turns. A partial App (tests, a server without providers yet) is not an
// error to crash on — it means "no model available", and the caller falls
// back to deterministic extraction.
func (a *App) learningTurnAvailable() bool {
	return a != nil && a.Providers != nil && a.Factory != nil && a.Conversations != nil && a.runs != nil
}

func (a *App) runLearningTurn(ctx context.Context, kind AgentKind, model, packet string) (string, string, error) {
	if !a.learningTurnAvailable() {
		return "", "", fmt.Errorf("no learning model available")
	}
	ctx, cancel := context.WithTimeout(ctx, learningCallTimeout)
	defer cancel()
	out, convID, err := a.runHeadlessTurnKind(ctx, packet, model, domain.TrustTrusted, nil, kind)
	if err != nil {
		return "", convID, err
	}
	text, _ := out["output"].(string)
	if strings.TrimSpace(text) == "" {
		return "", convID, fmt.Errorf("empty response from learning model")
	}
	return strings.TrimSpace(text), convID, nil
}

// doLearningTurn runs one learning-job LLM call through the injected seam
// when a test installed one, otherwise through the real headless turn.
func (a *App) doLearningTurn(ctx context.Context, kind AgentKind, model, packet string) (string, string, error) {
	if a.learningTurn != nil {
		return a.learningTurn(ctx, kind, model, packet)
	}
	return a.runLearningTurn(ctx, kind, model, packet)
}

// extractJSONFromText finds the first JSON array or object in text that may
// be wrapped in markdown code fences or surrounded by prose. Returns the
// trimmed JSON substring or the original text if no fence markers are found.
func extractJSONFromText(text string) string {
	text = strings.TrimSpace(text)
	// Strip markdown code fences if present.
	if idx := strings.Index(text, "```"); idx >= 0 {
		rest := text[idx+3:]
		// Skip optional language tag (e.g. ```json).
		if nl := strings.Index(rest, "\n"); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			text = strings.TrimSpace(rest[:end])
		}
	}
	// Find the first '[' or '{' to trim leading prose.
	if arrIdx := strings.Index(text, "["); arrIdx >= 0 {
		objIdx := strings.Index(text, "{")
		if objIdx < 0 || arrIdx < objIdx {
			// Find matching closing bracket.
			trimmed := text[arrIdx:]
			if end := findMatchingBracket(trimmed, '[', ']'); end > 0 {
				return trimmed[:end+1]
			}
		}
	}
	if objIdx := strings.Index(text, "{"); objIdx >= 0 {
		trimmed := text[objIdx:]
		if end := findMatchingBracket(trimmed, '{', '}'); end > 0 {
			return trimmed[:end+1]
		}
	}
	return text
}

// findMatchingBracket finds the index of the closing bracket that matches the
// opening bracket at position 0, respecting string literals and nesting.
func findMatchingBracket(s string, open, close byte) int {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == open {
			depth++
		} else if c == close {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// parseLLMOperations parses an LLM JSON response into a list of typed
// learning operations. Malformed entries are skipped; valid entries are
// converted to domain.LearningOperation with proper IDs and metadata.
func parseLLMOperations(text string, jobID string, expID string) []domain.LearningOperation {
	jsonText := extractJSONFromText(text)
	var raw []llmProposedOp
	if err := json.Unmarshal([]byte(jsonText), &raw); err != nil {
		// Try single object wrapped in array.
		var single llmProposedOp
		if err2 := json.Unmarshal([]byte(jsonText), &single); err2 != nil {
			return nil
		}
		raw = []llmProposedOp{single}
	}
	now := clock.NewTime().Time()
	var ops []domain.LearningOperation
	for _, r := range raw {
		kind := strings.TrimSpace(r.Kind)
		if !domain.ValidLearningOpKind(kind) {
			continue
		}
		// Skip skill operations from the consolidator (section 12 of RFC).
		if strings.HasPrefix(kind, "skill.") {
			continue
		}
		ops = append(ops, domain.LearningOperation{
			ID:        domain.NewULID(domain.IDPrefixLearnOp),
			Kind:      kind,
			Status:    domain.LearningOpProposed,
			Actor:     domain.ActorConsolidator,
			JobID:     jobID,
			Payload:   r.Payload,
			Evidence:  []string{expID},
			Reason:    r.Reason,
			Risk:      r.Risk,
			CreatedAt: now,
		})
	}
	return ops
}

// buildConsolidatorPacket builds the user-prompt packet for the memory
// consolidator LLM call (RFC section 19). It includes the experience JSON,
// related retrievable memory records, and scope metadata.
func (a *App) buildConsolidatorPacket(exp *domain.Experience) string {
	var b strings.Builder
	b.WriteString(resources.ConsolidatorUserPrompt())
	b.WriteString("\n\n--- EXPERIENCE ---\n")
	expJSON, _ := json.MarshalIndent(exp, "", "  ")
	b.Write(expJSON)

	b.WriteString("\n\n--- RELATED MEMORIES ---\n")
	if a.MemoryRecords != nil {
		records := a.MemoryRecords.List()
		count := 0
		type memBrief struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Body     string `json:"body"`
			Scope    string `json:"scope"`
			Project  string `json:"project"`
			Status   string `json:"status"`
			Evidence int    `json:"evidence_count"`
		}
		var briefs []memBrief
		for _, r := range records {
			if r == nil || !r.Retrievable() {
				continue
			}
			briefs = append(briefs, memBrief{
				ID: r.ID, Type: r.Type, Body: r.Body,
				Scope: r.Scope.Level, Project: r.Scope.Project,
				Status: r.Status, Evidence: r.EvidenceCount,
			})
			count++
			if count >= learningMaxRelatedMemories {
				break
			}
		}
		if len(briefs) > 0 {
			memJSON, _ := json.MarshalIndent(briefs, "", "  ")
			b.Write(memJSON)
		} else {
			b.WriteString("[]")
		}
	} else {
		b.WriteString("[]")
	}

	b.WriteString("\n\n--- SCOPE ---\n")
	scope := map[string]string{
		"workspace":   exp.Scope.Workspace,
		"project":     exp.Scope.Project,
		"environment": exp.Scope.Environment,
	}
	scopeJSON, _ := json.MarshalIndent(scope, "", "  ")
	b.Write(scopeJSON)

	return b.String()
}

// buildSkillEvolverPacket builds the user-prompt packet for the skill
// evolver LLM call. It includes the experience JSON and any existing
// learned skills that might be related.
func (a *App) buildSkillEvolverPacket(exp *domain.Experience) string {
	var b strings.Builder
	b.WriteString(resources.SkillEvolverUserPrompt())
	b.WriteString("\n\n--- EXPERIENCE ---\n")
	expJSON, _ := json.MarshalIndent(exp, "", "  ")
	b.Write(expJSON)

	b.WriteString("\n\n--- CURRENT RELATED SKILLS ---\n")
	if a.Skills != nil {
		type skillBrief struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		var briefs []skillBrief
		for _, s := range a.Skills.List() {
			if s == nil || s.Origin != domain.SkillOriginLearned {
				continue
			}
			briefs = append(briefs, skillBrief{
				ID: s.ID, Name: s.Name, Status: string(s.Status),
			})
		}
		if len(briefs) > 0 {
			skillJSON, _ := json.MarshalIndent(briefs, "", "  ")
			b.Write(skillJSON)
		} else {
			b.WriteString("[]")
		}
	} else {
		b.WriteString("[]")
	}

	return b.String()
}

// parseLLMSkillProposal parses an LLM JSON response into a skill proposal.
// Returns nil if the response is malformed or missing required fields.
func parseLLMSkillProposal(text string) *llmSkillProposal {
	jsonText := extractJSONFromText(text)
	var prop llmSkillProposal
	if err := json.Unmarshal([]byte(jsonText), &prop); err != nil {
		return nil
	}
	if prop.Kind != "skill.create" && prop.Kind != "skill.revise" {
		return nil
	}
	if strings.TrimSpace(prop.Steps) == "" {
		return nil
	}
	return &prop
}

// skillMeetsMinimumBar checks that a generated skill body contains the
// minimum RFC schema fields: purpose, trigger, preconditions (optional),
// steps, and verification (optional but recommended). The body must answer
// at least "what problem does this solve" and "what sequence to execute".
func skillMeetsMinimumBar(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	// Check for the presence of key sections. We use case-insensitive
	// heading matching to be tolerant of different markdown styles.
	lower := strings.ToLower(body)
	required := []string{"purpose", "trigger", "steps"}
	for _, kw := range required {
		if !strings.Contains(lower, kw) {
			return false
		}
	}
	return true
}
