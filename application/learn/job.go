package learn

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
	"nusashell/resources"
)

func (s *Service) RunLearningJob(id string) {
	job := s.loadLearningJob(id)
	if job == nil {
		return
	}
	// Capture the source before dispatch. The prompt and cursor update must
	// refer to this same boundary even if the transcript grows while the
	// background turn is running.
	source := s.captureLearningSource(job)
	s.startLearningJob(job)
	convID, ops, runErr, sourceReviewed := s.executeLearningJob(job, source)
	if convID != "" {
		job.LLMConversationID = convID
	}
	s.finishLearningJob(job, runErr)
	_ = s.deps.Jobs.Save(job)
	s.advanceLearningCursorAfterReview(source, runErr, sourceReviewed)
	s.recordLearningJobTrajectory(job, convID, ops)
	s.emitMemoryUpdated()
}

func (s *Service) loadLearningJob(id string) *domain.LearningJob {
	if s == nil || s.deps.Jobs == nil {
		return nil
	}
	job, err := s.deps.Jobs.Get(id)
	if err != nil {
		return nil
	}
	return job
}

func (s *Service) startLearningJob(job *domain.LearningJob) {
	now := clock.NewTime().Time()
	job.Status = domain.LearningJobRunning
	job.StartedAt = &now
	_ = s.deps.Jobs.Save(job)
	if s.deps.Bus != nil {
		s.deps.Bus.Emit(contracts.EventLearningJobStarted, contracts.LearningJobEvent{
			JobID:  job.ID,
			Kind:   job.Kind,
			Status: job.Status,
		})
	}
}

func (s *Service) executeLearningJob(job *domain.LearningJob, source *LearningSource) (string, []domain.LearningOperation, error, bool) {
	switch job.Kind {
	case domain.LearningJobLearner, domain.LearningJobConsolidate:
		ops, convID, err, reviewed := s.consolidateJobAt(job, source)
		return convID, ops, err, reviewed
	case domain.LearningJobEvolveSkill:
		convID, err, reviewed := s.evolveSkillJobAt(job, source)
		return convID, nil, err, reviewed
	case domain.LearningJobEvaluate:
		return "", nil, s.EvaluateSkillJob(job), false
	case domain.LearningJobRetire:
		if s.lifecycle != nil {
			s.lifecycle.PruneOnce()
		}
		return "", nil, nil, false
	default:
		return "", nil, fmt.Errorf("unknown job kind %s", job.Kind), false
	}
}

func (s *Service) finishLearningJob(job *domain.LearningJob, runErr error) {
	done := clock.NewTime().Time()
	job.FinishedAt = &done
	if runErr != nil {
		job.Status = domain.LearningJobError
		job.Error = runErr.Error()
		s.log("warn", "learning", "job error: id=%s kind=%s err=%v", job.ID, job.Kind, runErr)
		if s.deps.Bus != nil {
			s.deps.Bus.Emit(contracts.EventLearningJobError, contracts.LearningJobEvent{
				JobID:  job.ID,
				Kind:   job.Kind,
				Status: job.Status,
				Error:  "learning job failed",
			})
		}
		return
	}
	job.Status = domain.LearningJobDone
	if s.deps.Bus != nil {
		s.deps.Bus.Emit(contracts.EventLearningJobDone, contracts.LearningJobEvent{
			JobID:  job.ID,
			Kind:   job.Kind,
			Status: job.Status,
		})
	}
}

func (s *Service) advanceLearningCursorAfterReview(source *LearningSource, runErr error, sourceReviewed bool) {
	if runErr != nil || !sourceReviewed {
		return
	}
	if err := s.AdvanceLearningCursor(source); err != nil {
		// The learning mutation already succeeded. Do not turn a marker
		// persistence failure into a duplicate-prone job retry.
		s.log("warn", "learning", "cursor persistence failed: conversation=%s boundary=%d err=%v", source.ConversationID, source.MessageEnd, err)
	}
}

func (s *Service) captureLearningSource(job *domain.LearningJob) *LearningSource {
	source := &LearningSource{}
	if job == nil ||
		(job.Kind != domain.LearningJobLearner && job.Kind != domain.LearningJobConsolidate && job.Kind != domain.LearningJobEvolveSkill) ||
		s == nil || s.deps.Experiences == nil || job.ExperienceID == "" {
		return source
	}
	exp, err := s.deps.Experiences.Get(job.ExperienceID)
	if err != nil || exp == nil {
		return source
	}
	captured := s.LearningSourceForExperience(exp)
	return &captured
}

func (s *Service) resolveLearningSource(exp *domain.Experience, source *LearningSource) LearningSource {
	if source != nil {
		return *source
	}
	return s.LearningSourceForExperience(exp)
}

func (s *Service) AdvanceLearningCursor(source *LearningSource) error {
	if s == nil || s.deps.Conversations == nil || source == nil ||
		!source.BoundaryCaptured || source.ConversationID == "" {
		return nil
	}

	if s.deps.LockConversation != nil {
		unlock := s.deps.LockConversation(source.ConversationID)
		if unlock != nil {
			defer unlock()
		}
	}

	conversation, err := s.deps.Conversations.Get(source.ConversationID)
	if err != nil {
		return err
	}
	if conversation == nil {
		return fmt.Errorf("conversation %s is missing", source.ConversationID)
	}

	currentStart, currentEnd := LearningMessageRangeForConversation(conversation)
	capturedEnd := source.MessageEnd
	if capturedEnd < 0 {
		capturedEnd = 0
	}
	if capturedEnd > currentEnd {
		capturedEnd = currentEnd
	}
	target := currentStart
	if capturedEnd > target {
		target = capturedEnd
	}
	if target == conversation.LastReviewedMsgCount {
		return nil
	}

	conversation.LastReviewedMsgCount = target
	if s.deps.PersistConversation != nil {
		return s.deps.PersistConversation(conversation)
	}
	return nil
}

// recordLearningJobTrajectory appends a finished job's outcome to the
// learning trajectory so the Learning log feed shows it. Skill evolution
// already records its own lifecycle event (with the transcript id) inside
// evolveSkillJob, so only jobs that would otherwise be invisible in the feed
// are recorded here.
func (s *Service) recordLearningJobTrajectory(job *domain.LearningJob, convID string, ops []domain.LearningOperation) {
	if s.deps.Trajectory == nil || job == nil {
		return
	}
	switch job.Kind {
	case domain.LearningJobLearner, domain.LearningJobConsolidate:
		s.deps.Trajectory.Record("consolidate", learningJobDetail(job, convID, ops))
	}
}

// learningJobDetail builds the trajectory detail for a finished learning job.
// job_id ties the entry back to learning.jobs.status and llm_conversation_id
// points at the persisted transcript, so the feed can open the exact LLM run
// that produced this outcome. The raw error is deliberately omitted: the
// generic failure line in the UI is enough, and provider bodies stay
// server-side.
func learningJobDetail(job *domain.LearningJob, convID string, ops []domain.LearningOperation) map[string]interface{} {
	detail := map[string]interface{}{
		"job_id": job.ID,
		"kind":   string(job.Kind),
		"status": string(job.Status),
	}
	if convID != "" {
		detail["llm_conversation_id"] = convID
	}
	if mutations := learningJobMutations(ops); len(mutations) > 0 {
		detail["mutations"] = mutations
	}
	return detail
}

// learningJobMutations converts applied learning operations into the compact
// rows the Learning feed renders. The snippet prefers the stored body, then a
// named target (skills), then the model's stated reason, so a row is never
// blank.
func learningJobMutations(ops []domain.LearningOperation) []map[string]string {
	out := make([]map[string]string, 0, len(ops))
	for _, op := range ops {
		snippet := PayloadString(op.Payload, "body")
		if snippet == "" {
			snippet = PayloadString(op.Payload, "name")
		}
		if snippet == "" {
			snippet = op.Reason
		}
		out = append(out, map[string]string{"kind": string(op.Kind), "snippet": clip(snippet, 180)})
	}
	return out
}

// consolidateJob runs the consolidation for one job and returns the applied
// operations plus the id of the conversation holding the LLM transcript.
// The conversation id is returned even when the job produced nothing or
// failed: the transcript is exactly what explains that outcome.
func (s *Service) ConsolidateJob(job *domain.LearningJob) ([]domain.LearningOperation, string, error) {
	ops, convID, err, _ := s.consolidateJobAt(job, nil)
	return ops, convID, err
}

func (s *Service) consolidateJobAt(job *domain.LearningJob, source *LearningSource) ([]domain.LearningOperation, string, error, bool) {
	if s.deps.Experiences == nil || s.deps.Records == nil {
		return nil, "", fmt.Errorf("growth stores not configured"), false
	}
	exp, err := s.deps.Experiences.Get(job.ExperienceID)
	if err != nil {
		return nil, "", err, false
	}
	sourceValue := s.resolveLearningSource(exp, source)

	// Try the LLM-backed consolidator first (RFC section 14, 18-19). When a
	// learning model is available, the consolidator receives a short source
	// handoff and returns typed operations. If the LLM
	// path fails or no provider is configured, fall back to the
	// deterministic rule-based extraction (teachingOps) so the job still
	// produces output in offline/no-provider setups.
	ops, convID, sourceReviewed := s.consolidateViaLLMAt(job, exp, sourceValue)
	if !sourceReviewed {
		ops = consolidationOpsOrFallback(ops, exp, job.ID)
	}
	if len(ops) == 0 {
		return nil, convID, nil, sourceReviewed
	}
	return s.ApplyConsolidationOps(ops, convID, sourceReviewed, exp)
}

func consolidationOpsOrFallback(ops []domain.LearningOperation, exp *domain.Experience, jobID string) []domain.LearningOperation {
	if len(ops) > 0 {
		return ops
	}
	return TeachingOps(exp, jobID)
}

func (s *Service) ApplyConsolidationOps(ops []domain.LearningOperation, convID string, sourceReviewed bool, exp *domain.Experience) ([]domain.LearningOperation, string, error, bool) {
	userTexts := domain.UserTextsForExperience(exp)
	var firstErr error
	accepted := 0
	for i := range ops {
		if ops[i].Kind == domain.OpMemoryUpsert {
			if body := PayloadString(ops[i].Payload, "body"); !domain.ValidDurableMemoryBody(body, userTexts) {
				if s.deps.RejectMemory != nil {
					s.deps.RejectMemory(&ops[i], "rejected: not durable memory (question, raw user echo, or trivial fragment)")
				}
				continue
			}
		}
		s.PrepareConsolidationOp(&ops[i])
		if s.deps.ApplyMemory == nil {
			return ops, convID, fmt.Errorf("memory applier not configured"), false
		}
		if err := s.deps.ApplyMemory(&ops[i]); err != nil {
			s.log("warn", "learning", "consolidation op rejected: kind=%s target=%s err=%v", ops[i].Kind, memoryOpTargetID(&ops[i]), err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		accepted++
	}
	if accepted == 0 && firstErr != nil {
		return ops, convID, firstErr, false
	}
	return ops, convID, nil, sourceReviewed
}

// nearDuplicateMemoryThreshold is the minimum overlap at which an incoming
// body counts as a near-duplicate of an existing record. Above it the
// runtime merges the new evidence into the existing record instead of
// creating a parallel entry (or strengthens it when the text is identical).
const nearDuplicateMemoryThreshold = 0.55

// findMemoryMatch locates the existing record that an incoming memory
// upsert should target instead of creating a fresh duplicate. It returns
// (id, exact): exact=true for identical normalized bodies (the caller can
// strengthen in place); exact=false for near-duplicates the caller should
// merge into. Matching is scoped to the same type and project.
func findMemoryMatch(store RecordStore, body, typ, project string) (id string, exact bool) {
	if store == nil {
		return "", false
	}
	want := domain.NormalizeMemoryContent(body)
	if want == "" {
		return "", false
	}
	bestID, bestSim := "", 0.0
	for _, rec := range store.List() {
		if rec == nil || !rec.Retrievable() {
			continue
		}
		if typ != "" && rec.Type != typ {
			continue
		}
		if project != "" && !strings.EqualFold(rec.Scope.Project, project) {
			continue
		}
		if domain.NormalizeMemoryContent(rec.Body) == want {
			return rec.ID, true
		}
		if sim := domain.MemorySimilarity(body, rec.Body); sim > bestSim {
			bestSim = sim
			bestID = rec.ID
		}
	}
	if bestSim >= nearDuplicateMemoryThreshold {
		return bestID, false
	}
	return "", false
}

func (s *Service) PrepareConsolidationOp(op *domain.LearningOperation) {
	if op.Kind != domain.OpMemoryUpsert {
		return
	}
	body := PayloadString(op.Payload, "body")
	typ := PayloadString(op.Payload, "type")
	project := PayloadString(op.Payload, "project")
	id, exact := findMemoryMatch(s.deps.Records, body, typ, project)
	if id == "" {
		return
	}
	if op.Payload == nil {
		op.Payload = map[string]any{}
	}
	op.Payload["id"] = id
	if exact {
		// Purely identical text: strengthen the existing record rather than
		// writing a second copy. Near-duplicates stay OpMemoryUpsert and
		// MemoryService merges them into the existing record by id.
		op.Kind = domain.OpMemoryStrengthen
	}
}

// consolidateViaLLM calls the LLM-backed memory consolidator and parses the
// typed operations from its response. It returns the parsed operations and
// the id of the conversation holding the call's transcript.
//
// The conversation id comes back even when ops is empty: "the model saw the
// source handoff and decided nothing was durable" and "the model was unreachable"
// are different answers, and only the transcript tells them apart. The
// caller falls back to deterministic extraction when this returns no ops.
// Typed catalog results prefer the learn() tool-call arguments; assistant
// text is a fallback.
func (s *Service) ConsolidateViaLLM(job *domain.LearningJob, exp *domain.Experience) ([]domain.LearningOperation, string) {
	ops, convID, _ := s.consolidateViaLLMAt(job, exp, s.LearningSourceForExperience(exp))
	return ops, convID
}

func (s *Service) consolidateViaLLMAt(job *domain.LearningJob, exp *domain.Experience, source LearningSource) ([]domain.LearningOperation, string, bool) {
	if strings.TrimSpace(resources.LearnerPrompt()) == "" {
		return nil, "", false
	}
	prompt := s.BuildLearnerPacketAt(exp, source, job.Reason, procedureCountForJob(s, exp, job))
	text, convID, err := s.doLearningTurn(s.workspaceCtx(exp), s.learningModelID(), prompt)
	if err != nil {
		s.log("debug", "learning", "learner LLM call failed, using deterministic fallback: %v", err)
		return nil, convID, false
	}
	if result := ParseLearnerResult(text); result != nil {
		ops := OpsFromLearnerConsolidate(result.Consolidate, job.ID, exp.ID)
		if job.Reason == domain.TriggerRepeatedProcedure && result.Evaluate != nil && result.Evaluate.Approved {
			s.applyLearnerEvolve(job, exp, result)
		}
		if len(ops) == 0 {
			s.log("debug", "learning", "learner LLM returned no operations")
			return nil, convID, true
		}
		s.log("info", "learning", "learner LLM returned %d operations", len(ops))
		return ops, convID, true
	}
	ops, parsed := ParseLLMOperationsResult(text, job.ID, exp.ID)
	if parsed {
		if len(ops) == 0 {
			s.log("debug", "learning", "learner LLM returned no operations")
			return nil, convID, true
		}
		s.log("info", "learning", "learner LLM returned %d operations", len(ops))
		return ops, convID, true
	}
	s.log("debug", "learning", "learner LLM response could not be parsed")
	return nil, convID, false
}

func procedureCountForJob(s *Service, exp *domain.Experience, job *domain.LearningJob) int {
	if s == nil || exp == nil || job == nil || job.Reason != domain.TriggerRepeatedProcedure {
		return 0
	}
	fp := strings.TrimSpace(exp.Signals.ProcedureFingerprint)
	if fp == "" || s.deps.Experiences == nil {
		return 0
	}
	n := 1
	for _, h := range s.deps.Experiences.ListByConversation(exp.ConversationID) {
		if h == nil || h.ID == exp.ID {
			continue
		}
		if h.Signals.ProcedureFingerprint == fp {
			n++
		}
	}
	return n
}

func ParseLearnerResult(text string) *LearnerResult {
	jsonText := ExtractJSONFromText(text)
	var result LearnerResult
	if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		return nil
	}
	if strings.TrimSpace(result.StageReached) == "" && result.Consolidate == nil {
		return nil
	}
	return &result
}

func OpsFromLearnerConsolidate(stage *learnerConsolidate, jobID, expID string) []domain.LearningOperation {
	if stage == nil {
		return nil
	}
	action := strings.TrimSpace(strings.ToLower(stage.Action))
	if action == "" || action == "no_op" {
		return nil
	}
	if stage.Entry == nil {
		return nil
	}
	evidence := strings.TrimSpace(stage.Entry.Evidence)
	content := strings.TrimSpace(stage.Entry.Content)
	if evidence == "" || content == "" {
		return nil
	}
	now := clock.NewTime().Time()
	typ := memoryTypeFromLearner(stage.Entry.Type)
	supersedes := strings.TrimSpace(stage.Entry.Supersedes)
	if strings.EqualFold(supersedes, "null") {
		supersedes = ""
	}
	var ops []domain.LearningOperation
	if action == "supersede" && supersedes != "" {
		ops = append(ops, domain.LearningOperation{
			ID:       domain.NewULID(domain.IDPrefixLearnOp),
			Kind:     domain.OpMemoryContradict,
			Status:   domain.LearningOpProposed,
			Actor:    domain.ActorLearner,
			JobID:    jobID,
			TargetID: supersedes,
			Payload: map[string]any{
				"id": supersedes,
			},
			Evidence:  []string{expID, evidence},
			Reason:    "learner supersede",
			CreatedAt: now,
		})
	}
	kind := domain.OpMemoryUpsert
	if action == "update" {
		kind = domain.OpMemoryUpsert
	}
	ops = append(ops, domain.LearningOperation{
		ID:       domain.NewULID(domain.IDPrefixLearnOp),
		Kind:     kind,
		Status:   domain.LearningOpProposed,
		Actor:    domain.ActorLearner,
		JobID:    jobID,
		Evidence: []string{expID, evidence},
		Payload: map[string]any{
			"body":  content,
			"type":  typ,
			"scope": domain.MemoryScopeUser,
		},
		CreatedAt: now,
	})
	return ops
}

func memoryTypeFromLearner(t string) string {
	switch strings.TrimSpace(strings.ToLower(t)) {
	case domain.MemoryTypeFact:
		return domain.MemoryTypeFact
	case domain.MemoryTypePreference:
		return domain.MemoryTypePreference
	case "procedure":
		return domain.MemoryTypeConstraint
	case "correction_of_prior_memory":
		return domain.MemoryTypePreference
	default:
		return domain.MemoryTypeBelief
	}
}

func (s *Service) applyLearnerEvolve(job *domain.LearningJob, exp *domain.Experience, result *LearnerResult) {
	if s == nil || s.deps.Skills == nil || exp == nil || result == nil || result.Evaluate == nil || !result.Evaluate.Approved {
		return
	}
	name := LearnedSkillName(exp.Goal)
	if result.Evolve != nil && strings.TrimSpace(result.Evolve.SkillID) != "" {
		name = LearnedSkillName(result.Evolve.SkillID)
	} else if result.Evaluate.ProposedSkillShape != nil && strings.TrimSpace(result.Evaluate.ProposedSkillShape.Name) != "" {
		name = LearnedSkillName(result.Evaluate.ProposedSkillShape.Name)
	}
	body, description := learnerSkillBody(result, exp)
	if !SkillMeetsMinimumBar(body) {
		body, description = s.DeterministicSkillBody(exp)
	}
	if !SkillMeetsMinimumBar(body) {
		return
	}
	skill := NewLearnedSkill(name, description, body)
	if !s.ApplyLearnedSkillRevision(skill, name) {
		return
	}
	skill.EnsureStatusDefault()
	if domain.CreatorMayPromote(domain.ActorLearner) {
		return
	}
	if err := s.deps.Skills.Save(skill); err != nil {
		s.log("warn", "learning", "learner evolve save failed: %v", err)
		return
	}
	s.EmitSkillLifecycle("evolve", skill.ID, string(skill.Status), "")
}

func learnerSkillBody(result *LearnerResult, exp *domain.Experience) (string, string) {
	var name, trigger, steps string
	if result != nil && result.Evaluate != nil && result.Evaluate.ProposedSkillShape != nil {
		shape := result.Evaluate.ProposedSkillShape
		name = strings.TrimSpace(shape.Name)
		trigger = strings.TrimSpace(shape.TriggerDescription)
		steps = strings.TrimSpace(shape.StepsSummary)
	}
	if name == "" {
		name = "Learned Workflow"
	}
	if trigger == "" {
		trigger = "When the same goal recurs and this procedure matches the task context."
	}
	if steps == "" && exp != nil {
		var b strings.Builder
		for i, act := range exp.Actions {
			fmt.Fprintf(&b, "%d. `%s`\n", i+1, act.Name)
		}
		steps = strings.TrimSpace(b.String())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", name)
	b.WriteString("## Purpose\n")
	if exp != nil {
		b.WriteString("Repeat the verified workflow for: ")
		b.WriteString(clip(exp.Goal, 180))
		b.WriteString(".\n\n")
	} else {
		b.WriteString(name + "\n\n")
	}
	b.WriteString("## Trigger\n")
	b.WriteString(trigger)
	b.WriteString("\n\n## Steps\n")
	b.WriteString(steps)
	b.WriteString("\n")
	desc := clip(name, 200)
	if exp != nil && desc == name {
		desc = clip(exp.Goal, 200)
	}
	return b.String(), desc
}

// teachingOps is the deterministic no-provider fallback. It may only emit
// distilled corrections (Desired behavior), never raw user text: the
// durability gate rejects anything that echoes the user's own words, so
// emitting them here would produce rejected operations. Extraction does not
// populate Desired yet, which means the fallback is intentionally quiet
// today — fabricating "preferences" from raw steers was the source of the
// verbatim junk records (e.g. a user question stored as a preference).
func TeachingOps(exp *domain.Experience, jobID string) []domain.LearningOperation {
	if exp == nil {
		return nil
	}
	var ops []domain.LearningOperation
	now := clock.NewTime().Time()
	add := func(body, typ string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		ops = append(ops, domain.LearningOperation{
			ID:       domain.NewULID(domain.IDPrefixLearnOp),
			Kind:     domain.OpMemoryUpsert,
			Status:   domain.LearningOpProposed,
			Actor:    domain.ActorLearner,
			JobID:    jobID,
			Evidence: []string{exp.ID},
			Payload: map[string]any{
				"body":    body,
				"type":    typ,
				"scope":   domain.MemoryScopeUser,
				"project": exp.Scope.Project,
			},
			CreatedAt: now,
		})
	}
	for _, c := range exp.Corrections {
		text := strings.TrimSpace(c.Desired)
		if text == "" {
			// No distilled form: skip. UserSaid would be a raw echo and the
			// gate rejects it anyway.
			continue
		}
		kind := domain.MemoryTypePreference
		if c.Type == "fact" {
			kind = domain.MemoryTypeFact
		}
		if c.Type == "procedure" {
			kind = domain.MemoryTypeConstraint
		}
		add(text, kind)
	}
	return ops
}

// evolveSkillJob proposes one learned skill and returns the id of the
// conversation holding the evolver's transcript. The id comes back even when
// no skill was written: "the model declined to propose" and "the model was
// unreachable" look identical in the log without it.
func (s *Service) EvolveSkillJob(job *domain.LearningJob) (string, error) {
	convID, err, _ := s.evolveSkillJobAt(job, nil)
	return convID, err
}

func (s *Service) evolveSkillJobAt(job *domain.LearningJob, source *LearningSource) (string, error, bool) {
	if s.deps.Skills == nil || s.deps.Experiences == nil {
		return "", fmt.Errorf("skill store not configured"), false
	}
	exp, err := s.deps.Experiences.Get(job.ExperienceID)
	if err != nil {
		return "", err, false
	}
	if len(exp.Actions) < 3 {
		return "", nil, false
	}
	sourceValue := s.resolveLearningSource(exp, source)
	name := LearnedSkillName(exp.Goal)

	// Try the LLM-backed skill evolver first (RFC section 20-21). When a
	// learning model is available, the evolver receives a short source
	// handoff and returns a skill proposal with the full RFC schema
	// (purpose, trigger, preconditions, steps, verification, recovery,
	// anti-patterns). If the LLM path fails or no provider is configured,
	// fall back to a deterministic template that still meets the minimum
	// bar (purpose, trigger, steps) by structuring the experience data.
	body, description, convID, sourceReviewed := s.evolvedSkillDraft(exp, sourceValue)
	if !SkillMeetsMinimumBar(body) {
		s.log("debug", "learning", "skill body does not meet minimum bar, skipping")
		return convID, nil, sourceReviewed
	}

	skill := NewLearnedSkill(name, description, body)
	if !s.ApplyLearnedSkillRevision(skill, name) {
		return convID, nil, sourceReviewed
	}
	skill.EnsureStatusDefault()
	if domain.CreatorMayPromote(domain.ActorSkillEvolver) {
		return convID, fmt.Errorf("evolver must not promote"), false
	}
	if err := s.deps.Skills.Save(skill); err != nil {
		return convID, err, false
	}
	s.EmitSkillLifecycle("evolve", skill.ID, string(skill.Status), convID)
	// Evaluate in the same job goroutine. Nested goSafe races the skill
	// store with the still-finishing turn.
	evalErr := s.EvaluateSkillJob(&domain.LearningJob{
		Kind:    domain.LearningJobEvaluate,
		SkillID: skill.ID,
		Reason:  "post_evolve",
	})
	if evalErr != nil {
		return convID, evalErr, false
	}
	return convID, nil, sourceReviewed
}

func LearnedSkillName(goal string) string {
	name := strings.TrimSpace(goal)
	// Strip any framework/status prefixes the model may have included
	// (or repeated across evolutions): "learned-", "skill-", "workflow-".
	// The "learned-" prefix is a status marker applied exactly once here.
	lower := strings.ToLower(name)
	for strings.HasPrefix(lower, "learned-") || strings.HasPrefix(lower, "skill-") || strings.HasPrefix(lower, "workflow-") {
		if i := strings.Index(lower, "-"); i >= 0 {
			name = strings.TrimPrefix(name, name[:i+1])
			lower = strings.ToLower(name)
			continue
		}
		break
	}
	if strings.TrimSpace(name) == "" {
		return "learned-workflow"
	}
	slug := domain.SkillSlug(name)
	if slug == "skill" {
		// SkillSlug's empty-result default would produce the meaningless
		// "learned-skill" for names without ASCII letters.
		return "learned-workflow"
	}
	result := "learned-" + slug
	if len(result) > 48 {
		result = result[:48]
	}
	return result
}

// canonicalSkillTopicThreshold is the minimum name-overlap at which two
// learned skills are considered the same topic. Above it, evolution updates
// the existing (canonical) skill instead of spawning a near-duplicate
// folder like learned-tool-mapping vs learned-tool-mapping-workflow.
const canonicalSkillTopicThreshold = 0.6

// findCanonicalLearnedSkill returns the existing learned skill that the
// proposed skill revises, when one exists. Exact ids win; otherwise the
// closest topic match (token overlap of the slugified names) above the
// threshold is adopted, so repeated evolutions converge on one skill.
func FindCanonicalLearnedSkill(store SkillCatalog, name string) *domain.Skill {
	if store == nil {
		return nil
	}
	proposed := strings.ToLower(strings.TrimSpace(name))
	proposed = strings.TrimPrefix(proposed, "learned-")
	for _, s := range store.List() {
		if s == nil || s.Origin != domain.SkillOriginLearned {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(s.ID))
		if id == proposed || id == strings.ToLower(strings.TrimSpace(name)) {
			return s
		}
		if domain.MemorySimilarity(proposed, strings.TrimPrefix(id, "learned-")) >= canonicalSkillTopicThreshold {
			return s
		}
	}
	return nil
}

func (s *Service) evolvedSkillDraft(exp *domain.Experience, source LearningSource) (string, string, string, bool) {
	body, description, convID, sourceReviewed := s.evolveSkillViaLLMAt(exp, source)
	if body != "" {
		return body, description, convID, sourceReviewed
	}
	body, description = s.DeterministicSkillBody(exp)
	return body, description, convID, sourceReviewed
}

func NewLearnedSkill(name, description, body string) *domain.Skill {
	return &domain.Skill{
		ID:            name,
		Name:          name,
		Description:   description,
		Content:       body,
		Origin:        domain.SkillOriginLearned,
		Status:        domain.SkillStatusExperimental,
		Version:       1,
		ActiveVersion: 1,
		OwnedBy:       string(domain.SkillOriginLearned),
	}
}

func (s *Service) ApplyLearnedSkillRevision(skill *domain.Skill, name string) bool {
	existing, err := s.deps.Skills.Get(name, string(domain.SkillOriginLearned))
	if err != nil || existing == nil {
		// No skill under the exact proposed id: adopt the closest existing
		// learned skill on the same topic (if any) so evolution refines one
		// canonical skill instead of piling up near-duplicate folders.
		existing = FindCanonicalLearnedSkill(s.deps.Skills, name)
	}
	if existing == nil {
		return true
	}
	if existing.Version >= domain.MaxSkillRevisions {
		return false
	}
	skill.ID = existing.ID
	skill.Name = existing.Name
	skill.Status = domain.SkillStatusExperimental
	return true
}

// evolveSkillViaLLM calls the LLM-backed skill evolver (RFC section 20-21)
// and returns (body, description, conversationID). The conversation id comes
// back even when the proposal is unusable, so the caller can still surface
// the transcript that explains the empty result.
func (s *Service) evolveSkillViaLLM(exp *domain.Experience) (string, string, string) {
	body, description, convID, _ := s.evolveSkillViaLLMAt(exp, s.LearningSourceForExperience(exp))
	return body, description, convID
}

func (s *Service) evolveSkillViaLLMAt(exp *domain.Experience, source LearningSource) (string, string, string, bool) {
	if strings.TrimSpace(resources.LearnerPrompt()) == "" {
		return "", "", "", false
	}
	prompt := s.BuildLearnerPacketAt(exp, source, domain.TriggerRepeatedProcedure, 3)
	text, convID, err := s.doLearningTurn(s.workspaceCtx(exp), s.learningModelID(), prompt)
	if err != nil {
		s.log("debug", "learning", "skill evolver LLM call failed, using deterministic fallback: %v", err)
		return "", "", convID, false
	}
	prop := ParseLLMSkillProposal(text)
	if prop == nil {
		s.log("debug", "learning", "skill evolver LLM returned no valid proposal")
		return "", "", convID, false
	}
	var b strings.Builder
	b.WriteString("# " + strings.TrimSpace(prop.Name) + "\n\n")
	if prop.Purpose != "" {
		b.WriteString("## Purpose\n")
		b.WriteString(strings.TrimSpace(prop.Purpose))
		b.WriteString("\n\n")
	}
	if prop.Trigger != "" {
		b.WriteString("## Trigger\n")
		b.WriteString(strings.TrimSpace(prop.Trigger))
		b.WriteString("\n\n")
	}
	if prop.Preconditions != "" {
		b.WriteString("## Preconditions\n")
		b.WriteString(strings.TrimSpace(prop.Preconditions))
		b.WriteString("\n\n")
	}
	b.WriteString("## Steps\n")
	b.WriteString(strings.TrimSpace(prop.Steps))
	b.WriteString("\n\n")
	if prop.Verification != "" {
		b.WriteString("## Verification\n")
		b.WriteString(strings.TrimSpace(prop.Verification))
		b.WriteString("\n\n")
	}
	if prop.Recovery != "" {
		b.WriteString("## Recovery\n")
		b.WriteString(strings.TrimSpace(prop.Recovery))
		b.WriteString("\n\n")
	}
	if prop.AntiPatterns != "" {
		b.WriteString("## Anti-patterns\n")
		b.WriteString(strings.TrimSpace(prop.AntiPatterns))
		b.WriteString("\n\n")
	}
	desc := prop.Description
	if desc == "" {
		desc = firstLearnedSentence(prop.Purpose)
	}
	if desc == "" {
		desc = "Learned workflow: " + strings.TrimPrefix(strings.TrimSpace(prop.Name), "learned-")
	}
	s.log("info", "learning", "skill evolver LLM returned proposal: kind=%s name=%s", prop.Kind, prop.Name)
	return b.String(), desc, convID, true
}

// firstLearnedSentence returns the first sentence of a body as the skill
// description seed, without trailing punctuation noise. It never falls back
// to user goal text; the skill name carries the topic.
func firstLearnedSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if i := strings.Index(text, "."); i > 0 && i < 220 {
		text = text[:i+1]
	}
	return clip(text, 200)
}

// deterministicSkillBody builds a skill body from the experience data that
// meets the minimum RFC bar (purpose, trigger, steps). Used when the LLM
// evolver is unavailable. The body is a template, not a free-form paragraph.
func (s *Service) DeterministicSkillBody(exp *domain.Experience) (string, string) {
	var b strings.Builder
	b.WriteString("# Learned Workflow\n\n")
	b.WriteString("## Purpose\n")
	b.WriteString("Repeat the verified workflow for: ")
	b.WriteString(clip(exp.Goal, 180))
	b.WriteString(".\n\n")

	b.WriteString("## Trigger\n")
	b.WriteString("When the same goal recurs and this procedure matches the task context.\n\n")

	if len(exp.Corrections) > 0 {
		b.WriteString("## Preconditions\n")
		for _, c := range exp.Corrections {
			if c.Desired != "" {
				fmt.Fprintf(&b, "- %s\n", c.Desired)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("## Steps\n")
	for i, act := range exp.Actions {
		fmt.Fprintf(&b, "%d. `%s`", i+1, act.Name)
		if act.Digest != "" {
			fmt.Fprintf(&b, " — %s", act.Digest)
		}
		b.WriteByte('\n')
	}
	b.WriteString("\n")

	b.WriteString("## Verification\n")
	if exp.Outcome.Status == "success" && len(exp.Outcome.Verification) > 0 {
		for _, v := range exp.Outcome.Verification {
			fmt.Fprintf(&b, "- %s\n", v)
		}
	} else {
		b.WriteString("Confirm the task goal is achieved without errors.\n")
	}

	// The deterministic path has no LLM-authored description; never paste
	// the raw goal sentence into the description field.
	return b.String(), "Learned workflow extracted from a NusaShell session"
}

func (s *Service) EvaluateSkillJob(job *domain.LearningJob) error {
	if s.deps.Skills == nil || job.SkillID == "" {
		return nil
	}
	if domain.CreatorMayPromote(domain.ActorSkillEval) || domain.CreatorMayPromote(domain.ActorSkillEvolver) {
		return fmt.Errorf("evaluator must not be the creator")
	}
	skill, err := s.deps.Skills.Get(job.SkillID, string(domain.SkillOriginLearned))
	if err != nil {
		return err
	}
	if skill.Status == domain.SkillStatusTrusted {
		return nil
	}
	if skill.Version > domain.MaxSkillRevisions {
		skill.Status = domain.SkillStatusDeprecated
		skill.Touch(clock.NewTime().Time())
		return s.deps.Skills.Save(skill)
	}
	// No deterministic verifier in v1: stay experimental. Trusted is human-only.
	return nil
}

// emitSkillLifecycle announces a skill change and records it in the learning
// trajectory. conversationID links the event to the persisted LLM transcript
// that produced it, so the Learning log can open that exact run.
func (s *Service) EmitSkillLifecycle(op, id, status, conversationID string) {
	if s == nil {
		return
	}
	if s.deps.Bus != nil {
		s.deps.Bus.Emit(contracts.EventSkillUpdated, map[string]any{
			"id":     id,
			"status": status,
			"op":     op,
		})
	}
	if s.deps.Trajectory != nil {
		detail := map[string]interface{}{
			"id":     id,
			"status": status,
		}
		if conversationID != "" {
			detail["llm_conversation_id"] = conversationID
		}
		s.deps.Trajectory.Record("skill_"+op, detail)
	}
	if s.deps.OnSkillChanged != nil {
		s.deps.OnSkillChanged(op, id, status, conversationID)
	}
}

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}

func PayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func memoryOpTargetID(op *domain.LearningOperation) string {
	if op == nil {
		return ""
	}
	if id := PayloadString(op.Payload, "id"); id != "" {
		return id
	}
	return strings.TrimSpace(op.TargetID)
}
