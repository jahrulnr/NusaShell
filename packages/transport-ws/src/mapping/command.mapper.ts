import type { ParsedRequest } from "@nusashell/contracts";
import type {
  StartPluginCommand,
  StopPluginCommand,
  RestartPluginCommand,
  CallToolCommand,
  CancelToolCallCommand,
} from "@nusashell/application";

export function mapToCommand(request: ParsedRequest):
  | { kind: "command"; command: StartPluginCommand | StopPluginCommand | RestartPluginCommand | CallToolCommand | CancelToolCallCommand }
  | { kind: "query" } {
  switch (request.method) {
    case "plugin.start":
      return {
        kind: "command",
        command: {
          kind: "start-plugin",
          pluginId: request.payload.pluginId,
        } as StartPluginCommand,
      };
    case "plugin.stop":
      return {
        kind: "command",
        command: {
          kind: "stop-plugin",
          pluginId: request.payload.pluginId,
        } as StopPluginCommand,
      };
    case "plugin.restart":
      return {
        kind: "command",
        command: {
          kind: "restart-plugin",
          pluginId: request.payload.pluginId,
        } as RestartPluginCommand,
      };
    case "tool.call":
      return {
        kind: "command",
        command: {
          kind: "call-tool",
          pluginId: request.payload.pluginId,
          requestId: request.payload.requestId,
          toolName: request.payload.toolName,
          args: request.payload.args,
          ...(request.payload.timeoutMs !== undefined
            ? { timeoutMs: request.payload.timeoutMs }
            : {}),
        } as CallToolCommand,
      };
    case "tool.cancel":
      return {
        kind: "command",
        command: {
          kind: "cancel-tool-call",
          pluginId: request.payload.pluginId,
          requestId: request.payload.requestId,
        } as CancelToolCallCommand,
      };
    default:
      return { kind: "query" };
  }
}
