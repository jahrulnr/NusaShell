package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"nusashell/contracts"
	"nusashell/domain"
)

func (a *Service) loadRepo(id string) (*ConversationRepository, error) {
	if a == nil || a.Conversations == nil {
		return nil, fmt.Errorf("conversation store is required")
	}
	c, err := a.Conversations.Get(id)
	if err != nil {
		return nil, err
	}
	return bindConversation(a.Conversations, c), nil
}

func (a *Service) loadRepoRPC(id string) (*ConversationRepository, *contracts.RPCError) {
	c, rpcErr := a.getConversation(id)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return bindConversation(a.Conversations, c), nil
}

func (a *Service) getConversation(id string) (*domain.Conversation, *contracts.RPCError) {
	if strings.TrimSpace(id) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "conversation id is required"}
	}
	if a == nil || a.Conversations == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "conversation store is required"}
	}
	c, err := a.Conversations.Get(id)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return c, nil
}

func (a *Service) chatMessagesForProvider(c *domain.Conversation, pendingMsgID string, caps ModelCapabilities) []ChatMessage {
	if a != nil && a.deps.ChatMessages != nil {
		return a.deps.ChatMessages(c, pendingMsgID, caps)
	}
	return chatMessages(c, pendingMsgID, caps)
}

func (a *Service) enrichWithVisionDescriptions(ctx context.Context, conversation *domain.Conversation, pendingMsgID string, settings domain.Settings) *domain.Conversation {
	if a != nil && a.deps.EnrichVision != nil {
		return a.deps.EnrichVision(ctx, conversation, pendingMsgID, settings)
	}
	return conversation
}

func (a *Service) enrichWithAudioDescriptions(ctx context.Context, conversation *domain.Conversation, pendingMsgID string, settings domain.Settings) *domain.Conversation {
	if a != nil && a.deps.EnrichAudio != nil {
		return a.deps.EnrichAudio(ctx, conversation, pendingMsgID, settings)
	}
	return conversation
}

func (a *Service) enrichWithVideoDescriptions(ctx context.Context, conversation *domain.Conversation, pendingMsgID string, settings domain.Settings) *domain.Conversation {
	if a != nil && a.deps.EnrichVideo != nil {
		return a.deps.EnrichVideo(ctx, conversation, pendingMsgID, settings)
	}
	return conversation
}

func (a *Service) executeReadImage(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
	if a != nil && a.deps.ExecuteReadImage != nil {
		return a.deps.ExecuteReadImage(run, toolCall, caps, settings)
	}
	return "", nil, fmt.Errorf("read_image is not configured")
}

func (a *Service) executeReadAudio(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
	if a != nil && a.deps.ExecuteReadAudio != nil {
		return a.deps.ExecuteReadAudio(run, toolCall, caps, settings)
	}
	return "", nil, fmt.Errorf("read_audio is not configured")
}

func (a *Service) executeReadVideo(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
	if a != nil && a.deps.ExecuteReadVideo != nil {
		return a.deps.ExecuteReadVideo(run, toolCall, caps, settings)
	}
	return "", nil, fmt.Errorf("read_video is not configured")
}

func (a *Service) executeReadDocument(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
	if a != nil && a.deps.ExecuteReadDocument != nil {
		return a.deps.ExecuteReadDocument(run, toolCall, caps, settings)
	}
	return "", nil, fmt.Errorf("read_document is not configured")
}

func (a *Service) executeGenerateMedia(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	if a != nil && a.deps.ExecuteGenerateMedia != nil {
		return a.deps.ExecuteGenerateMedia(run, toolCall, settings)
	}
	return "", nil, fmt.Errorf("generate_media is not configured")
}

func (a *Service) learnerSkillCreatorReference() (path, content string) {
	if a != nil && a.deps.SkillCreatorRef != nil {
		return a.deps.SkillCreatorRef()
	}
	return "", ""
}

func (a *Service) acknowledgeLearnerResult(args string) (string, error) {
	if a != nil && a.deps.AcknowledgeLearner != nil {
		return a.deps.AcknowledgeLearner(args)
	}
	return "", fmt.Errorf("learner result not configured")
}

func learningNodeIDsFromTool(a *Service, toolCall domain.ToolCall, output string) []string {
	if a == nil || a.deps.LearningNodeIDs == nil {
		return nil
	}
	return a.deps.LearningNodeIDs(toolCall, output)
}

func (a *Service) RecordLearningTurnNodes(run *TurnRun, ids []string) {
	if a == nil || run == nil {
		return
	}
	ids = uniqueLearningIDs(ids)
	if len(ids) == 0 {
		return
	}
	run.learningNodesMu.Lock()
	if run.learningNodes == nil {
		run.learningNodes = make(map[string]struct{})
	}
	newIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := run.learningNodes[id]; ok {
			continue
		}
		run.learningNodes[id] = struct{}{}
		newIDs = append(newIDs, id)
	}
	allIDs := make([]string, 0, len(run.learningNodes))
	for id := range run.learningNodes {
		allIDs = append(allIDs, id)
	}
	run.learningNodesMu.Unlock()
	if a.deps.RecordTurnPairs != nil {
		a.deps.RecordTurnPairs(allIDs, newIDs)
	}
}

func uniqueLearningIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (a *Service) DecorateRateLimitError(providerID string, err error) error {
	if a != nil && a.deps.DecorateRateLimit != nil {
		return a.deps.DecorateRateLimit(providerID, err)
	}
	return err
}

func (a *Service) recordExperience(conv *domain.Conversation, headless bool) {
	if a != nil && a.deps.RecordExperience != nil {
		a.deps.RecordExperience(conv, headless)
	}
}

func (a *Service) maybeAnnounceTaskMemory(conversationID string, conversation *domain.Conversation) {
	if a != nil && a.deps.MaybeAnnounceTaskMemory != nil {
		a.deps.MaybeAnnounceTaskMemory(conversationID, conversation)
	}
}

func (a *Service) ConversationRulesForTest(run *TurnRun, adapter ProviderContext, conv *domain.Conversation, settings domain.Settings, provider *domain.Provider, model, currentMsgID string, round int) *conversationRules {
	p := a.NewConversationRules(run, adapter, conv, settings, provider, model, "", currentMsgID, ModelCapabilities{}, nil, 0, nil, false)
	p.currentMsgID = currentMsgID
	p.round = round
	return p
}

func (a *Service) delegateRunSnapshot(runID string) (*domain.AcpRun, bool) {
	if a != nil && a.deps.DelegateSnapshot != nil {
		return a.deps.DelegateSnapshot(runID)
	}
	return nil, false
}

func (a *Service) ConversationTurnLock(conversationID string) *sync.Mutex {
	a.conversationTurnsMu.Lock()
	defer a.conversationTurnsMu.Unlock()
	if a.conversationTurns == nil {
		a.conversationTurns = map[string]*sync.Mutex{}
	}
	lock, ok := a.conversationTurns[conversationID]
	if !ok {
		lock = &sync.Mutex{}
		a.conversationTurns[conversationID] = lock
	}
	return lock
}

func (a *Service) TrackPendingRun(conversationID, runID, tool string) {
	if conversationID == "" || runID == "" {
		return
	}
	a.pendingRunsMu.Lock()
	defer a.pendingRunsMu.Unlock()
	if a.pendingRuns == nil {
		a.pendingRuns = map[string]map[string]string{}
	}
	if a.pendingRuns[conversationID] == nil {
		a.pendingRuns[conversationID] = map[string]string{}
	}
	a.pendingRuns[conversationID][runID] = tool
}

func (a *Service) UntrackPendingRun(conversationID, runID string) bool {
	a.pendingRunsMu.Lock()
	defer a.pendingRunsMu.Unlock()
	set := a.pendingRuns[conversationID]
	if set == nil {
		return false
	}
	if _, ok := set[runID]; !ok {
		return false
	}
	delete(set, runID)
	if len(set) == 0 {
		delete(a.pendingRuns, conversationID)
	}
	return true
}

func (a *Service) HasPendingRuns(conversationID string) bool {
	a.pendingRunsMu.Lock()
	defer a.pendingRunsMu.Unlock()
	return len(a.pendingRuns[conversationID]) > 0
}

func (a *Service) PendingBackgroundRuns(conversationID string) []domain.BackgroundRunInfo {
	a.pendingRunsMu.Lock()
	set := a.pendingRuns[conversationID]
	ids := make([]string, 0, len(set))
	for runID := range set {
		ids = append(ids, runID)
	}
	a.pendingRunsMu.Unlock()
	sort.Strings(ids)

	out := make([]domain.BackgroundRunInfo, 0, len(ids))
	for _, runID := range ids {
		a.pendingRunsMu.Lock()
		tool := set[runID]
		a.pendingRunsMu.Unlock()
		info := domain.BackgroundRunInfo{
			ID:     runID,
			Tool:   tool,
			Status: "pending",
		}
		if dr, ok := a.delegateRunSnapshot(runID); ok && dr != nil {
			info.Agent = dr.AgentName
			info.Model = dr.CurrentModelID
			info.Workspace = dr.Workspace
		}
		out = append(out, info)
	}
	return out
}
