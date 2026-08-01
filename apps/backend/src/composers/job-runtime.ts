import {
  FilesystemJobFs,
  SqliteJobStore,
  JsonJobStore,
  type Logger,
} from "@nusashell/infrastructure";
import {
  JobAgentToolGateway,
  JobAgentExecutor,
  JobScheduler,
  CallToolHandler,
  DEFAULT_JOB_EXECUTOR_SETTINGS,
  type EventDispatcher,
  type JobStorePort,
  type JobFsPort,
} from "@nusashell/application";
import { fileURLToPath } from "node:url";
import type { ContainerOptions } from "../container.js";
import type { PluginRuntimeParts } from "./plugin-runtime.js";
import type { AgentRuntimeParts } from "./agent-runtime.js";

export interface JobRuntimeParts {
  readonly jobScheduler: JobScheduler;
  readonly jobStore: JobStorePort;
  readonly jobFs: JobFsPort;
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
  return { jobScheduler, jobStore, jobFs };
}
