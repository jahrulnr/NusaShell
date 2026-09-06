// Package agent owns the agent turn engine and round loop: turn
// lifecycle, streaming, hydration checkpoints, compaction, prompts,
// announcements, capability registry, and the ask-question pause. It is the
// only subsystem (besides the application root wiring) that may orchestrate
// across features. Cross-feature flow goes through narrow Deps callbacks,
// Bus events, and exported feature entry points (conversation.Bind /
// NewConversation, provider chat types, tools.ToolFactory / AgentKind).
// It must never import nusashell/application or infrastructure/ai/core;
// provider/ is the single sanctioned core import.
package agent
