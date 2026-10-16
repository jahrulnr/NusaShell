package agent

// ConversationRules is the exported name for the conversation agent rule set
// so root tests can hold a pointer returned by ConversationRulesForTest.
type ConversationRules = conversationRules

const (
	PromptCacheConversationPrefix = promptCacheConversationPrefix
	PromptCacheBackgroundPrefix   = promptCacheBackgroundPrefix
	MaxToolErrorLen               = maxToolErrorLen
	RoundStreamFrameCap           = roundStreamFrameCap
	RoundStreamSubBuf             = roundStreamSubBuf
)
