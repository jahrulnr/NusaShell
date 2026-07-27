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
  RestartPluginHandler,
  ListPluginsHandler,
  GetPluginHandler,
  GetPluginStateHandler,
  type StartPluginCommand,
  type StartPluginResult,
  type StopPluginCommand,
  type StopPluginResult,
  type RestartPluginCommand,
  type RestartPluginResult,
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

// Config
export { type AppConfig, loadConfig } from "./config/index.js";

// System queries
export type { SystemPingQuery, SystemPingResult, SystemVersionQuery, SystemVersionResult } from "./system/index.js";
export { SystemPingHandler, SystemVersionHandler } from "./system/index.js";
