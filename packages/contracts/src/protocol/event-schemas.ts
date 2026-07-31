import { z } from "zod";

const PluginStateSchema = z.enum([
  "idle",
  "starting",
  "running",
  "stopping",
  "crashed",
]);

export const PluginInstalledEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.installed"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    version: z.string(),
    timestamp: z.string(),
  }),
});

export const PluginUninstalledEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.uninstalled"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    timestamp: z.string(),
  }),
});

export const PluginStartedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.started"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    state: PluginStateSchema,
    pid: z.number().int(),
    timestamp: z.string(),
  }),
});

export const PluginStoppedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.stopped"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    state: PluginStateSchema,
    timestamp: z.string(),
  }),
});

export const PluginCrashedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.crashed"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    state: PluginStateSchema,
    exitCode: z.number().int(),
    timestamp: z.string(),
  }),
});

export const PluginStateChangedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.state_changed"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    oldState: PluginStateSchema,
    newState: PluginStateSchema,
    timestamp: z.string(),
  }),
});

export const ToolCallCompletedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("tool.call_completed"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    requestId: z.string(),
    toolName: z.string(),
    success: z.boolean(),
    timestamp: z.string(),
  }),
});

export const AgentTextDeltaEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.text_delta"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    delta: z.string(),
    timestamp: z.string(),
  }),
});

export const AgentReasoningDeltaEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.reasoning_delta"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    delta: z.string(),
    timestamp: z.string(),
  }),
});

export const AgentToolCallStartEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.tool_call_start"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    callId: z.string(),
    name: z.string(),
    args: z.record(z.string(), z.unknown()).optional(),
    timestamp: z.string(),
  }),
});

export const AgentToolCallEndEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.tool_call_end"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    callId: z.string(),
    name: z.string(),
    ok: z.boolean(),
    error: z.string().optional(),
    args: z.record(z.string(), z.unknown()).optional(),
    output: z.string().max(12_000).optional(),
    timestamp: z.string(),
  }),
});

export const AgentContextEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.context"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    estimatedTokens: z.number().int().nonnegative(),
    inputTokens: z.number().int().nonnegative().optional(),
    outputTokens: z.number().int().nonnegative().optional(),
    timestamp: z.string(),
  }),
});

export const AgentLearningUpdatedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.learning_updated"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    reviewTraceId: z.string().min(1),
    kinds: z.array(z.string()),
    summary: z.string(),
    timestamp: z.string(),
  }),
});

export const EventSchema = z.discriminatedUnion("event", [
  PluginInstalledEventSchema,
  PluginUninstalledEventSchema,
  PluginStartedEventSchema,
  PluginStoppedEventSchema,
  PluginCrashedEventSchema,
  PluginStateChangedEventSchema,
  ToolCallCompletedEventSchema,
  AgentTextDeltaEventSchema,
  AgentReasoningDeltaEventSchema,
  AgentToolCallStartEventSchema,
  AgentToolCallEndEventSchema,
  AgentContextEventSchema,
  AgentLearningUpdatedEventSchema,
]);

export type PluginInstalledEvent = z.infer<typeof PluginInstalledEventSchema>;
export type PluginUninstalledEvent = z.infer<typeof PluginUninstalledEventSchema>;
export type PluginStartedEvent = z.infer<typeof PluginStartedEventSchema>;
export type PluginStoppedEvent = z.infer<typeof PluginStoppedEventSchema>;
export type PluginCrashedEvent = z.infer<typeof PluginCrashedEventSchema>;
export type PluginStateChangedEvent = z.infer<typeof PluginStateChangedEventSchema>;
export type ToolCallCompletedEvent = z.infer<typeof ToolCallCompletedEventSchema>;
export type AgentTextDeltaEvent = z.infer<typeof AgentTextDeltaEventSchema>;
export type AgentReasoningDeltaEvent = z.infer<typeof AgentReasoningDeltaEventSchema>;
export type AgentToolCallStartEvent = z.infer<typeof AgentToolCallStartEventSchema>;
export type AgentToolCallEndEvent = z.infer<typeof AgentToolCallEndEventSchema>;
export type AgentContextEvent = z.infer<typeof AgentContextEventSchema>;
export type AgentLearningUpdatedEvent = z.infer<typeof AgentLearningUpdatedEventSchema>;
export type ParsedEvent = z.infer<typeof EventSchema>;
