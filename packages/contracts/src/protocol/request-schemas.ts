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
  PluginGetRequestSchema,
  PluginStateRequestSchema,
  ToolCallRequestSchema,
  ToolCancelRequestSchema,
  ToolListRequestSchema,
  SystemPingRequestSchema,
  SystemVersionRequestSchema,
  SubscribeRequestSchema,
  UnsubscribeRequestSchema,
]);

export type PluginStartRequest = z.infer<typeof PluginStartRequestSchema>;
export type PluginStopRequest = z.infer<typeof PluginStopRequestSchema>;
export type PluginListRequest = z.infer<typeof PluginListRequestSchema>;
export type PluginRestartRequest = z.infer<typeof PluginRestartRequestSchema>;
export type PluginGetRequest = z.infer<typeof PluginGetRequestSchema>;
export type PluginStateRequest = z.infer<typeof PluginStateRequestSchema>;
export type ToolCallRequest = z.infer<typeof ToolCallRequestSchema>;
export type ToolCancelRequest = z.infer<typeof ToolCancelRequestSchema>;
export type ToolListRequest = z.infer<typeof ToolListRequestSchema>;
export type ParsedRequest = z.infer<typeof RequestSchema>;
