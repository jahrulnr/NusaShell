// Shared primitives
export type { DomainEvent } from "./shared/domain-event.js";
export {
  DomainError,
  type DomainErrorCode,
} from "./shared/domain-error.js";
export { Entity } from "./shared/entity.js";
export {
  err,
  isErr,
  isOk,
  ok,
  type Result,
} from "./shared/result.js";

// Plugin value objects
export { PluginId, type PluginId as PluginIdType } from "./plugin/value-objects/plugin-id.js";
export {
  PluginVersion,
  type PluginVersion as PluginVersionType,
} from "./plugin/value-objects/plugin-version.js";
export {
  PLUGIN_RUNTIME_STATES,
  type PluginRuntimeState,
} from "./plugin/value-objects/runtime-state.js";
export {
  TRANSPORT_TYPES,
  type TransportType,
} from "./plugin/value-objects/transport-type.js";

// Plugin entities
export {
  PluginManifest,
  type PluginManifestInput,
  type WindowMode,
} from "./plugin/entities/plugin-manifest.js";
export { Plugin, type CreatePluginInput } from "./plugin/entities/plugin.js";
export { PluginRuntime } from "./plugin/entities/plugin-runtime.js";

// Plugin policies
export { RuntimeTransitionPolicy } from "./plugin/services/runtime-transition-policy.js";
export { PluginLifecyclePolicy } from "./plugin/services/plugin-lifecycle-policy.js";

// Plugin events
export { PluginInstalledEvent } from "./plugin/events/plugin-installed.event.js";
export { PluginStartedEvent } from "./plugin/events/plugin-started.event.js";
export { PluginStoppedEvent } from "./plugin/events/plugin-stopped.event.js";
export { PluginCrashedEvent } from "./plugin/events/plugin-crashed.event.js";
export { PluginStateChangedEvent } from "./plugin/events/plugin-state-changed.event.js";
export { ToolCallCompletedEvent } from "./plugin/events/tool-call-completed.event.js";

// Plugin errors
export { InvalidRuntimeTransitionError } from "./plugin/errors/invalid-runtime-transition.error.js";
export { PluginDisabledError } from "./plugin/errors/plugin-disabled.error.js";
export { PluginNotFoundError } from "./plugin/errors/plugin-not-found.error.js";

// Tool value objects
export { ToolName, type ToolName as ToolNameType } from "./tool/value-objects/tool-name.js";
export { RequestId, type RequestId as RequestIdType } from "./tool/value-objects/request-id.js";

// Tool entities
export {
  ToolCall,
  type CreateToolCallInput,
  type ToolCallStatus,
} from "./tool/entities/tool-call.js";

// Tool errors
export { ToolNotFoundError } from "./tool/errors/tool-not-found.error.js";
export { ToolCallTimeoutError } from "./tool/errors/tool-call-timeout.error.js";
