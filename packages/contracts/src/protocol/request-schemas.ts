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

const AgentMessageSchema = z.union([
  z.object({ role: z.enum(["system", "user"]), content: z.string().min(1) }),
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

export const AgentRunRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("agent.run"),
  protocolVersion: z.string().optional(),
  payload: z.object({
    messages: z.array(AgentMessageSchema).min(1),
    pluginIds: z.array(z.string().min(1)).default([]),
    providerId: z.string().min(1).optional(),
    maxToolRounds: z.number().int().min(1).max(32).optional(),
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
  SystemPingRequestSchema,
  SystemVersionRequestSchema,
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
export type ParsedRequest = z.infer<typeof RequestSchema>;
