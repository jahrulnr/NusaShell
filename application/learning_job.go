package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
	"nusashell/resources"
)

func (a *App) runLearningJob(id string) {
	job := a.loadLearningJob(id)
	if job == nil {
		return
	}
	// Capture the source before dispatch. The prompt and cursor update must
	// refer to this same boundary even if the transcript grows while the
	// background turn is running.
	source := a.captureLearningSource(job)
	a.startLearningJob(job)
	convID, ops, runErr, sourceReviewed := a.executeLearningJob(job, source)
	a.finishLearningJob(job, runErr)
	_ = a.LearningJobs.Save(job)
	a.advanceLearningCursorAfterReview(source, runErr, sourceReviewed)
	a.recordLearningJobTrajectory(job, convID, ops)
	a.emitMemoryUpdated()
}

func (a *App) loadLearningJob(id string) *domain.LearningJob {
	if a == nil || a.LearningJobs == nil {
		return nil
	}
	job, err := a.LearningJobs.Get(id)
	if err != nil {
		return nil
	}
	return job
}

func (a *App) startLearningJob(job *domain.LearningJob) {
	now := clock.NewTime().Time()
	job.Status = domain.LearningJobRunning
	job.StartedAt = &now
	_ = a.LearningJobs.Save(job)
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventLearningJobStarted, contracts.LearningJobEvent{
			JobID:  job.ID,
			Kind:   job.Kind,
			Status: job.Status,
		})
	}
}

func (a *App) executeLearningJob(job *domain.LearningJob, source *learningSource) (string, []domain.LearningOperation, error, bool) {
	switch job.Kind {
	case domain.LearningJobLearner, domain.LearningJobConsolidate:
		ops, convID, err, reviewed := a.consolidateJobAt(job, source)
		return convID, ops, err, reviewed
	case domain.LearningJobEvolveSkill:
		convID, err, reviewed := a.evolveSkillJobAt(job, source)
		return convID, nil, err, reviewed
	case domain.LearningJobEvaluate:
		return "", nil, a.evaluateSkillJob(job), false
	case domain.LearningJobRetire:
		if a.lifecycle != nil {
			a.lifecycle.PruneOnce()
		}
		return "", nil, nil, false
	default:
		return "", nil, fmt.Errorf("unknown job kind %s", job.Kind), false
	}
}

func (a *App) finishLearningJob(job *domain.LearningJob, runErr error) {
	done := clock.NewTime().Time()
	job.FinishedAt = &done
	if runErr != nil {
		job.Status = domain.LearningJobError
		job.Error = runErr.Error()
		a.log("warn", "learning", "job error: id=%s kind=%s err=%v", job.ID, job.Kind, runErr)
		if a.Bus != nil {
			a.Bus.Emit(contracts.EventLearningJobError, contracts.LearningJobEvent{
				JobID:  job.ID,
				Kind:   job.Kind,
				Status: job.Status,
				Error:  "learning job failed",
			})
		}
		return
	}
	job.Status = domain.LearningJobDone
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventLearningJobDone, contracts.LearningJobEvent{
			JobID:  job.ID,
			Kind:   job.Kind,
			Status: job.Status,
		})
	}
}

func (a *App) advanceLearningCursorAfterReview(source *learningSource, runErr error, sourceReviewed bool) {
	if runErr != nil || !sourceReviewed {
		return
	}
	if err := a.advanceLearningCursor(source); err != nil {
		// The learning mutation already succeeded. Do not turn a marker
		// persistence failure into a duplicate-prone job retry.
		a.log("warn", "learning", "cursor persistence failed: conversation=%s boundary=%d err=%v", source.conversationID, source.messageEnd, err)
	}
}

func (a *App) captureLearningSource(job *domain.LearningJob) *learningSource {
	source := &learningSource{}
	if job == nil ||
		(job.Kind != domain.LearningJobLearner && job.Kind != domain.LearningJobConsolidate && job.Kind != domain.LearningJobEvolveSkill) ||
		a == nil || a.Experiences == nil || job.ExperienceID == "" {
		return source
	}
	exp, err := a.Experiences.Get(job.ExperienceID)
	if err != nil || exp == nil {
		return source
	}
	captured := a.learningSourceForExperience(exp)
	return &captured
}

func (a *App) resolveLearningSource(exp *domain.Experience, source *learningSource) learningSource {
	if source != nil {
		return *source
	}
	return a.learningSourceForExperience(exp)
}

func (a *App) advanceLearningCursor(source *learningSource) error {
	if a == nil || a.Conversations == nil || source == nil ||
		!source.boundaryCaptured || source.conversationID == "" {
		return nil
	}

	lock := a.conversationTurnLock(source.conversationID)
	lock.Lock()
	defer lock.Unlock()

	conversation, err := a.Conversations.Get(source.conversationID)
	if err != nil {
		return err
	}
	if conversation == nil {
		return fmt.Errorf("conversation %s is missing", source.conversationID)
	}

	currentStart, currentEnd := learningMessageRangeForConversation(conversation)
	capturedEnd := source.messageEnd
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

	repo := bindConversation(a.Conversations, conversation)
	repo.Conversation().LastReviewedMsgCount = target
	return repo.Save()
}

// recordLearningJobTrajectory appends a finished job's outcome to the
// learning trajectory so the Learning log feed shows it. Skill evolution
// already records its own lifecycle event (with the transcript id) inside
// evolveSkillJob, so only jobs that would otherwise be invisible in the feed
// are recorded here.
func (a *App) recordLearningJobTrajectory(job *domain.LearningJob, convID string, ops []domain.LearningOperation) {
	if a.Trajectory == nil || job == nil {
		return
	}
	switch job.Kind {
	case domain.LearningJobLearner, domain.LearningJobConsolidate:
		a.Trajectory.Record("consolidate", learningJobDetail(job, convID, ops))
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
		snippet := payloadString(op.Payload, "body")
		if snippet == "" {
			snippet = payloadString(op.Payload, "name")
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
func (a *App) consolidateJob(job *domain.LearningJob) ([]domain.LearningOperation, string, error) {
	ops, convID, err, _ := a.consolidateJobAt(job, nil)
	return ops, convID, err
}

func (a *App) consolidateJobAt(job *domain.LearningJob, source *learningSource) ([]domain.LearningOperation, string, error, bool) {
	if a.Experiences == nil || a.MemoryRecords == nil {
		return nil, "", fmt.Errorf("growth stores not configured"), false
	}
	exp, err := a.Experiences.Get(job.ExperienceID)
	if err != nil {
		return nil, "", err, false
	}
	sourceValue := a.resolveLearningSource(exp, source)
	svc := NewMemoryService(a.MemoryRecords, a.LearningOps)

	// Try the LLM-backed consolidator first (RFC section 14, 18-19). When a
	// learning model is available, the consolidator receives a short source
	// handoff and returns typed operations. If the LLM
	// path fails or no provider is configured, fall back to the
	// deterministic rule-based extraction (teachingOps) so the job still
	// produces output in offline/no-provider setups.
	ops, convID, sourceReviewed := a.consolidateViaLLMAt(job, exp, sourceValue)
	if !sourceReviewed {
		ops = consolidationOpsOrFallback(ops, exp, job.ID)
	}
	if len(ops) == 0 {
		return nil, convID, nil, sourceReviewed
	}
	return a.applyConsolidationOps(ops, convID, sourceReviewed, svc)
}

func consolidationOpsOrFallback(ops []domain.LearningOperation, exp *domain.Experience, jobID string) []domain.LearningOperation {
	if len(ops) > 0 {
		return ops
	}
	return teachingOps(exp, jobID)
}

func (a *App) applyConsolidationOps(ops []domain.LearningOperation, convID string, sourceReviewed bool, svc *MemoryService) ([]domain.LearningOperation, string, error, bool) {
	for i := range ops {
		a.prepareConsolidationOp(&ops[i])
		if err := svc.Apply(&ops[i]); err != nil {
			return ops[:i], convID, err, false
		}
	}
	return ops, convID, nil, sourceReviewed
}

func (a *App) prepareConsolidationOp(op *domain.LearningOperation) {
	if op.Kind != domain.OpMemoryUpsert {
		return
	}
	body := payloadString(op.Payload, "body")
	typ := payloadString(op.Payload, "type")
	project := payloadString(op.Payload, "project")
	id := matchingRecordID(a.MemoryRecords, body, typ, project)
	if id == "" {
		return
	}
	op.Kind = domain.OpMemoryStrengthen
	if op.Payload == nil {
		op.Payload = map[string]any{}
	}
	op.Payload["id"] = id
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
func (a *App) consolidateViaLLM(job *domain.LearningJob, exp *domain.Experience) ([]domain.LearningOperation, string) {
	ops, convID, _ := a.consolidateViaLLMAt(job, exp, a.learningSourceForExperience(exp))
	return ops, convID
}

func (a *App) consolidateViaLLMAt(job *domain.LearningJob, exp *domain.Experience, source learningSource) ([]domain.LearningOperation, string, bool) {
	if strings.TrimSpace(resources.LearnerPrompt()) == "" {
		return nil, "", false
	}
	prompt := a.buildLearnerPacketAt(exp, source, job.Reason, procedureCountForJob(a, exp, job))
	text, convID, err := a.doLearningTurn(WithWorkspace(context.Background(), exp.Scope.Workspace), AgentLearner, a.learningModelID(), prompt)
	if err != nil {
		a.log("debug", "learning", "learner LLM call failed, using deterministic fallback: %v", err)
		return nil, convID, false
	}
	if result := parseLearnerResult(text); result != nil {
		ops := opsFromLearnerConsolidate(result.Consolidate, job.ID, exp.ID)
		if job.Reason == domain.TriggerRepeatedProcedure && result.Evaluate != nil && result.Evaluate.Approved {
			a.applyLearnerEvolve(job, exp, result)
		}
		if len(ops) == 0 {
			a.log("debug", "learning", "learner LLM returned no operations")
			return nil, convID, true
		}
		a.log("info", "learning", "learner LLM returned %d operations", len(ops))
		return ops, convID, true
	}
	ops, parsed := parseLLMOperationsResult(text, job.ID, exp.ID)
	if parsed {
		if len(ops) == 0 {
			a.log("debug", "learning", "learner LLM returned no operations")
			return nil, convID, true
		}
		a.log("info", "learning", "learner LLM returned %d operations", len(ops))
		return ops, convID, true
	}
	a.log("debug", "learning", "learner LLM response could not be parsed")
	return nil, convID, false
}

func procedureCountForJob(a *App, exp *domain.Experience, job *domain.LearningJob) int {
	if a == nil || exp == nil || job == nil || job.Reason != domain.TriggerRepeatedProcedure {
		return 0
	}
	fp := strings.TrimSpace(exp.Signals.ProcedureFingerprint)
	if fp == "" || a.Experiences == nil {
		return 0
	}
	n := 1
	for _, h := range a.Experiences.ListByConversation(exp.ConversationID) {
		if h == nil || h.ID == exp.ID {
			continue
		}
		if h.Signals.ProcedureFingerprint == fp {
			n++
		}
	}
	return n
}

func parseLearnerResult(text string) *learnerResult {
	jsonText := extractJSONFromText(text)
	var result learnerResult
	if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		return nil
	}
	if strings.TrimSpace(result.StageReached) == "" && result.Consolidate == nil {
		return nil
	}
	return &result
}

func opsFromLearnerConsolidate(stage *learnerConsolidate, jobID, expID string) []domain.LearningOperation {
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
			ID:        domain.NewULID(domain.IDPrefixLearnOp),
			Kind:      domain.OpMemoryContradict,
			Status:    domain.LearningOpProposed,
			Actor:     domain.ActorLearner,
			JobID:     jobID,
			TargetID:  supersedes,
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

func (a *App) applyLearnerEvolve(job *domain.LearningJob, exp *domain.Experience, result *learnerResult) {
	if a == nil || a.Skills == nil || exp == nil || result == nil || result.Evaluate == nil || !result.Evaluate.Approved {
		return
	}
	name := learnedSkillName(exp.Goal)
	if result.Evolve != nil && strings.TrimSpace(result.Evolve.SkillID) != "" {
		name = learnedSkillName(result.Evolve.SkillID)
	} else if result.Evaluate.ProposedSkillShape != nil && strings.TrimSpace(result.Evaluate.ProposedSkillShape.Name) != "" {
		name = learnedSkillName(result.Evaluate.ProposedSkillShape.Name)
	}
	body, description := learnerSkillBody(result, exp)
	if !skillMeetsMinimumBar(body) {
		body, description = a.deterministicSkillBody(exp)
	}
	if !skillMeetsMinimumBar(body) {
		return
	}
	skill := newLearnedSkill(name, description, body)
	if !a.applyLearnedSkillRevision(skill, name) {
		return
	}
	skill.EnsureStatusDefault()
	if domain.CreatorMayPromote(domain.ActorLearner) {
		return
	}
	if err := a.Skills.Save(skill); err != nil {
		a.log("warn", "learning", "learner evolve save failed: %v", err)
		return
	}
	a.emitSkillLifecycle("evolve", skill.ID, string(skill.Status), "")
}

func learnerSkillBody(result *learnerResult, exp *domain.Experience) (string, string) {
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

func matchingRecordID(store MemoryRecordStore, body, typ, project string) string {
	if store == nil {
		return ""
	}
	want := domain.NormalizeMemoryContent(body)
	if want == "" {
		return ""
	}
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
			return rec.ID
		}
	}
	return ""
}

func teachingOps(exp *domain.Experience, jobID string) []domain.LearningOperation {
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
	if exp.Signals.ExplicitTeaching {
		add(exp.Goal, domain.MemoryTypePreference)
	}
	for _, c := range exp.Corrections {
		text := strings.TrimSpace(c.Desired)
		if text == "" {
			text = strings.TrimSpace(c.UserSaid)
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
func (a *App) evolveSkillJob(job *domain.LearningJob) (string, error) {
	convID, err, _ := a.evolveSkillJobAt(job, nil)
	return convID, err
}

func (a *App) evolveSkillJobAt(job *domain.LearningJob, source *learningSource) (string, error, bool) {
	if a.Skills == nil || a.Experiences == nil {
		return "", fmt.Errorf("skill store not configured"), false
	}
	exp, err := a.Experiences.Get(job.ExperienceID)
	if err != nil {
		return "", err, false
	}
	if len(exp.Actions) < 3 {
		return "", nil, false
	}
	sourceValue := a.resolveLearningSource(exp, source)
	name := learnedSkillName(exp.Goal)

	// Try the LLM-backed skill evolver first (RFC section 20-21). When a
	// learning model is available, the evolver receives a short source
	// handoff and returns a skill proposal with the full RFC schema
	// (purpose, trigger, preconditions, steps, verification, recovery,
	// anti-patterns). If the LLM path fails or no provider is configured,
	// fall back to a deterministic template that still meets the minimum
	// bar (purpose, trigger, steps) by structuring the experience data.
	body, description, convID, sourceReviewed := a.evolvedSkillDraft(exp, sourceValue)
	if !skillMeetsMinimumBar(body) {
		a.log("debug", "learning", "skill body does not meet minimum bar, skipping")
		return convID, nil, sourceReviewed
	}

	skill := newLearnedSkill(name, description, body)
	if !a.applyLearnedSkillRevision(skill, name) {
		return convID, nil, sourceReviewed
	}
	skill.EnsureStatusDefault()
	if domain.CreatorMayPromote(domain.ActorSkillEvolver) {
		return convID, fmt.Errorf("evolver must not promote"), false
	}
	if err := a.Skills.Save(skill); err != nil {
		return convID, err, false
	}
	a.emitSkillLifecycle("evolve", skill.ID, string(skill.Status), convID)
	// Evaluate in the same job goroutine. Nested goSafe races the skill
	// store with the still-finishing turn.
	evalErr := a.evaluateSkillJob(&domain.LearningJob{
		Kind:    domain.LearningJobEvaluate,
		SkillID: skill.ID,
		Reason:  "post_evolve",
	})
	if evalErr != nil {
		return convID, evalErr, false
	}
	return convID, nil, sourceReviewed
}

func learnedSkillName(goal string) string {
	name := "learned-" + domain.SkillSlug(strings.TrimSpace(goal))
	if name == "learned-" || len(name) < 12 {
		name = "learned-workflow"
	}
	if len(name) > 48 {
		return name[:48]
	}
	return name
}

func (a *App) evolvedSkillDraft(exp *domain.Experience, source learningSource) (string, string, string, bool) {
	body, description, convID, sourceReviewed := a.evolveSkillViaLLMAt(exp, source)
	if body != "" {
		return body, description, convID, sourceReviewed
	}
	body, description = a.deterministicSkillBody(exp)
	return body, description, convID, sourceReviewed
}

func newLearnedSkill(name, description, body string) *domain.Skill {
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

func (a *App) applyLearnedSkillRevision(skill *domain.Skill, name string) bool {
	existing, err := a.Skills.Get(name, string(domain.SkillOriginLearned))
	if err != nil || existing == nil {
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
func (a *App) evolveSkillViaLLM(exp *domain.Experience) (string, string, string) {
	body, description, convID, _ := a.evolveSkillViaLLMAt(exp, a.learningSourceForExperience(exp))
	return body, description, convID
}

func (a *App) evolveSkillViaLLMAt(exp *domain.Experience, source learningSource) (string, string, string, bool) {
	if strings.TrimSpace(resources.LearnerPrompt()) == "" {
		return "", "", "", false
	}
	prompt := a.buildLearnerPacketAt(exp, source, domain.TriggerRepeatedProcedure, 3)
	text, convID, err := a.doLearningTurn(WithWorkspace(context.Background(), exp.Scope.Workspace), AgentLearner, a.learningModelID(), prompt)
	if err != nil {
		a.log("debug", "learning", "skill evolver LLM call failed, using deterministic fallback: %v", err)
		return "", "", convID, false
	}
	prop := parseLLMSkillProposal(text)
	if prop == nil {
		a.log("debug", "learning", "skill evolver LLM returned no valid proposal")
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
		desc = clip(exp.Goal, 200)
	}
	a.log("info", "learning", "skill evolver LLM returned proposal: kind=%s name=%s", prop.Kind, prop.Name)
	return b.String(), desc, convID, true
}

// deterministicSkillBody builds a skill body from the experience data that
// meets the minimum RFC bar (purpose, trigger, steps). Used when the LLM
// evolver is unavailable. The body is a template, not a free-form paragraph.
func (a *App) deterministicSkillBody(exp *domain.Experience) (string, string) {
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

	return b.String(), clip(exp.Goal, 200)
}

func (a *App) evaluateSkillJob(job *domain.LearningJob) error {
	if a.Skills == nil || job.SkillID == "" {
		return nil
	}
	if domain.CreatorMayPromote(domain.ActorSkillEval) || domain.CreatorMayPromote(domain.ActorSkillEvolver) {
		return fmt.Errorf("evaluator must not be the creator")
	}
	s, err := a.Skills.Get(job.SkillID, string(domain.SkillOriginLearned))
	if err != nil {
		return err
	}
	if s.Status == domain.SkillStatusTrusted {
		return nil
	}
	if s.Version > domain.MaxSkillRevisions {
		s.Status = domain.SkillStatusDeprecated
		s.Touch(clock.NewTime().Time())
		return a.Skills.Save(s)
	}
	// No deterministic verifier in v1: stay experimental. Trusted is human-only.
	return nil
}

// emitSkillLifecycle announces a skill change and records it in the learning
// trajectory. conversationID links the event to the persisted LLM transcript
// that produced it, so the Learning log can open that exact run.
func (a *App) emitSkillLifecycle(op, id, status, conversationID string) {
	if a == nil {
		return
	}
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventSkillUpdated, map[string]any{
			"id":     id,
			"status": status,
			"op":     op,
		})
	}
	if a.Trajectory != nil {
		detail := map[string]interface{}{
			"id":     id,
			"status": status,
		}
		if conversationID != "" {
			detail["llm_conversation_id"] = conversationID
		}
		a.Trajectory.Record("skill_"+op, detail)
	}
}
