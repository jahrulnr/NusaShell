import type { ParsedRequest } from "@nusashell/contracts";
import type {
  StartPluginCommand,
  StopPluginCommand,
  CallToolCommand,
} from "@nusashell/application";

export function mapToCommand(request: ParsedRequest):
  | { kind: "command"; command: StartPluginCommand | StopPluginCommand | CallToolCommand }
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
    default:
      return { kind: "query" };
  }
}
