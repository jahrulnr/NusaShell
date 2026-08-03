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
  UpdateJobCommand,
  SetJobEnabledCommand,
  RunJobNowCommand,
  CancelJobCommand,
  RemoveJobCommand,
  AddPipelineCommand,
  UpdatePipelineCommand,
  RemovePipelineCommand,
  RunPipelineCommand,
  RunAcpTurnCommand,
  CancelAcpTurnCommand,
  AnswerAcpPermissionCommand,
  AnswerAcpAskCommand,
  SetAcpConfigOptionCommand,
  EnsureAcpSessionCommand,
  ProbeAcpProviderCommand,
} from "@nusashell/application";

export function mapToCommand(request: ParsedRequest):
  | { kind: "command"; command: StartPluginCommand | StopPluginCommand | RestartPluginCommand | InstallPluginCommand | UninstallPluginCommand | SetPluginAutostartCommand | CallToolCommand | CancelToolCallCommand | RunAgentTurnCommand | CancelAgentTurnCommand | AnswerAskQuestionCommand | AddJobCommand | UpdateJobCommand | SetJobEnabledCommand | RunJobNowCommand | CancelJobCommand | RemoveJobCommand | AddPipelineCommand | UpdatePipelineCommand | RemovePipelineCommand | RunPipelineCommand | RunAcpTurnCommand | CancelAcpTurnCommand | AnswerAcpPermissionCommand | AnswerAcpAskCommand | SetAcpConfigOptionCommand | EnsureAcpSessionCommand | ProbeAcpProviderCommand }
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
          ...(request.payload.trigger !== undefined ? { trigger: request.payload.trigger } : {}),
          ...(request.payload.schedule !== undefined ? { schedule: request.payload.schedule } : {}),
          mode: request.payload.mode,
          ...(request.payload.repeatTimes !== undefined ? { repeatTimes: request.payload.repeatTimes } : {}),
          ...(request.payload.onComplete !== undefined ? { onComplete: request.payload.onComplete } : {}),
        } as AddJobCommand,
      };
    case "job.update":
      return {
        kind: "command",
        command: {
          kind: "update-job",
          id: request.payload.id,
          ...(request.payload.name !== undefined ? { name: request.payload.name } : {}),
          ...(request.payload.trigger !== undefined ? { trigger: request.payload.trigger } : {}),
          ...(request.payload.schedule !== undefined ? { schedule: request.payload.schedule } : {}),
          ...(request.payload.mode !== undefined ? { mode: request.payload.mode } : {}),
          ...(request.payload.repeatTimes !== undefined ? { repeatTimes: request.payload.repeatTimes } : {}),
          ...(request.payload.enabled !== undefined ? { enabled: request.payload.enabled } : {}),
          ...(request.payload.onComplete !== undefined ? { onComplete: request.payload.onComplete } : {}),
        } as UpdateJobCommand,
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
    case "job.cancel":
      return {
        kind: "command",
        command: {
          kind: "cancel-job",
          id: request.payload.id,
        } as CancelJobCommand,
      };
    case "job.remove":
      return {
        kind: "command",
        command: {
          kind: "remove-job",
          id: request.payload.id,
        } as RemoveJobCommand,
      };
    case "pipeline.add":
      return {
        kind: "command",
        command: {
          kind: "add-pipeline",
          name: request.payload.name,
          trigger: request.payload.trigger,
          steps: request.payload.steps,
          ...(request.payload.description !== undefined ? { description: request.payload.description } : {}),
          ...(request.payload.settings !== undefined ? { settings: request.payload.settings } : {}),
        } as AddPipelineCommand,
      };
    case "pipeline.update":
      return {
        kind: "command",
        command: {
          kind: "update-pipeline",
          id: request.payload.id,
          ...(request.payload.name !== undefined ? { name: request.payload.name } : {}),
          ...(request.payload.description !== undefined ? { description: request.payload.description } : {}),
          ...(request.payload.trigger !== undefined ? { trigger: request.payload.trigger } : {}),
          ...(request.payload.steps !== undefined ? { steps: request.payload.steps } : {}),
          ...(request.payload.settings !== undefined ? { settings: request.payload.settings } : {}),
          ...(request.payload.enabled !== undefined ? { enabled: request.payload.enabled } : {}),
        } as UpdatePipelineCommand,
      };
    case "pipeline.remove":
      return {
        kind: "command",
        command: {
          kind: "remove-pipeline",
          id: request.payload.id,
        } as RemovePipelineCommand,
      };
    case "pipeline.run":
      return {
        kind: "command",
        command: {
          kind: "run-pipeline",
          id: request.payload.id,
        } as RunPipelineCommand,
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
            ...(request.payload.provider.preferredConfig ? { preferredConfig: request.payload.provider.preferredConfig } : {}),
          },
        } as EnsureAcpSessionCommand,
      };
    case "acp.probe":
      return {
        kind: "command",
        command: {
          kind: "probe-acp-provider",
          provider: {
            providerId: request.payload.provider.providerId,
            command: request.payload.provider.command,
            args: request.payload.provider.args,
            ...(request.payload.provider.authMethodId ? { authMethodId: request.payload.provider.authMethodId } : {}),
            ...(request.payload.provider.env ? { env: request.payload.provider.env } : {}),
          },
        } as ProbeAcpProviderCommand,
      };
    default:
      return { kind: "query" };
  }
}
