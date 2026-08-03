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
  const jobScheduler = new JobScheduler({
    store: jobStore,
    executor: jobExecutor,
    callToolHandler: new CallToolHandler(plugin.runtimeManager),
    eventDispatcher,
    jobFs,
    executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS,
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
    executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS,
    logger,
  });
  const eventJobMatcher = new EventJobMatcher({
    store: jobStore,
    scheduler: jobScheduler,
    eventDispatcher,
    logger,
    pipelineStore,
    pipelineScheduler,
  });
  eventJobMatcher.start();
  return { jobScheduler, jobStore, jobFs, eventJobMatcher, pipelineStore, pipelineScheduler };
}
