package agent

import (
	"nusashell/domain"
)

type RepeatedToolGuard = repeatedToolGuard

func ToolRoundSignature(calls []domain.ToolCall) string {
	return toolRoundSignature(calls)
}

func CompactionPassAvailable(contextWindow int, runningSummary string, summaryMaxOut int) int {
	return compactionPassAvailable(contextWindow, runningSummary, summaryMaxOut)
}

func ExtractCompactionSummary(resp ChatResponse) string {
	return extractCompactionSummary(resp)
}

func AppendCompactionHandoffUser(msgs []ChatMessage) []ChatMessage {
	return appendCompactionHandoffUser(msgs)
}

func CompactionToolChoice(kind domain.ProviderKind) any {
	return compactionToolChoice(kind)
}

func CompactionSummaryEchoesAssistant(summary string, msgs []ChatMessage) bool {
	return compactionSummaryEchoesAssistant(summary, msgs)
}

func ReasoningDeltaVisible(accumulated string) bool {
	return reasoningDeltaVisible(accumulated)
}

func FilterToolAttachmentsByCaps(atts []domain.Attachment, content string, caps ModelCapabilities) ([]domain.Attachment, string) {
	return filterToolAttachmentsByCaps(atts, content, caps)
}

func MergeUsage(a, b ChatUsage) ChatUsage {
	return mergeUsage(a, b)
}

func FinalHeadlessAssistantMessage(messages []domain.Message, finalMessageID string) (domain.Message, bool) {
	return finalHeadlessAssistantMessage(messages, finalMessageID)
}

func HeadlessWorkspace(ctxWorkspace string, kind AgentKind, fallbackDataDir string) string {
	return headlessWorkspace(ctxWorkspace, kind, fallbackDataDir)
}

func HeadlessConversationType(kind AgentKind) domain.ConversationType {
	return headlessConversationType(kind)
}

func HeadlessTurnTitle(kind AgentKind, prompt string) string {
	return headlessTurnTitle(kind, prompt)
}

func ServerCompactionContextManagement(model string) []map[string]any {
	return serverCompactionContextManagement(model)
}

func (a *Service) RestartAnnouncement() domain.Message {
	return a.restartAnnouncement()
}

func (a *Service) AutoContinueAnnouncement(decision domain.AutoContinueDecision) domain.Message {
	return a.autoContinueAnnouncement(decision)
}

func (a *Service) SubagentResultMessage(run *domain.AcpRun, outputPath string, status domain.ToolCallStatus) domain.Message {
	return a.subagentResultMessage(run, outputPath, status)
}

const (
	CompactionSummaryMaxOut     = compactionSummaryMaxOut
	CompactionSummaryMinChars   = compactionSummaryMinChars
	CompactionSummaryMaxRetries = compactionSummaryMaxRetries
	CompactionSystemReserve     = compactionSystemReserve
	MaxPendingAnnouncements     = maxPendingAnnouncements
	HydrationFileListMaxBytes   = hydrationFileListMaxBytes
)
