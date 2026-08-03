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
    streamSeq: z.number().int().positive().optional(),
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
    streamSeq: z.number().int().positive().optional(),
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
    streamSeq: z.number().int().positive().optional(),
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
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

export const AgentAskRequestEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.ask_request"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    callId: z.string().min(1),
    question: z.string().min(1),
    options: z.array(z.object({
      id: z.string().min(1),
      label: z.string().min(1),
      description: z.string().optional(),
      default: z.boolean().optional(),
      icon: z.string().optional(),
      image: z.string().optional(),
    })).min(1).max(8),
    allowFreeText: z.boolean(),
    multiSelect: z.boolean(),
    streamSeq: z.number().int().positive().optional(),
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
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

export const AgentTurnStartedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.turn_started"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    conversationId: z.string().min(1).optional(),
    streamSeq: z.number().int().positive(),
    timestamp: z.string(),
  }),
});

export const AgentTurnEndEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.turn_end"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    reason: z.enum(["completed", "cancelled", "failed", "superseded"]),
    streamSeq: z.number().int().positive(),
    timestamp: z.string(),
  }),
});

export const AgentTurnSupersededEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.turn_superseded"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    byTraceId: z.string().min(1),
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

export const AgentCancelRequestedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("agent.cancel_requested"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    streamSeq: z.number().int().positive().optional(),
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

export const JobCompletedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("job.completed"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    jobId: z.string(),
    name: z.string(),
    summary: z.string(),
    timestamp: z.string(),
    traceId: z.string().optional(),
  }),
});

export const JobFailedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("job.failed"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    jobId: z.string(),
    name: z.string(),
    error: z.string(),
    timestamp: z.string(),
    traceId: z.string().optional(),
  }),
});

export const JobStartedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("job.started"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    jobId: z.string(),
    name: z.string(),
    traceId: z.string(),
    startedAt: z.string(),
    timestamp: z.string(),
  }),
});

export const JobCancelledEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("job.cancelled"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    jobId: z.string(),
    name: z.string(),
    traceId: z.string(),
    timestamp: z.string(),
  }),
});

export const AcpTextDeltaEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("acp.text_delta"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    delta: z.string(),
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

export const AcpThoughtDeltaEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("acp.thought_delta"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    delta: z.string(),
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

const AcpToolKindSchema = z.enum(["terminal", "read", "edit", "unknown"]);
const AcpToolStatusSchema = z.enum(["pending", "running", "ok", "fail"]);

export const AcpToolCallEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("acp.tool_call"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    call: z.object({
      id: z.string().min(1),
      title: z.string().min(1),
      kind: AcpToolKindSchema,
      status: AcpToolStatusSchema,
      summary: z.string().max(1000),
    }),
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

export const AcpToolCallUpdateEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("acp.tool_call_update"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    callId: z.string().min(1),
    status: AcpToolStatusSchema,
    summary: z.string().max(1000).optional(),
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

export const AcpPlanEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("acp.plan"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    steps: z.array(z.object({
      id: z.string().min(1),
      text: z.string().min(1),
      done: z.boolean(),
    })),
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

const AcpPermissionOptionSchema = z.object({
  optionId: z.string().min(1),
  name: z.string().min(1),
  kind: z.enum(["allow", "deny", "allow_once", "allow_always"]).optional(),
});

export const AcpPermissionRequestEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("acp.permission_request"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    requestId: z.string().min(1),
    toolTitle: z.string().min(1),
    detail: z.string().max(2000).optional(),
    options: z.array(AcpPermissionOptionSchema).min(1),
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

const AcpAskOptionSchema = z.object({
  optionId: z.string().min(1),
  name: z.string().min(1),
});

export const AcpAskRequestEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("acp.ask_request"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    requestId: z.string().min(1),
    question: z.string().min(1).max(4000),
    options: z.array(AcpAskOptionSchema).optional(),
    multiSelect: z.boolean().optional(),
    allowFreeText: z.boolean().optional(),
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

export const AcpTurnEndEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("acp.turn_end"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    ok: z.boolean(),
    error: z.string().max(4000).optional(),
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

export const AcpSessionStateEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("acp.session_state"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    traceId: z.string().min(1),
    conversationId: z.string().min(1),
    state: z.enum(["idle", "starting", "running", "error", "cancelled"]),
    streamSeq: z.number().int().positive().optional(),
    timestamp: z.string(),
  }),
});

export const SubagentRunStartedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("subagent.run_started"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    runId: z.string().min(1),
    conversationId: z.string().min(1),
    providerId: z.string().min(1),
    prompt: z.string(),
    title: z.string().optional(),
    parentConversationId: z.string().optional(),
    parentTraceId: z.string().optional(),
    timestamp: z.string(),
  }),
});

export const SubagentRunEndedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("subagent.run_ended"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    runId: z.string().min(1),
    conversationId: z.string().min(1),
    providerId: z.string().min(1),
    ok: z.boolean(),
    summary: z.string().optional(),
    error: z.string().optional(),
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
  AgentAskRequestEventSchema,
  AgentContextEventSchema,
  AgentTurnStartedEventSchema,
  AgentTurnEndEventSchema,
  AgentTurnSupersededEventSchema,
  AgentCancelRequestedEventSchema,
  AgentLearningUpdatedEventSchema,
  JobCompletedEventSchema,
  JobFailedEventSchema,
  JobStartedEventSchema,
  JobCancelledEventSchema,
  AcpTextDeltaEventSchema,
  AcpThoughtDeltaEventSchema,
  AcpToolCallEventSchema,
  AcpToolCallUpdateEventSchema,
  AcpPlanEventSchema,
  AcpPermissionRequestEventSchema,
  AcpAskRequestEventSchema,
  AcpTurnEndEventSchema,
  AcpSessionStateEventSchema,
  SubagentRunStartedEventSchema,
  SubagentRunEndedEventSchema,
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
export type AgentAskRequestEvent = z.infer<typeof AgentAskRequestEventSchema>;
export type AgentContextEvent = z.infer<typeof AgentContextEventSchema>;
export type AgentTurnStartedEvent = z.infer<typeof AgentTurnStartedEventSchema>;
export type AgentTurnEndEvent = z.infer<typeof AgentTurnEndEventSchema>;
export type AgentTurnSupersededEvent = z.infer<typeof AgentTurnSupersededEventSchema>;
export type AgentCancelRequestedEvent = z.infer<typeof AgentCancelRequestedEventSchema>;
export type AgentLearningUpdatedEvent = z.infer<typeof AgentLearningUpdatedEventSchema>;
export type JobCompletedEvent = z.infer<typeof JobCompletedEventSchema>;
export type JobFailedEvent = z.infer<typeof JobFailedEventSchema>;
export type JobStartedEvent = z.infer<typeof JobStartedEventSchema>;
export type JobCancelledEvent = z.infer<typeof JobCancelledEventSchema>;
export type AcpTextDeltaEvent = z.infer<typeof AcpTextDeltaEventSchema>;
export type AcpThoughtDeltaEvent = z.infer<typeof AcpThoughtDeltaEventSchema>;
export type AcpToolCallEvent = z.infer<typeof AcpToolCallEventSchema>;
export type AcpToolCallUpdateEvent = z.infer<typeof AcpToolCallUpdateEventSchema>;
export type AcpPlanEvent = z.infer<typeof AcpPlanEventSchema>;
export type AcpPermissionRequestEvent = z.infer<typeof AcpPermissionRequestEventSchema>;
export type AcpAskRequestEvent = z.infer<typeof AcpAskRequestEventSchema>;
export type AcpTurnEndEvent = z.infer<typeof AcpTurnEndEventSchema>;
export type AcpSessionStateEvent = z.infer<typeof AcpSessionStateEventSchema>;
export type ParsedEvent = z.infer<typeof EventSchema>;
