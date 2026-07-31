// Errors
export {
  ApplicationError,
  type ApplicationErrorCode,
  ConflictError,
  OperationTimeoutError,
} from "./errors/index.js";

// Events
export {
  EventDispatcher,
  createAgentTextDeltaEvent,
  createAgentReasoningDeltaEvent,
  createAgentToolCallStartEvent,
  createAgentToolCallEndEvent,
  createAgentContextUpdateEvent,
  createLearningUpdatedEvent,
  type ApplicationEvent,
  type AgentTextDeltaEvent,
  type AgentReasoningDeltaEvent,
  type AgentToolCallStartEvent,
  type AgentToolCallEndEvent,
  type AgentContextUpdateEvent,
  type AgentLearningUpdatedEvent,
  type EventHandler,
  type EventHandlerFn,
} from "./events/index.js";

// Messaging
export {
  CommandBus,
  QueryBus,
  type Command,
  type CommandResult,
  type CommandHandler,
  type Query,
  type QueryResult,
  type QueryHandler,
} from "./messaging/index.js";

// Plugin use cases + ports + services
export type {
  PluginRepositoryPort,
  PluginInstallerPort,
  PluginProcessPort,
  ProcessHandle,
  McpClientPort,
  McpClientFactoryPort,
  ToolDescriptor,
  PromptArgumentDescriptor,
  PromptDescriptor,
  PromptResult,
  ResourceDescriptor,
  ResourceReadResult,
  ResourceTemplateDescriptor,
  CompletionReference,
  CompletionResult,
  ClockPort,
  LoggerPort,
} from "./plugin/index.js";
export {
  PluginOperationQueue,
  PluginRuntimeManager,
  type PluginRuntimeManagerDeps,
  type PluginView,
  type StartPluginOptions,
  type CallToolOptions,
  StartPluginHandler,
  StopPluginHandler,
  RestartPluginHandler,
  InstallPluginHandler,
  UninstallPluginHandler,
  SetPluginAutostartHandler,
  ListPluginsHandler,
  GetPluginHandler,
  GetPluginStateHandler,
  type StartPluginCommand,
  type StartPluginResult,
  type StopPluginCommand,
  type StopPluginResult,
  type RestartPluginCommand,
  type RestartPluginResult,
  type InstallPluginCommand,
  type InstallPluginResult,
  type UninstallPluginCommand,
  type UninstallPluginResult,
  type SetPluginAutostartCommand,
  type ListPluginsQuery,
  type ListPluginsResult,
  type PluginListItem,
  type GetPluginQuery,
  type GetPluginResult,
  type GetPluginStateQuery,
  type GetPluginStateResult,
} from "./plugin/index.js";

// Tool use cases
export type { CallToolCommand, CallToolResult, CancelToolCallCommand, CancelToolCallResult, ListToolsQuery, ListToolsResult, ToolItem } from "./tool/index.js";
export { CallToolHandler, CancelToolCallHandler, ListToolsHandler } from "./tool/index.js";

// Agent runtime
export type {
  AgentMessage,
  AgentContentPart,
  AgentModelCapabilities,
  AgentTokenUsage,
  AgentToolCall,
  AgentToolDefinition,
  AgentProviderRequest,
  AgentProviderResult,
  AgentProvider,
  AgentProviderRegistryPort,
  ReasoningEffort,
  AgentToolGateway,
  AgentPrompt,
  PromptLoaderPort,
  ReviewPromptKind,
  ReviewState,
  ReviewStateStorePort,
  DocsHit,
  DocSummary,
  DocContent,
  DocsIndexPort,
  AgentTurnRunnerDeps,
  RunAgentTurnInput,
  AgentTurnResult,
  AgentToolExecution,
  AgentTurnWorker,
  AgentProviderStrategy,
  RoutedAgentProviderOptions,
  PromptVars,
  RunAgentTurnCommand,
  CancelAgentTurnCommand,
  CancelAgentTurnResult,
} from "./agent/index.js";
export {
  AgentTurnRunner,
  McpAgentToolGateway,
  ReviewAgentToolGateway,
  BackgroundReviewScheduler,
  InProcessAgentTurnWorker,
  RoutedAgentProvider,
  AgentTurnCoordinator,
  RunAgentTurnHandler,
  CancelAgentTurnHandler,
  injectPrompts,
  applyVars,
  formatMemoryPrompt,
  DEFAULT_REVIEW_SETTINGS,
} from "./agent/index.js";
export type {
  WriteOrigin,
  SkillApprovalStagingPort,
  BackgroundReviewSettings,
  BackgroundReviewSchedulerDeps,
} from "./agent/index.js";

// Config
export { type AppConfig, loadConfig } from "./config/index.js";

// System queries
export type { SystemPingQuery, SystemPingResult, SystemVersionQuery, SystemVersionResult } from "./system/index.js";
export { SystemPingHandler, SystemVersionHandler } from "./system/index.js";

// MCP capability queries
export type { GetPromptQuery, ListPromptsQuery, ListResourcesQuery, ListResourceTemplatesQuery, ReadResourceQuery } from "./mcp/index.js";
export { GetPromptHandler, ListPromptsHandler, ListResourcesHandler, ListResourceTemplatesHandler, ReadResourceHandler } from "./mcp/index.js";

// Local Agent Skills
export type {
  SkillDetail,
  SkillFileEntry,
  SkillReadResult,
  SkillRegistryPort,
  SkillSummary,
  ArchivedSkillSummary,
  SkillOrigin,
  SkillProvenanceEntry,
  SkillProvenancePort,
  SkillState,
  UsageBumpKind,
  SkillUsageRecord,
  SkillUsagePort,
} from "./skill/index.js";
export { latestActivityAt } from "./skill/index.js";
export {
  SkillCuratorService,
  type CuratorSettings,
  type CuratorChange,
  type CuratorResult,
  type SkillCuratorDeps,
  DEFAULT_CURATOR_SETTINGS,
} from "./skill/index.js";
export {
  SkillCuratorScheduler,
  type CuratorSchedulerSettings,
  type SkillCuratorSchedulerDeps,
  DEFAULT_SCHEDULER_SETTINGS,
} from "./skill/index.js";

// Agent Memory
export type {
  MemoryTarget,
  MemoryEntry,
  MemoryUsage,
  MemorySnapshot,
  MemoryMutationResult,
  MemoryStorePort,
} from "./memory/index.js";
export {
  MEMORY_LIMIT,
  USER_LIMIT,
  ENTRY_DELIMITER,
  limitFor,
  splitEntries,
  joinEntries,
  charsOf,
  usageOf,
  checkCapacity,
  findUniqueMatch,
  MATCH_AMBIGUOUS,
  MATCH_NOT_FOUND,
  MATCH_EMPTY,
  addEntry,
  replaceEntry,
  removeEntry,
} from "./memory/index.js";

// Learning graph
export {
  LearningGraphService,
  type LearningGraphDeps,
  type LearningNode,
  type LearningEdge,
  type LearningCluster,
  type LearningGraphStats,
  type LearningGraph,
  type LearningNodeDetail,
  type MutationResult,
  parseMemoryNodeId,
  parseRelatedSkills,
} from "./learning/index.js";
