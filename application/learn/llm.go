package learn

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"nusashell/domain"
	clock "nusashell/pkg/time"
	"nusashell/resources"
)

// llmProposedOp is the JSON shape the LLM returns for each typed operation.
// It maps directly to domain.LearningOperation after validation.
type LLMProposedOp struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
	Reason  string         `json:"reason,omitempty"`
	Risk    string         `json:"risk,omitempty"`
}

// llmSkillProposal is the JSON shape the LLM returns for a skill creation
// or revision. It carries the minimum RFC skill schema fields.
type LLMSkillProposal struct {
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

type LearnerResult struct {
	StageReached string              `json:"stage_reached"`
	Consolidate  *learnerConsolidate `json:"consolidate"`
	Evaluate     *learnerEvaluate    `json:"evaluate"`
	Evolve       *learnerEvolve      `json:"evolve"`
}

type learnerConsolidate struct {
	Stage         string        `json:"stage"`
	Action        string        `json:"action"`
	Entry         *learnerEntry `json:"entry"`
	ReasonForNoOp string        `json:"reason_for_no_op,omitempty"`
}

type learnerEntry struct {
	Type       string `json:"type"`
	Content    string `json:"content"`
	Evidence   string `json:"evidence"`
	Supersedes string `json:"supersedes"`
	Scope      string `json:"scope"`
	Project    string `json:"project"`
}

type learnerEvaluate struct {
	Stage              string             `json:"stage"`
	Approved           bool               `json:"approved"`
	Reason             string             `json:"reason"`
	ProposedSkillShape *learnerSkillShape `json:"proposed_skill_shape"`
}

type learnerSkillShape struct {
	Name               string `json:"name"`
	TriggerDescription string `json:"trigger_description"`
	StepsSummary       string `json:"steps_summary"`
}

type learnerEvolve struct {
	Stage       string `json:"stage"`
	Action      string `json:"action"`
	SkillID     string `json:"skill_id"`
	DiffSummary string `json:"diff_summary"`
}

// learningModelID returns the configured learning-job model override. An
// empty string means "let the headless turn resolve the first enabled
// provider", which is also the behavior when no override is set.
func (s *Service) learningModelID() string {
	if s.deps.Settings == nil {
		return ""
	}
	return strings.TrimSpace(s.deps.Settings.Get().ReviewModel)
}

// runLearningTurn executes one learning-job LLM call as a headless agent turn
// and returns the final assistant text plus the id of the conversation that
// now holds its transcript.
//
// Routing the call through the agent turn loop (instead of a bare
// non-streaming completion) is what makes a background job auditable: the
// short source handoff, every tool round, and the final answer are persisted as a
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
func (s *Service) learningTurnAvailable() bool {
	return s != nil && (s.deps.LearningTurn != nil || s.deps.Headless != nil)
}

func (s *Service) workspaceCtx(exp *domain.Experience) context.Context {
	ctx := context.Background()
	if exp == nil || s == nil || s.deps.WithWorkspace == nil {
		return ctx
	}
	return s.deps.WithWorkspace(ctx, exp.Scope.Workspace)
}

func (s *Service) runLearningTurn(ctx context.Context, model, prompt string) (string, string, error) {
	if s.deps.Headless == nil {
		return "", "", fmt.Errorf("no learning model available")
	}
	out, convID, err := s.deps.Headless.RunHeadlessTurn(ctx, prompt, model, domain.TrustTrusted, nil)
	if err != nil {
		return "", convID, err
	}
	text, _ := out["output"].(string)
	if convID != "" && s.deps.Conversations != nil {
		if conv, getErr := s.deps.Conversations.Get(convID); getErr == nil {
			text = LearnerTurnOutput(conv, text)
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", convID, fmt.Errorf("empty response from learning model")
	}
	return text, convID, nil
}

// doLearningTurn runs one learning-job LLM call through the injected seam
// when a test installed one, otherwise through the real headless turn.
func (s *Service) doLearningTurn(ctx context.Context, model, prompt string) (string, string, error) {
	if s.deps.LearningTurn != nil {
		return s.deps.LearningTurn(ctx, model, prompt)
	}
	return s.runLearningTurn(ctx, model, prompt)
}

// extractJSONFromText finds the first JSON array or object in text that may
// be wrapped in markdown code fences or surrounded by prose. Returns the
// trimmed JSON substring or the original text if no fence markers are found.
func ExtractJSONFromText(text string) string {
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
func ParseLLMOperations(text string, jobID string, expID string) []domain.LearningOperation {
	ops, _ := ParseLLMOperationsResult(text, jobID, expID)
	return ops
}

func ParseLLMOperationsResult(text string, jobID string, expID string) ([]domain.LearningOperation, bool) {
	jsonText := ExtractJSONFromText(text)
	var raw []LLMProposedOp
	if err := json.Unmarshal([]byte(jsonText), &raw); err != nil {
		// Try single object wrapped in array.
		var single LLMProposedOp
		if err2 := json.Unmarshal([]byte(jsonText), &single); err2 != nil {
			return nil, false
		}
		raw = []LLMProposedOp{single}
	}
	if raw == nil && strings.TrimSpace(jsonText) == "null" {
		return nil, false
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
			Actor:     domain.ActorLearner,
			JobID:     jobID,
			Payload:   r.Payload,
			Evidence:  []string{expID},
			Reason:    r.Reason,
			Risk:      r.Risk,
			CreatedAt: now,
		})
	}
	if len(raw) > 0 && len(ops) == 0 {
		return nil, false
	}
	return ops, true
}

type LearningSource struct {
	ConversationID   string
	Path             string
	Project          string
	MessageStart     int
	MessageEnd       int
	BoundaryCaptured bool
}

// learningSourceForExperience resolves the source conversation handoff for a
// background learning turn. The source content stays in the persisted
// conversation file; only its stable location, source project label, and
// incremental message range are put in the background user's short
// instruction.
func (s *Service) LearningSourceForExperience(exp *domain.Experience) LearningSource {
	if exp == nil {
		return LearningSource{}
	}
	source := LearningSource{
		ConversationID: strings.TrimSpace(exp.ConversationID),
		Project:        strings.TrimSpace(exp.Scope.Project),
	}
	if source.ConversationID == "" || s == nil {
		return source
	}
	if s.deps.Conversations != nil {
		if conversation, err := s.deps.Conversations.Get(source.ConversationID); err == nil && conversation != nil {
			source.MessageStart, source.MessageEnd = LearningMessageRangeForConversation(conversation)
			source.BoundaryCaptured = true
		}
	}
	if s.deps.ConversationPath != nil {
		source.Path = absConversationPath(s.deps.ConversationPath(source.ConversationID))
	}
	// The production JSON store implements ConversationFileLocator. Keep the
	// standard data-root fallback for test/custom stores that expose the same
	// persisted layout only through App.DataDir.
	if source.Path == "" {
		source.Path = learningConversationFallbackPath(s.deps.DataDir, source.ConversationID)
	}
	return source
}

func learningMessageRange(store ConversationStore, conversationID string) (int, int) {
	if store == nil {
		return 0, 0
	}
	conversation, err := store.Get(conversationID)
	if err != nil || conversation == nil {
		return 0, 0
	}
	return LearningMessageRangeForConversation(conversation)
}

func LearningMessageRangeForConversation(conversation *domain.Conversation) (int, int) {
	if conversation == nil {
		return 0, 0
	}
	start := conversation.LastReviewedMsgCount
	if start < 0 || start > len(conversation.Messages) {
		start = 0
	}
	return start, len(conversation.Messages)
}

func absConversationPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absolute
}

func learningConversationFallbackPath(dataDir, conversationID string) string {
	if dataDir == "" || !safeLearningConversationID(conversationID) {
		return ""
	}
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		return ""
	}
	return filepath.Join(absolute, "conversations", conversationID+".json")
}

func safeLearningConversationID(conversationID string) bool {
	return conversationID != "" &&
		filepath.Base(conversationID) == conversationID &&
		!strings.ContainsAny(conversationID, `/\`) &&
		!strings.Contains(conversationID, "..") &&
		!strings.ContainsRune(conversationID, 0)
}

// buildLearnerPacketAt builds the short user instruction for one unified
// learner turn (Stage 1 always; Stage 2/3 only when trigger_reason is
// repeated_procedure). Experience and memory bodies are not serialized
// into role=user; the agent reads the source conversation through file_read.
//
// The skill-authoring reference for Stages 2-3 is NOT carried here: the
// runtime attaches the skill-creator SKILL.md as a hydration file_read slot
// (see learnerSkillCreatorReference), so the model receives it as a tool
// result without having to search for it.
func (s *Service) BuildLearnerPacketAt(exp *domain.Experience, source LearningSource, reason string, procedureCount int) string {
	project := strings.TrimSpace(source.Project)
	if project == "" && exp != nil {
		project = strings.TrimSpace(exp.Scope.Project)
	}
	return resources.RenderLearnerUserPromptForProject(
		reason,
		procedureCount,
		source.ConversationID,
		source.Path,
		source.MessageStart,
		source.MessageEnd,
		project,
	)
}

// learnerSkillCreatorReference resolves the skill-creator SKILL.md for the
// learner hydration slot: the live skill store wins (the editable copy the
// skill tools also serve), and the embedded bundle guarantees presence when
// the live copy was deleted or not yet seeded. Returns ("", "") only when
// neither source is available or the data directory is unknown.
func (s *Service) LearnerSkillCreatorReference() (path, content string) {
	if s == nil || strings.TrimSpace(s.deps.DataDir) == "" {
		return "", ""
	}
	content = ""
	if s.deps.Skills != nil {
		if sk, err := s.deps.Skills.Get("skill-creator", ""); err == nil && sk != nil {
			content = strings.TrimSpace(sk.Content)
		}
	}
	if content == "" {
		content = strings.TrimSpace(resources.BuiltinSkill("skill-creator"))
	}
	if content == "" {
		return "", ""
	}
	// Forward slashes in the agent file_read path keep hydration / tool
	// slots portable across Windows (filepath.Join would use backslashes).
	return filepath.ToSlash(filepath.Join(strings.TrimRight(s.deps.DataDir, `/\`), "skills", "skill-creator", "SKILL.md")), content
}

// parseLLMSkillProposal parses an LLM JSON response into a skill proposal.
// Returns nil if the response is malformed or missing required fields.
func ParseLLMSkillProposal(text string) *LLMSkillProposal {
	jsonText := ExtractJSONFromText(text)
	var prop LLMSkillProposal
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
func SkillMeetsMinimumBar(body string) bool {
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
