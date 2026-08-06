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
      args: z.record(z.string(), z.unknown()).default({}),
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
    maxToolRounds: z.number().int().min(0).max(10_000).optional(),
    workspace: z.string().max(4096).optional(),
    resume: z.boolean().optional(),
    supersedeTraceId: z.string().min(1).max(128).optional(),
    conversationId: z.string().min(1).max(128).optional(),
    autoContinueIndex: z.number().int().min(0).max(100).optional(),
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

export const AgentGetActiveTurnRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("agent.get_active_turn"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    conversationId: z.string().min(1).max(128),
  }),
});

const TodoItemSchema = z.object({
  id: z.string().min(1).max(128),
  content: z.string().min(1).max(500),
  status: z.enum(["pending", "in_progress", "completed"]),
});

export const AgentTodosSetRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("agent.todos_set"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    conversationId: z.string().min(1).max(128),
    items: z.array(TodoItemSchema).max(50),
  }),
});

export const AgentTodosDeleteRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("agent.todos_delete"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    conversationId: z.string().min(1).max(128),
    ids: z.array(z.string().min(1).max(128)).max(50),
  }),
});

export const AgentTodosGetRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("agent.todos_get"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    conversationId: z.string().min(1).max(128),
  }),
});

export const AgentToolJobListRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("agent.tool_job_list"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    conversationId: z.string().min(1).max(128),
  }),
});

export const AgentToolJobKillRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("agent.tool_job_kill"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    handleId: z.string().min(1).max(128),
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
  z.object({
    type: z.literal("agent"),
    prompt: z.string().min(1).max(10000),
    providerId: z.string().min(1).optional(),
    model: z.string().min(1).max(200).optional(),
    effort: z.enum(["auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"]).optional(),
  }),
  z.object({
    type: z.literal("tool"),
    pluginId: z.string().min(1),
    toolName: z.string().min(1),
    args: z.record(z.string(), z.unknown()),
  }),
]);

const ConditionSchema = z.object({
  path: z.string().min(1).max(500),
  op: z.enum(["eq", "contains", "regex"]),
  value: z.string().max(2000),
});

const JobScheduleSchema = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("once"), runAt: z.string().min(1) }),
  z.object({ kind: z.literal("interval"), minutes: z.number().int().min(1).max(525600) }),
  z.object({ kind: z.literal("cron"), expr: z.string().min(1).max(200) }),
]);

const JobTriggerSchema = z.discriminatedUnion("kind", [
  z.object({
    kind: z.literal("schedule"),
    schedule: JobScheduleSchema,
  }),
  z.object({
    kind: z.literal("event"),
    pattern: z.string().min(1).max(500),
    pluginId: z.string().min(1).optional(),
    conditions: z.array(ConditionSchema).optional(),
    throttleMs: z.number().int().min(0).max(60 * 60 * 1000).optional(),
    maxFiresPerHour: z.number().int().min(1).max(100000).optional(),
  }),
]);

export const JobAddRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("job.add"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    name: z.string().min(1).max(200),
    schedule: z.string().min(1).max(200).optional(),
    trigger: JobTriggerSchema.optional(),
    mode: JobModeSchema,
    repeatTimes: z.number().int().min(1).max(100000).optional(),
    onComplete: z.object({
      type: z.string().min(1).max(200),
      payload: z.record(z.unknown()).optional(),
    }).optional(),
  }),
});

export const JobUpdateRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("job.update"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    id: z.string().min(1),
    name: z.string().min(1).max(200).optional(),
    schedule: z.string().min(1).max(200).optional(),
    trigger: JobTriggerSchema.optional(),
    mode: JobModeSchema.optional(),
    repeatTimes: z.number().int().min(1).max(100000).nullable().optional(),
    enabled: z.boolean().optional(),
    onComplete: z.object({
      type: z.string().min(1).max(200),
      payload: z.record(z.unknown()).optional(),
    }).nullable().optional(),
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

export const JobCancelRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("job.cancel"),
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
    includeBody: z.boolean().optional(),
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

const PipelineTriggerSchema = JobTriggerSchema;

const PipelineStepSchema = z.object({
  id: z.string().min(1).max(100),
  name: z.string().min(1).max(200),
  action: JobModeSchema,
  dependsOn: z.array(z.string().min(1).max(100)).optional(),
  condition: z.unknown().optional(),
  outputKey: z.string().min(1).max(100).optional(),
});

export const PipelineAddRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("pipeline.add"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    name: z.string().min(1).max(200),
    description: z.string().max(2000).optional(),
    trigger: PipelineTriggerSchema,
    steps: z.array(PipelineStepSchema).min(1),
    settings: z.object({
      timeoutMs: z.number().int().min(0).optional(),
    }).optional(),
  }),
});

export const PipelineUpdateRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("pipeline.update"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    id: z.string().min(1),
    name: z.string().min(1).max(200).optional(),
    description: z.string().max(2000).nullable().optional(),
    trigger: PipelineTriggerSchema.optional(),
    steps: z.array(PipelineStepSchema).min(1).optional(),
    settings: z.object({
      timeoutMs: z.number().int().min(0).optional(),
    }).nullable().optional(),
    enabled: z.boolean().optional(),
  }),
});

export const PipelineRemoveRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("pipeline.remove"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    id: z.string().min(1),
  }),
});

export const PipelineRunRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("pipeline.run"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    id: z.string().min(1),
  }),
});

export const PipelineCancelRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("pipeline.cancel"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    id: z.string().min(1),
  }),
});

export const PipelineListRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("pipeline.list"),
  protocolVersion: z.string().optional(),
  payload: z.object({}),
});

export const PipelineRunsRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("pipeline.runs"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    id: z.string().min(1),
    limit: z.number().int().min(1).max(100).optional(),
    includeBody: z.boolean().optional(),
  }),
});

export const PipelineRunGetRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("pipeline.run-get"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    runId: z.string().min(1),
    includeBody: z.boolean().optional(),
  }),
});

const AcpContentBlockSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("text"), text: z.string().min(1) }),
  z.object({
    type: z.literal("image"),
    data: z.string().min(1),
    mimeType: z.string().min(1),
  }),
]);

const AcpProviderDescriptorSchema = z.object({
  providerId: z.string().min(1),
  command: z.string().min(1),
  args: z.array(z.string()),
  authMethodId: z.string().optional(),
});

export const AcpRunRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("acp.run"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    traceId: z.string().min(1).max(128),
    conversationId: z.string().min(1).max(128),
    workspace: z.string().max(4096).optional(),
    provider: AcpProviderDescriptorSchema,
    prompt: z.array(AcpContentBlockSchema).min(1).max(32),
  }),
});

export const AcpCancelRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("acp.cancel"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    traceId: z.string().min(1).max(128),
    conversationId: z.string().min(1).max(128),
  }),
});

export const AcpPermissionAnswerRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("acp.permission_answer"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    traceId: z.string().min(1).max(128),
    conversationId: z.string().min(1).max(128),
    requestId: z.string().min(1).max(128),
    optionId: z.string().min(1).max(128),
  }),
});

export const AcpAskAnswerRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("acp.ask_answer"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    traceId: z.string().min(1).max(128),
    conversationId: z.string().min(1).max(128),
    requestId: z.string().min(1).max(128),
    optionIds: z.array(z.string().min(1).max(128)).max(16).optional(),
    text: z.string().max(8000).optional(),
  }),
});

export const AcpSessionInfoRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("acp.session_info"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    conversationId: z.string().min(1).max(128),
  }),
});

export const AcpSetConfigOptionRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("acp.set_config_option"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    conversationId: z.string().min(1).max(128),
    configId: z.string().min(1).max(128),
    value: z.union([z.string(), z.boolean()]),
  }),
});

export const AcpEnsureSessionRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("acp.ensure_session"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    conversationId: z.string().min(1).max(128),
    workspace: z.string().optional(),
    provider: z.object({
      providerId: z.string().min(1),
      command: z.string().min(1),
      args: z.array(z.string()).default([]),
      authMethodId: z.string().optional(),
      preferredConfig: z.record(z.string(), z.union([z.string(), z.boolean()])).optional(),
    }),
  }),
});

export const AcpProbeRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("acp.probe"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    provider: z.object({
      providerId: z.string().min(1),
      command: z.string().min(1),
      args: z.array(z.string()).default([]),
      authMethodId: z.string().optional(),
      env: z.record(z.string(), z.string()).optional(),
    }),
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
  AgentGetActiveTurnRequestSchema,
  AgentTodosSetRequestSchema,
  AgentTodosDeleteRequestSchema,
  AgentTodosGetRequestSchema,
  AgentToolJobListRequestSchema,
  AgentToolJobKillRequestSchema,
  SystemPingRequestSchema,
  SystemVersionRequestSchema,
  JobAddRequestSchema,
  JobUpdateRequestSchema,
  JobListRequestSchema,
  JobSetEnabledRequestSchema,
  JobRunRequestSchema,
  JobCancelRequestSchema,
  JobRemoveRequestSchema,
  JobOutputRequestSchema,
  JobValidateScheduleRequestSchema,
  PipelineAddRequestSchema,
  PipelineUpdateRequestSchema,
  PipelineRemoveRequestSchema,
  PipelineRunRequestSchema,
  PipelineCancelRequestSchema,
  PipelineListRequestSchema,
  PipelineRunsRequestSchema,
  PipelineRunGetRequestSchema,
  AcpRunRequestSchema,
  AcpCancelRequestSchema,
  AcpPermissionAnswerRequestSchema,
  AcpAskAnswerRequestSchema,
  AcpSessionInfoRequestSchema,
  AcpSetConfigOptionRequestSchema,
  AcpEnsureSessionRequestSchema,
  AcpProbeRequestSchema,
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
export type AgentGetActiveTurnRequest = z.infer<typeof AgentGetActiveTurnRequestSchema>;
export type AgentTodosSetRequest = z.infer<typeof AgentTodosSetRequestSchema>;
export type AgentTodosDeleteRequest = z.infer<typeof AgentTodosDeleteRequestSchema>;
export type AgentTodosGetRequest = z.infer<typeof AgentTodosGetRequestSchema>;
export type AgentToolJobListRequest = z.infer<typeof AgentToolJobListRequestSchema>;
export type AgentToolJobKillRequest = z.infer<typeof AgentToolJobKillRequestSchema>;
export type JobAddRequest = z.infer<typeof JobAddRequestSchema>;
export type JobUpdateRequest = z.infer<typeof JobUpdateRequestSchema>;
export type JobListRequest = z.infer<typeof JobListRequestSchema>;
export type JobSetEnabledRequest = z.infer<typeof JobSetEnabledRequestSchema>;
export type JobRunRequest = z.infer<typeof JobRunRequestSchema>;
export type JobCancelRequest = z.infer<typeof JobCancelRequestSchema>;
export type JobRemoveRequest = z.infer<typeof JobRemoveRequestSchema>;
export type JobOutputRequest = z.infer<typeof JobOutputRequestSchema>;
export type JobValidateScheduleRequest = z.infer<typeof JobValidateScheduleRequestSchema>;
export type PipelineAddRequest = z.infer<typeof PipelineAddRequestSchema>;
export type PipelineUpdateRequest = z.infer<typeof PipelineUpdateRequestSchema>;
export type PipelineRemoveRequest = z.infer<typeof PipelineRemoveRequestSchema>;
export type PipelineRunRequest = z.infer<typeof PipelineRunRequestSchema>;
export type PipelineCancelRequest = z.infer<typeof PipelineCancelRequestSchema>;
export type PipelineListRequest = z.infer<typeof PipelineListRequestSchema>;
export type PipelineRunsRequest = z.infer<typeof PipelineRunsRequestSchema>;
export type PipelineRunGetRequest = z.infer<typeof PipelineRunGetRequestSchema>;
export type AcpRunRequest = z.infer<typeof AcpRunRequestSchema>;
export type AcpCancelRequest = z.infer<typeof AcpCancelRequestSchema>;
export type AcpPermissionAnswerRequest = z.infer<typeof AcpPermissionAnswerRequestSchema>;
export type AcpAskAnswerRequest = z.infer<typeof AcpAskAnswerRequestSchema>;
export type AcpSessionInfoRequest = z.infer<typeof AcpSessionInfoRequestSchema>;
export type AcpSetConfigOptionRequest = z.infer<typeof AcpSetConfigOptionRequestSchema>;
export type AcpEnsureSessionRequest = z.infer<typeof AcpEnsureSessionRequestSchema>;
export type AcpProbeRequest = z.infer<typeof AcpProbeRequestSchema>;
export type ParsedRequest = z.infer<typeof RequestSchema>;
