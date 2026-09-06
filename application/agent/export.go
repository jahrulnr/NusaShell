package agent

import (
	"time"

	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/contracts"
	"nusashell/domain"
)

// ConversationRules is the exported name for the conversation agent rule set
// so root tests can hold a pointer returned by ConversationRulesForTest.
type ConversationRules = conversationRules

func ChatMessages(c *domain.Conversation, pendingMsgID string, caps ModelCapabilities) []ChatMessage {
	return chatMessages(c, pendingMsgID, caps)
}

func IsTPMDominatedRequest(err error) bool { return isTPMDominatedRequest(err) }
func TPMContextCap(limit, maxOutput int) int {
	return tpmContextCap(limit, maxOutput)
}
func ExtractErrBody(err error) string { return extractErrBody(err) }
func IsLearnable400(err error) bool   { return isLearnable400(err) }

func BuildSystemPrompt(c *domain.Conversation, userPrompt string) string {
	return buildSystemPrompt(c, userPrompt)
}
func BuildSystemPromptForRun(run *TurnRun, c *domain.Conversation, userPrompt string) string {
	return buildSystemPromptForRun(run, c, userPrompt)
}

func NewAnnouncement(typ, args, message string) Announcement {
	return newAnnouncement(typ, args, message)
}

func NewSteerEntry(text string, attachments []domain.Attachment) *SteerEntry {
	return newSteerEntry(text, attachments)
}

func ShouldAnnounceRestart(c *domain.Conversation, startedAt time.Time) bool {
	return shouldAnnounceRestart(c, startedAt)
}

func ModelCapabilitiesWithLearned(provider *domain.Provider, model string, cache *learnedparams.Cache, manual *modeloverrides.Cache) ModelCapabilities {
	return modelCapabilitiesWithLearned(provider, model, cache, manual)
}

func ApplyStreamRound(message *domain.Message, model string, round StreamedTurnRound) {
	applyStreamRound(message, model, round)
}

func AskPendingEvent(conversationID, runID, callID string, req domain.AskQuestionRequest) contracts.AskPendingEvent {
	return askPendingEvent(conversationID, runID, callID, req)
}

func EstimateRequestTokens(system string, messages []ChatMessage, tools []ToolDef) int64 {
	return estimateRequestTokens(system, messages, tools)
}

func PromptCachePrefixForRun(run *TurnRun) string {
	return promptCachePrefixForRun(run)
}

func TruncateToolError(msg string) string {
	return truncateToolError(msg)
}

func HasUserMessage(messages []ChatMessage) bool {
	return hasUserMessage(messages)
}

func NeedsUserMessageAtEnd(messages []ChatMessage) bool {
	return needsUserMessageAtEnd(messages)
}

func AppendContinuationTool(messages []ChatMessage) []ChatMessage {
	return appendContinuationTool(messages)
}

func LastFailedAssistantIndex(msgs []domain.Message) int {
	return lastFailedAssistantIndex(msgs)
}

const (
	PromptCacheConversationPrefix = promptCacheConversationPrefix
	PromptCacheBackgroundPrefix   = promptCacheBackgroundPrefix
	MaxToolErrorLen               = maxToolErrorLen
	RoundStreamFrameCap           = roundStreamFrameCap
	RoundStreamSubBuf             = roundStreamSubBuf
)
