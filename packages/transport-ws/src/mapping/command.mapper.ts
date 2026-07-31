import type { ParsedRequest } from "@nusashell/contracts";
import type {
  StartPluginCommand,
  StopPluginCommand,
  RestartPluginCommand,
  InstallPluginCommand,
  UninstallPluginCommand,
  CallToolCommand,
  CancelToolCallCommand,
  SetPluginAutostartCommand,
  RunAgentTurnCommand,
  CancelAgentTurnCommand,
  AddJobCommand,
  SetJobEnabledCommand,
  RunJobNowCommand,
  RemoveJobCommand,
} from "@nusashell/application";

export function mapToCommand(request: ParsedRequest):
  | { kind: "command"; command: StartPluginCommand | StopPluginCommand | RestartPluginCommand | InstallPluginCommand | UninstallPluginCommand | SetPluginAutostartCommand | CallToolCommand | CancelToolCallCommand | RunAgentTurnCommand | CancelAgentTurnCommand | AddJobCommand | SetJobEnabledCommand | RunJobNowCommand | RemoveJobCommand }
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
    case "plugin.install":
      return {
        kind: "command",
        command: {
          kind: "install-plugin",
          source: request.payload.source,
          path: request.payload.path,
        } as InstallPluginCommand,
      };
    case "plugin.uninstall":
      return {
        kind: "command",
        command: {
          kind: "uninstall-plugin",
          pluginId: request.payload.pluginId,
        } as UninstallPluginCommand,
      };
    case "plugin.autostart":
      return { kind: "command", command: { kind: "set-plugin-autostart", pluginId: request.payload.pluginId, autostart: request.payload.autostart } as SetPluginAutostartCommand };
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
    case "agent.run":
      return {
        kind: "command",
        command: {
          kind: "run-agent-turn",
          messages: request.payload.messages,
          pluginIds: request.payload.pluginIds,
          ...(request.payload.providerId !== undefined ? { providerId: request.payload.providerId } : {}),
          ...(request.payload.model !== undefined ? { model: request.payload.model } : {}),
          ...(request.payload.effort !== undefined ? { effort: request.payload.effort } : {}),
          ...(request.payload.modelCapabilities !== undefined ? { modelCapabilities: request.payload.modelCapabilities } : {}),
          ...(request.payload.userPrompt !== undefined ? { userPrompt: request.payload.userPrompt } : {}),
          ...(request.payload.traceId !== undefined ? { traceId: request.payload.traceId } : {}),
          ...(request.payload.maxToolRounds !== undefined ? { maxToolRounds: request.payload.maxToolRounds } : {}),
          ...(request.payload.workspace !== undefined ? { workspace: request.payload.workspace } : {}),
        } as RunAgentTurnCommand,
      };
    case "agent.cancel":
      return {
        kind: "command",
        command: {
          kind: "cancel-agent-turn",
          traceId: request.payload.traceId,
        } as CancelAgentTurnCommand,
      };
    case "job.add":
      return {
        kind: "command",
        command: {
          kind: "add-job",
          name: request.payload.name,
          schedule: request.payload.schedule,
          mode: request.payload.mode,
          ...(request.payload.repeatTimes !== undefined ? { repeatTimes: request.payload.repeatTimes } : {}),
        } as AddJobCommand,
      };
    case "job.set-enabled":
      return {
        kind: "command",
        command: {
          kind: "set-job-enabled",
          id: request.payload.id,
          enabled: request.payload.enabled,
        } as SetJobEnabledCommand,
      };
    case "job.run":
      return {
        kind: "command",
        command: {
          kind: "run-job-now",
          id: request.payload.id,
        } as RunJobNowCommand,
      };
    case "job.remove":
      return {
        kind: "command",
        command: {
          kind: "remove-job",
          id: request.payload.id,
        } as RemoveJobCommand,
      };
    default:
      return { kind: "query" };
  }
}
