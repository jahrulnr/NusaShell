import {
  CommandBus,
  QueryBus,
  StartPluginHandler,
  StopPluginHandler,
  RestartPluginHandler,
  InstallPluginHandler,
  UninstallPluginHandler,
  SetPluginAutostartHandler,
  ListPluginsHandler,
  GetPluginHandler,
  GetPluginStateHandler,
  CallToolHandler,
  CancelToolCallHandler,
  ListToolsHandler,
  ListPromptsHandler,
  GetPromptHandler,
  ListResourcesHandler,
  ListResourceTemplatesHandler,
  ReadResourceHandler,
  RunAgentTurnHandler,
  CancelAgentTurnHandler,
  AnswerAskQuestionHandler,
  ManageTodosHandler,
  KillToolJobHandler,
  GetActiveTurnHandler,
  ToolJobListHandler,
  AddJobHandler,
  UpdateJobHandler,
  SetJobEnabledHandler,
  RunJobNowHandler,
  CancelJobHandler,
  RemoveJobHandler,
  ListJobsHandler,
  JobOutputHandler,
  ValidateScheduleHandler,
  AddPipelineHandler,
  UpdatePipelineHandler,
  RemovePipelineHandler,
  RunPipelineHandler,
  ListPipelinesHandler,
  RunAcpTurnHandler,
  CancelAcpTurnHandler,
  AnswerAcpPermissionHandler,
  AnswerAcpAskHandler,
  SetAcpConfigOptionHandler,
  EnsureAcpSessionHandler,
  ProbeAcpProviderHandler,
  ImportAcpModelsHandler,
  GetAcpSessionInfoHandler,
  SystemPingHandler,
  SystemVersionHandler,
  ConfigureAiHandler,
  ConfigureAiRuntimeHandler,
  RemoveAiHandler,
  type AiConfigurationPort,
  type EventDispatcher,
  createAgentTextDeltaEvent,
  createAgentReasoningDeltaEvent,
  createAgentToolCallStartEvent,
  createAgentToolCallEndEvent,
  createAgentContextUpdateEvent,
  createAgentTurnStartedEvent,
  createAgentTurnEndEvent,
  createAgentTurnSupersededEvent,
  createAgentCancelRequestedEvent,
  createAgentTodoUpdatedEvent,
} from "@nusashell/application";
import { SystemClock, type Logger } from "@nusashell/infrastructure";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { ContainerOptions } from "../container.js";
import type { SubagentPortImpl } from "./subagent-port-impl.js";
import type { PluginRuntimeParts } from "./plugin-runtime.js";
import type { SkillsRuntimeParts } from "./skills-runtime.js";
import type { AgentRuntimeParts } from "./agent-runtime.js";
import type { JobRuntimeParts } from "./job-runtime.js";
import type { AcpRuntimeParts } from "./acp-runtime.js";

export interface BusParts {
  readonly commandBus: CommandBus;
  readonly queryBus: QueryBus;
}

export function registerBuses(
  options: ContainerOptions,
  logger: Logger,
  eventDispatcher: EventDispatcher,
  clock: SystemClock,
  plugin: PluginRuntimeParts,
  skills: SkillsRuntimeParts,
  agent: AgentRuntimeParts,
  jobs: JobRuntimeParts,
  acp: AcpRuntimeParts,
  aiConfiguration: AiConfigurationPort,
  subagentPort?: SubagentPortImpl,
): BusParts {
  const commandBus = new CommandBus();
  commandBus.register("start-plugin", new StartPluginHandler(plugin.runtimeManager));
  commandBus.register("stop-plugin", new StopPluginHandler(plugin.runtimeManager));
  commandBus.register("restart-plugin", new RestartPluginHandler(plugin.runtimeManager));
  commandBus.register("call-tool", new CallToolHandler(plugin.runtimeManager));
  commandBus.register("cancel-tool-call", new CancelToolCallHandler(plugin.runtimeManager));
  commandBus.register("set-plugin-autostart", new SetPluginAutostartHandler(plugin.runtimeManager));
  commandBus.register("run-agent-turn", new RunAgentTurnHandler(
    agent.agentProviderRegistry,
    agent.agentToolGateway,
    options.ai?.providerId || (options.ai?.stubEnabled ? "stub" : ""),
    agent.aiRuntime,
    logger,
    agent.agentTurnCoordinator,
    (traceId, delta) => {
      void eventDispatcher.publish(agent.withStreamSeq(createAgentTextDeltaEvent(traceId, delta)));
    },
    (traceId, delta) => {
      void eventDispatcher.publish(agent.withStreamSeq(createAgentReasoningDeltaEvent(traceId, delta)));
    },
    (traceId, call) => {
      void eventDispatcher.publish(agent.withStreamSeq(createAgentToolCallStartEvent(traceId, call)));
    },
    (traceId, execution) => {
      void eventDispatcher.publish(agent.withStreamSeq(createAgentToolCallEndEvent(traceId, execution)));
    },
    (traceId, update) => {
      void eventDispatcher.publish(agent.withStreamSeq(createAgentContextUpdateEvent(traceId, update.estimatedTokens, update.usage)));
    },
    agent.promptLoader,
    agent.aiRuntime.userPrompt,
    skills.memoryStore,
    async (result, context) => {
      void agent.backgroundReviewScheduler.tick(result);
      void skills.skillCuratorScheduler.tick();
      if (context?.conversationId && options.sealAgentTurn) {
        try {
          await options.sealAgentTurn(context.conversationId, result, { resume: context.resume === true });
        } catch (error) {
          logger.error("sealAgentTurn failed for conversation %s: %s", context.conversationId, error instanceof Error ? error.message : String(error));
        }
      }
    },
    (traceId, reason) => {
      void eventDispatcher.publish(agent.withStreamSeq(createAgentTurnEndEvent(traceId, reason)));
      agent.streamSeqRegistry.clear(traceId);
    },
    (traceId) => {
      void eventDispatcher.publish(agent.withStreamSeq(createAgentTurnStartedEvent(traceId)));
    },
    (oldTraceId, newTraceId) => {
      void eventDispatcher.publish(agent.withStreamSeq(createAgentTurnSupersededEvent(oldTraceId, newTraceId)));
    },
    agent.runtimeOsProbe,
    agent.activeTurns,
    undefined,
    subagentPort,
    agent.conversationTodos,
  ));
  commandBus.register("cancel-agent-turn", new CancelAgentTurnHandler(
    agent.agentTurnCoordinator,
    (traceId) => { void eventDispatcher.publish(agent.withStreamSeq(createAgentCancelRequestedEvent(traceId))); },
  ));
  commandBus.register("answer-ask-question", new AnswerAskQuestionHandler(agent.askQuestionService));
  commandBus.register("manage-todos", new ManageTodosHandler(
    agent.conversationTodos,
    (conversationId, items) => {
      void eventDispatcher.publish(createAgentTodoUpdatedEvent(conversationId, items));
    },
  ));
  commandBus.register("kill-tool-job", new KillToolJobHandler(agent.asyncToolRuntime));
  commandBus.register("add-job", new AddJobHandler(jobs.jobStore));
  commandBus.register("update-job", new UpdateJobHandler(jobs.jobStore));
  commandBus.register("set-job-enabled", new SetJobEnabledHandler(jobs.jobStore));
  commandBus.register("run-job-now", new RunJobNowHandler(jobs.jobScheduler));
  commandBus.register("cancel-job", new CancelJobHandler(jobs.jobScheduler));
  commandBus.register("remove-job", new RemoveJobHandler(jobs.jobStore));
  if (jobs.pipelineStore && jobs.pipelineScheduler) {
    commandBus.register("add-pipeline", new AddPipelineHandler(jobs.pipelineStore));
    commandBus.register("update-pipeline", new UpdatePipelineHandler(jobs.pipelineStore));
    commandBus.register("remove-pipeline", new RemovePipelineHandler(jobs.pipelineStore));
    commandBus.register("run-pipeline", new RunPipelineHandler(jobs.pipelineScheduler));
  }
  commandBus.register("run-acp-turn", new RunAcpTurnHandler(acp.acpSessionService));
  commandBus.register("cancel-acp-turn", new CancelAcpTurnHandler(acp.acpSessionService));
  commandBus.register("answer-acp-permission", new AnswerAcpPermissionHandler(acp.acpPermissionService));
  commandBus.register("answer-acp-ask", new AnswerAcpAskHandler(acp.acpAskService));
  commandBus.register("set-acp-config-option", new SetAcpConfigOptionHandler(acp.acpSessionService));
  commandBus.register("ensure-acp-session", new EnsureAcpSessionHandler(acp.acpSessionService));
  commandBus.register("probe-acp-provider", new ProbeAcpProviderHandler(acp.acpClient));
  commandBus.register("import-acp-models", new ImportAcpModelsHandler(acp.acpSessionService));
  commandBus.register("configure-ai", new ConfigureAiHandler(aiConfiguration));
  commandBus.register("configure-ai-runtime", new ConfigureAiRuntimeHandler(aiConfiguration));
  commandBus.register("remove-ai", new RemoveAiHandler(aiConfiguration));
  if (plugin.pluginInstaller) {
    commandBus.register("install-plugin", new InstallPluginHandler(plugin.pluginInstaller, eventDispatcher, clock));
    commandBus.register("uninstall-plugin", new UninstallPluginHandler(plugin.pluginInstaller, plugin.runtimeManager, plugin.pluginRepository, eventDispatcher, clock));
  }

  const queryBus = new QueryBus();
  queryBus.register("list-plugins", new ListPluginsHandler(plugin.runtimeManager));
  queryBus.register("get-plugin", new GetPluginHandler(plugin.runtimeManager));
  queryBus.register("get-plugin-state", new GetPluginStateHandler(plugin.runtimeManager));
  queryBus.register("list-tools", new ListToolsHandler(plugin.runtimeManager));
  queryBus.register("list-prompts", new ListPromptsHandler(plugin.runtimeManager));
  queryBus.register("get-prompt", new GetPromptHandler(plugin.runtimeManager));
  queryBus.register("list-resources", new ListResourcesHandler(plugin.runtimeManager));
  queryBus.register("list-resource-templates", new ListResourceTemplatesHandler(plugin.runtimeManager));
  queryBus.register("read-resource", new ReadResourceHandler(plugin.runtimeManager));
  queryBus.register("system-ping", new SystemPingHandler());
  const providedVersion = options.appVersion?.trim();
  let appVersion = providedVersion || "0.0.0";
  if (!providedVersion) {
    for (const candidate of [
      resolve(process.cwd(), "VERSION"),
      resolve(__dirname, "../../../../../VERSION"),
      resolve(__dirname, "../../../../VERSION"),
    ]) {
      try { appVersion = readFileSync(candidate, "utf8").trim(); break; } catch { /* try next */ }
    }
  }
  queryBus.register("system-version", new SystemVersionHandler(appVersion));
  queryBus.register("list-jobs", new ListJobsHandler(jobs.jobStore));
  queryBus.register("job-output", new JobOutputHandler(jobs.jobStore, jobs.jobFs));
  queryBus.register("validate-schedule", new ValidateScheduleHandler());
  if (jobs.pipelineStore) {
    queryBus.register("list-pipelines", new ListPipelinesHandler(jobs.pipelineStore));
  }
  queryBus.register("get-acp-session-info", new GetAcpSessionInfoHandler(acp.acpSessionService));
  queryBus.register("get-active-turn", new GetActiveTurnHandler(agent.activeTurns));
  queryBus.register("tool-job-list", new ToolJobListHandler(agent.asyncToolRuntime));

  return { commandBus, queryBus };
}
