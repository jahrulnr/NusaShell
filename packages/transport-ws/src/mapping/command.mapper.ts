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
  AnswerAskQuestionCommand,
  AddJobCommand,
  SetJobEnabledCommand,
  RunJobNowCommand,
  RemoveJobCommand,
  RunAcpTurnCommand,
  CancelAcpTurnCommand,
  AnswerAcpPermissionCommand,
  AnswerAcpAskCommand,
  SetAcpConfigOptionCommand,
  EnsureAcpSessionCommand,
} from "@nusashell/application";

export function mapToCommand(request: ParsedRequest):
  | { kind: "command"; command: StartPluginCommand | StopPluginCommand | RestartPluginCommand | InstallPluginCommand | UninstallPluginCommand | SetPluginAutostartCommand | CallToolCommand | CancelToolCallCommand | RunAgentTurnCommand | CancelAgentTurnCommand | AnswerAskQuestionCommand | AddJobCommand | SetJobEnabledCommand | RunJobNowCommand | RemoveJobCommand | RunAcpTurnCommand | CancelAcpTurnCommand | AnswerAcpPermissionCommand | AnswerAcpAskCommand | SetAcpConfigOptionCommand | EnsureAcpSessionCommand }
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
          interactive: true,
          ...(request.payload.providerId !== undefined ? { providerId: request.payload.providerId } : {}),
          ...(request.payload.model !== undefined ? { model: request.payload.model } : {}),
          ...(request.payload.effort !== undefined ? { effort: request.payload.effort } : {}),
          ...(request.payload.modelCapabilities !== undefined ? { modelCapabilities: request.payload.modelCapabilities } : {}),
          ...(request.payload.userPrompt !== undefined ? { userPrompt: request.payload.userPrompt } : {}),
          ...(request.payload.traceId !== undefined ? { traceId: request.payload.traceId } : {}),
          ...(request.payload.maxToolRounds !== undefined ? { maxToolRounds: request.payload.maxToolRounds } : {}),
          ...(request.payload.workspace !== undefined ? { workspace: request.payload.workspace } : {}),
          ...(request.payload.resume !== undefined ? { resume: request.payload.resume } : {}),
          ...(request.payload.supersedeTraceId !== undefined ? { supersedeTraceId: request.payload.supersedeTraceId } : {}),
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
    case "agent.ask_answer":
      return {
        kind: "command",
        command: {
          kind: "answer-ask-question",
          traceId: request.payload.traceId,
          callId: request.payload.callId,
          via: request.payload.via,
          ...(request.payload.optionIds !== undefined ? { optionIds: request.payload.optionIds } : {}),
          ...(request.payload.text !== undefined ? { text: request.payload.text } : {}),
        } as AnswerAskQuestionCommand,
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
    case "acp.run":
      return {
        kind: "command",
        command: {
          kind: "run-acp-turn",
          traceId: request.payload.traceId,
          conversationId: request.payload.conversationId,
          workspace: request.payload.workspace,
          provider: request.payload.provider,
          prompt: request.payload.prompt,
        } as RunAcpTurnCommand,
      };
    case "acp.cancel":
      return {
        kind: "command",
        command: {
          kind: "cancel-acp-turn",
          traceId: request.payload.traceId,
          conversationId: request.payload.conversationId,
        } as CancelAcpTurnCommand,
      };
    case "acp.permission_answer":
      return {
        kind: "command",
        command: {
          kind: "answer-acp-permission",
          traceId: request.payload.traceId,
          conversationId: request.payload.conversationId,
          requestId: request.payload.requestId,
          optionId: request.payload.optionId,
        } as AnswerAcpPermissionCommand,
      };
    case "acp.ask_answer":
      return {
        kind: "command",
        command: {
          kind: "answer-acp-ask",
          traceId: request.payload.traceId,
          conversationId: request.payload.conversationId,
          requestId: request.payload.requestId,
          optionIds: request.payload.optionIds,
          text: request.payload.text,
        } as AnswerAcpAskCommand,
      };
    case "acp.set_config_option":
      return {
        kind: "command",
        command: {
          kind: "set-acp-config-option",
          conversationId: request.payload.conversationId,
          configId: request.payload.configId,
          value: request.payload.value,
        } as SetAcpConfigOptionCommand,
      };
    case "acp.ensure_session":
      return {
        kind: "command",
        command: {
          kind: "ensure-acp-session",
          conversationId: request.payload.conversationId,
          workspace: request.payload.workspace,
          provider: {
            providerId: request.payload.provider.providerId,
            command: request.payload.provider.command,
            args: request.payload.provider.args,
            authMethodId: request.payload.provider.authMethodId,
          },
        } as EnsureAcpSessionCommand,
      };
    default:
      return { kind: "query" };
  }
}
