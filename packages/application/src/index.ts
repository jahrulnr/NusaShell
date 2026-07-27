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
  type ApplicationEvent,
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
  PluginProcessPort,
  ProcessHandle,
  McpClientPort,
  McpClientFactoryPort,
  ToolDescriptor,
  ClockPort,
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
  ListPluginsHandler,
  type StartPluginCommand,
  type StartPluginResult,
  type StopPluginCommand,
  type StopPluginResult,
  type ListPluginsQuery,
  type ListPluginsResult,
  type PluginListItem,
} from "./plugin/index.js";

// Tool use cases
export type { CallToolCommand, CallToolResult } from "./tool/index.js";
export { CallToolHandler } from "./tool/index.js";
