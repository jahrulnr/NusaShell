import { z } from "zod";

export const PluginStartRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.start"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    pluginId: z.string().min(1),
  }),
});

export const PluginStopRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.stop"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    pluginId: z.string().min(1),
  }),
});

export const PluginListRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.list"),
  protocolVersion: z.string().optional(),
  payload: z.object({}).optional().default({}),
});

export const ToolCallRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("tool.call"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    pluginId: z.string().min(1),
    requestId: z.string().min(1),
    toolName: z.string().min(1),
    args: z.record(z.string(), z.unknown()),
    timeoutMs: z.number().int().positive().optional(),
  }),
});

export const ToolCancelRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("tool.cancel"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    pluginId: z.string().min(1),
    requestId: z.string().min(1),
  }),
});

export const PluginRestartRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.restart"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    pluginId: z.string().min(1),
  }),
});

export const PluginInstallRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.install"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    source: z.enum(["url", "local"]),
    path: z.string().min(1),
  }),
});

export const PluginUninstallRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.uninstall"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    pluginId: z.string().min(1),
  }),
});
export const PluginAutostartRequestSchema = z.object({ kind: z.literal("request"), id: z.string().min(1), method: z.literal("plugin.autostart"), protocolVersion: z.string().optional(), payload: z.object({ pluginId: z.string().min(1), autostart: z.boolean() }) });

export const PluginGetRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.get"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    pluginId: z.string().min(1),
  }),
});

export const PluginStateRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.state"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    pluginId: z.string().min(1),
  }),
});

export const ToolListRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("tool.list"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    pluginId: z.string().min(1),
  }),
});

export const PromptListRequestSchema = z.object({ kind: z.literal("request"), id: z.string().min(1), method: z.literal("prompt.list"), protocolVersion: z.string().optional(), payload: z.object({ pluginId: z.string().min(1) }) });
export const PromptGetRequestSchema = z.object({ kind: z.literal("request"), id: z.string().min(1), method: z.literal("prompt.get"), protocolVersion: z.string().optional(), payload: z.object({ pluginId: z.string().min(1), name: z.string().min(1), args: z.record(z.string(), z.string()).default({}) }) });
export const ResourceListRequestSchema = z.object({ kind: z.literal("request"), id: z.string().min(1), method: z.literal("resource.list"), protocolVersion: z.string().optional(), payload: z.object({ pluginId: z.string().min(1) }) });
export const ResourceTemplateListRequestSchema = z.object({ kind: z.literal("request"), id: z.string().min(1), method: z.literal("resource.template.list"), protocolVersion: z.string().optional(), payload: z.object({ pluginId: z.string().min(1) }) });
export const ResourceReadRequestSchema = z.object({ kind: z.literal("request"), id: z.string().min(1), method: z.literal("resource.read"), protocolVersion: z.string().optional(), payload: z.object({ pluginId: z.string().min(1), uri: z.string().min(1) }) });

const AgentContentPartSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("text"), text: z.string() }),
  z.object({
    type: z.literal("image"),
    dataUrl: z.string().max(6_000_000).regex(/^data:image\/[^;,]+;base64,/i),
    name: z.string().max(255).optional(),
    detail: z.enum(["auto", "low", "high"]).optional(),
  }),
  z.object({
    type: z.literal("file"),
    dataUrl: z.string().max(6_000_000).regex(/^data:[^;,]+;base64,/i),
    mediaType: z.string().min(1).max(100),
    name: z.string().min(1).max(255),
  }),
]);

const AgentMessageSchema = z.union([
  z.object({ role: z.literal("system"), content: z.string().min(1) }),
  z.object({
    role: z.literal("user"),
    content: z.union([z.string().min(1), z.array(AgentContentPartSchema).min(1).max(12)]),
  }),
  z.object({
    role: z.literal("assistant"),
    content: z.string().optional(),
    toolCalls: z.array(z.object({
      id: z.string().min(1),
      name: z.string().min(1),
      args: z.record(z.string(), z.unknown()),
    })).optional(),
  }),
  z.object({
    role: z.literal("tool"),
    toolCallId: z.string().min(1),
    name: z.string().min(1),
    content: z.string(),
  }),
]);

const AgentModelCapabilitiesSchema = z.object({
  contextWindow: z.number().int().positive().max(2_000_000).optional(),
  maxOutput: z.number().int().positive().max(2_000_000).optional(),
  inputModes: z.array(z.string().min(1).max(50)).max(20).optional(),
  outputModes: z.array(z.string().min(1).max(50)).max(20).optional(),
  supportedEfforts: z.array(z.enum(["auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"])).max(8).optional(),
  defaultEffort: z.enum(["auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"]).optional(),
  reasoningSupported: z.boolean().optional(),
  reasoningMandatory: z.boolean().optional(),
  reasoningSupportsMaxTokens: z.boolean().optional(),
  supportsTools: z.boolean().optional(),
  supportsVision: z.boolean().optional(),
});

export const AgentRunRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("agent.run"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    messages: z.array(AgentMessageSchema).min(1),
    pluginIds: z.array(z.string().min(1)).default([]),
    providerId: z.string().min(1).optional(),
    model: z.string().min(1).max(200).optional(),
    effort: z.enum(["auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"]).optional(),
    modelCapabilities: AgentModelCapabilitiesSchema.optional(),
    userPrompt: z.string().max(10000).optional(),
    traceId: z.string().min(1).max(128).optional(),
    maxToolRounds: z.number().int().min(1).max(100).optional(),
    workspace: z.string().max(4096).optional(),
  }),
});

export const AgentCancelRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("agent.cancel"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    traceId: z.string().min(1).max(128),
  }),
});

export const AgentAskAnswerRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("agent.ask_answer"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    traceId: z.string().min(1).max(128),
    callId: z.string().min(1).max(128),
    via: z.enum(["option", "text"]),
    optionIds: z.array(z.string().min(1).max(128)).max(16).optional(),
    text: z.string().max(8000).optional(),
  }),
});

export const SystemPingRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("system.ping"),
  protocolVersion: z.string().optional(),
  payload: z.object({}).optional().default({}),
});

export const SystemVersionRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("system.version"),
  protocolVersion: z.string().optional(),
  payload: z.object({}).optional().default({}),
});

const JobModeSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("agent"), prompt: z.string().min(1).max(10000) }),
  z.object({
    type: z.literal("tool"),
    pluginId: z.string().min(1),
    toolName: z.string().min(1),
    args: z.record(z.string(), z.unknown()),
  }),
]);

export const JobAddRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("job.add"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    name: z.string().min(1).max(200),
    schedule: z.string().min(1).max(200),
    mode: JobModeSchema,
    repeatTimes: z.number().int().min(1).max(100000).optional(),
  }),
});

export const JobListRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("job.list"),
  protocolVersion: z.string().optional(),
  payload: z.object({}).optional().default({}),
});

export const JobSetEnabledRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("job.set-enabled"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    id: z.string().min(1),
    enabled: z.boolean(),
  }),
});

export const JobRunRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("job.run"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    id: z.string().min(1),
  }),
});

export const JobRemoveRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("job.remove"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    id: z.string().min(1),
  }),
});

export const JobOutputRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("job.output"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    id: z.string().min(1),
    limit: z.number().int().min(1).max(100).optional(),
  }),
});

export const JobValidateScheduleRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("job.validate-schedule"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    schedule: z.string().min(1).max(200),
  }),
});

export const SubscribeRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("subscribe"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    eventTypes: z.array(z.string()).optional(),
  }).optional().default({}),
});

export const UnsubscribeRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("unsubscribe"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    eventTypes: z.array(z.string()).optional(),
  }).optional().default({}),
});

export const RequestSchema = z.discriminatedUnion("method", [
  PluginStartRequestSchema,
  PluginStopRequestSchema,
  PluginListRequestSchema,
  PluginRestartRequestSchema,
  PluginInstallRequestSchema,
  PluginUninstallRequestSchema,
  PluginAutostartRequestSchema,
  PluginGetRequestSchema,
  PluginStateRequestSchema,
  ToolCallRequestSchema,
  ToolCancelRequestSchema,
  ToolListRequestSchema,
  PromptListRequestSchema,
  PromptGetRequestSchema,
  ResourceListRequestSchema,
  ResourceTemplateListRequestSchema,
  ResourceReadRequestSchema,
  AgentRunRequestSchema,
  AgentCancelRequestSchema,
  AgentAskAnswerRequestSchema,
  SystemPingRequestSchema,
  SystemVersionRequestSchema,
  JobAddRequestSchema,
  JobListRequestSchema,
  JobSetEnabledRequestSchema,
  JobRunRequestSchema,
  JobRemoveRequestSchema,
  JobOutputRequestSchema,
  JobValidateScheduleRequestSchema,
  SubscribeRequestSchema,
  UnsubscribeRequestSchema,
]);

export type PluginStartRequest = z.infer<typeof PluginStartRequestSchema>;
export type PluginStopRequest = z.infer<typeof PluginStopRequestSchema>;
export type PluginListRequest = z.infer<typeof PluginListRequestSchema>;
export type PluginRestartRequest = z.infer<typeof PluginRestartRequestSchema>;
export type PluginInstallRequest = z.infer<typeof PluginInstallRequestSchema>;
export type PluginUninstallRequest = z.infer<typeof PluginUninstallRequestSchema>;
export type PluginAutostartRequest = z.infer<typeof PluginAutostartRequestSchema>;
export type PluginGetRequest = z.infer<typeof PluginGetRequestSchema>;
export type PluginStateRequest = z.infer<typeof PluginStateRequestSchema>;
export type ToolCallRequest = z.infer<typeof ToolCallRequestSchema>;
export type ToolCancelRequest = z.infer<typeof ToolCancelRequestSchema>;
export type ToolListRequest = z.infer<typeof ToolListRequestSchema>;
export type PromptListRequest = z.infer<typeof PromptListRequestSchema>;
export type PromptGetRequest = z.infer<typeof PromptGetRequestSchema>;
export type ResourceListRequest = z.infer<typeof ResourceListRequestSchema>;
export type ResourceTemplateListRequest = z.infer<typeof ResourceTemplateListRequestSchema>;
export type ResourceReadRequest = z.infer<typeof ResourceReadRequestSchema>;
export type AgentRunRequest = z.infer<typeof AgentRunRequestSchema>;
export type AgentCancelRequest = z.infer<typeof AgentCancelRequestSchema>;
export type AgentAskAnswerRequest = z.infer<typeof AgentAskAnswerRequestSchema>;
export type JobAddRequest = z.infer<typeof JobAddRequestSchema>;
export type JobListRequest = z.infer<typeof JobListRequestSchema>;
export type JobSetEnabledRequest = z.infer<typeof JobSetEnabledRequestSchema>;
export type JobRunRequest = z.infer<typeof JobRunRequestSchema>;
export type JobRemoveRequest = z.infer<typeof JobRemoveRequestSchema>;
export type JobOutputRequest = z.infer<typeof JobOutputRequestSchema>;
export type JobValidateScheduleRequest = z.infer<typeof JobValidateScheduleRequestSchema>;
export type ParsedRequest = z.infer<typeof RequestSchema>;
