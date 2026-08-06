import {
  FilesystemJobFs,
  SqliteJobStore,
  JsonJobStore,
  JsonPipelineStore,
  type Logger,
} from "@nusashell/infrastructure";
import {
  JobAgentToolGateway,
  JobAgentExecutor,
  JobScheduler,
  EventJobMatcher,
  PipelineScheduler,
  PipelineTriggerCoordinator,
  CallToolHandler,
  DEFAULT_JOB_EXECUTOR_SETTINGS,
  type EventDispatcher,
  type JobStorePort,
  type JobFsPort,
  type PipelineStorePort,
} from "@nusashell/application";
import { fileURLToPath } from "node:url";
import type { ContainerOptions } from "../container.js";
import type { PluginRuntimeParts } from "./plugin-runtime.js";
import type { AgentRuntimeParts } from "./agent-runtime.js";

export interface JobRuntimeParts {
  readonly jobScheduler: JobScheduler;
  readonly jobStore: JobStorePort;
  readonly jobFs: JobFsPort;
  readonly eventJobMatcher: EventJobMatcher;
  readonly pipelineStore?: PipelineStorePort;
  readonly pipelineScheduler?: PipelineScheduler;
  readonly pipelineTriggerCoordinator?: PipelineTriggerCoordinator;
}

export function createJobRuntime(
  options: ContainerOptions,
  logger: Logger,
  eventDispatcher: EventDispatcher,
  plugin: PluginRuntimeParts,
  agent: AgentRuntimeParts,
): JobRuntimeParts {
  const jobsRoot = options.jobsRoot ?? fileURLToPath(new URL("../../../.nusashell/agent/jobs", import.meta.url));
  let jobStore: JobStorePort;
  if (plugin.db) {
    jobStore = new SqliteJobStore(plugin.db);
  } else {
    jobStore = new JsonJobStore(jobsRoot);
  }
  const jobToolGateway = new JobAgentToolGateway(agent.agentToolGateway);
  const jobExecutor = new JobAgentExecutor({
    providerRegistry: agent.agentProviderRegistry,
    toolGateway: jobToolGateway,
    defaultProviderId: options.ai?.providerId || (options.ai?.stubEnabled ? "stub" : ""),
    logger,
  });
  const jobFs: JobFsPort = new FilesystemJobFs(jobsRoot);
  // Resolve job/pipeline executor settings from config instead of the
  // hardcoded default (maxToolRounds: 8) so scheduled/pipeline turns follow
  // the same ceiling as interactive agent turns:
  //   NUSASHELL_JOB_MAX_TOOL_ROUNDS → options.ai.maxToolRounds → default 8.
  const executorSettings: typeof DEFAULT_JOB_EXECUTOR_SETTINGS = {
    ...DEFAULT_JOB_EXECUTOR_SETTINGS,
    ...(options.ai
      ? {
          maxToolRounds: options.ai.jobMaxToolRounds ?? options.ai.maxToolRounds,
          ...(options.ai.maxRepeatedToolCalls !== undefined
            ? { maxRepeatedToolCalls: options.ai.maxRepeatedToolCalls }
            : {}),
          ...(options.ai.strategy !== undefined ? { strategy: options.ai.strategy } : {}),
          ...(options.ai.totalAttemptBudget !== undefined
            ? { totalAttemptBudget: options.ai.totalAttemptBudget }
            : {}),
        }
      : {}),
  };
  const jobScheduler = new JobScheduler({
    store: jobStore,
    executor: jobExecutor,
    callToolHandler: new CallToolHandler(plugin.runtimeManager),
    eventDispatcher,
    jobFs,
    executorSettings,
    logger,
  });
  if (options.jobs) {
    jobScheduler.configure(options.jobs);
  }
  const pipelineStore = new JsonPipelineStore(jobsRoot);
  const pipelineScheduler = new PipelineScheduler({
    store: pipelineStore,
    executor: jobExecutor,
    callToolHandler: new CallToolHandler(plugin.runtimeManager),
    eventDispatcher,
    executorSettings,
    logger,
  });
  void pipelineScheduler.recoverOnStartup().catch((err) => {
    logger.error(
      "pipeline recovery failed: %s",
      err instanceof Error ? err.message : String(err),
    );
  });
  const pipelineTriggerCoordinator = new PipelineTriggerCoordinator({
    store: pipelineStore,
    scheduler: pipelineScheduler,
    eventDispatcher,
    logger,
  });
  if (options.jobs) {
    pipelineTriggerCoordinator.configure({
      enabled: options.jobs.enabled,
      tickSeconds: options.jobs.tickSeconds,
    });
  }
  const eventJobMatcher = new EventJobMatcher({
    store: jobStore,
    scheduler: jobScheduler,
    eventDispatcher,
    logger,
    pipelineStore,
    pipelineScheduler,
  });
  eventJobMatcher.start();
  return {
    jobScheduler,
    jobStore,
    jobFs,
    eventJobMatcher,
    pipelineStore,
    pipelineScheduler,
    pipelineTriggerCoordinator,
  };
}
