import {
  SystemClock,
  createLogger,
  OpenAiCompatibleAgentProvider,
  type Logger,
  type LogObserver,
  type SkillApprovalStaging,
  type SqliteDatabase,
} from "@nusashell/infrastructure";
import {
  EventDispatcher,
  type PluginRepositoryPort,
  type SkillRegistryPort,
  type SkillProvenancePort,
  type SkillUsagePort,
  type CuratorSettings,
  type MemoryStorePort,
  type CommandBus,
  type QueryBus,
  type PluginRuntimeManager,
  type BackgroundReviewScheduler,
  type SkillCuratorService,
  type SkillCuratorScheduler,
  type JobScheduler,
  type EventJobMatcher,
  type LearningGraphService,
  type BackgroundReviewSettings,
  type JobSchedulerSettings,
  type AiConfigurationPort,
} from "@nusashell/application";
import {
  MessageRouter,
  WebSocketServer,
  WebSocketEventPublisher,
} from "@nusashell/transport-ws";
import {
  createPluginRuntime,
  createSkillsRuntime,
  createAgentRuntime,
  createJobRuntime,
  createAcpRuntime,
  registerBuses,
  createTransport,
  type AgentRuntimeParts,
} from "./composers/index.js";

export interface ContainerOptions {
  readonly port: number;
  readonly host?: string;
  readonly pluginsRoot?: string;
  readonly bundledPluginsRoot?: string;
  readonly userPluginsRoot?: string;
  readonly promptsRoot?: string;
  readonly docsRoot?: string;
  readonly docsIndexStorageRoot?: string;
  readonly skillsRoot?: string;
  readonly builtinSkillsRoot?: string;
  readonly memoryRoot?: string;
  readonly jobsRoot?: string;
  readonly dbPath?: string;
  readonly logLevel?: string;
  readonly logFile?: string;
  readonly loggerObserver?: LogObserver;
  readonly resolvePluginRuntimeEnvironment?: (
    pluginId: string,
  ) => Promise<Readonly<Record<string, string>>> | Readonly<Record<string, string>>;
  readonly ai?: {
    readonly providerId: string;
    readonly stubEnabled?: boolean;
    readonly api?: "chat" | "responses" | "messages";
    readonly model?: string;
    readonly baseUrl?: string;
    readonly apiKey?: string;
    readonly maxToolRounds: number;
    readonly maxRepeatedToolCalls?: number;
    readonly softRecoverAttempts?: number;
    readonly maxConcurrentToolCalls?: number;
    readonly strategy?: "failover" | "round-robin" | "switch";
    readonly totalAttemptBudget?: number;
    readonly stream?: boolean;
    readonly vision?: "auto" | "on" | "off";
    readonly userPrompt?: string;
    readonly timeoutMs?: number;
    readonly retry?: {
      readonly attemptBudget: number;
      readonly baseDelayMs: number;
      readonly maxDelayMs: number;
      readonly jitter: number;
    };
    readonly context?: {
      readonly compactionEnabled: boolean;
      readonly maxInputTokens: number;
      readonly reserveTokens: number;
      readonly recentTurns: number;
      readonly summaryMaxChars: number;
    };
  };
  readonly backgroundReview?: Partial<BackgroundReviewSettings>;
  readonly jobs?: Partial<JobSchedulerSettings>;
}

export interface Container {
  readonly commandBus: CommandBus;
  readonly queryBus: QueryBus;
  readonly eventDispatcher: EventDispatcher;
  readonly runtimeManager: PluginRuntimeManager;
  readonly router: MessageRouter;
  readonly wsServer: WebSocketServer;
  readonly eventPublisher: WebSocketEventPublisher;
  readonly pluginRepository: PluginRepositoryPort;
  readonly syncPlugins: () => Promise<void>;
  readonly skillRegistry: SkillRegistryPort;
  readonly skillProvenance: SkillProvenancePort;
  readonly skillUsage: SkillUsagePort;
  readonly skillApprovalStaging: SkillApprovalStaging;
  readonly skillCurator: SkillCuratorService;
  readonly skillCuratorScheduler: SkillCuratorScheduler;
  readonly backgroundReviewScheduler: BackgroundReviewScheduler;
  readonly jobScheduler: JobScheduler;
  readonly eventJobMatcher: EventJobMatcher;
  readonly learningGraph: LearningGraphService;
  readonly memoryStore: MemoryStorePort;
  readonly db?: SqliteDatabase | undefined;
  readonly logger: Logger;
  configureAi(settings: {
    providerId: string;
    api?: "chat" | "responses" | "messages";
    model?: string;
    baseUrl?: string;
    apiKey?: string;
    timeoutMs?: number;
    maxAttempts?: number;
  }): void;
  configureAiRuntime(settings: {
    strategy: "failover" | "round-robin" | "switch";
    totalAttemptBudget: number;
    stream: boolean;
    vision: "auto" | "on" | "off";
    userPrompt: string;
    maxToolRounds?: number;
    maxRepeatedToolCalls?: number;
    softRecoverAttempts?: number;
    maxConcurrentToolCalls?: number;
    compactionEnabled?: boolean;
    maxInputTokens?: number;
    reserveTokens?: number;
    recentTurns?: number;
    summaryMaxChars?: number;
  }): void;
  removeAi(providerId: string): void;
  configureBackgroundReview(settings: Partial<BackgroundReviewSettings>): void;
  configureCurator(settings: Partial<CuratorSettings>): void;
  configureCuratorScheduler(settings: Partial<{ enabled: boolean; intervalHours: number; paused: boolean }>): void;
  configureJobScheduler(settings: Partial<JobSchedulerSettings>): void;
}

export function createContainer(options: ContainerOptions): Container {
  const clock = new SystemClock();
  const logger = createLogger({
    level: options.logLevel ?? "info",
    ...(options.loggerObserver ? { observer: options.loggerObserver } : {}),
    ...(options.logFile ? { logFile: options.logFile } : {}),
  });

  const eventDispatcher = new EventDispatcher();

  const plugin = createPluginRuntime(options, logger, eventDispatcher, clock);
  const skills = createSkillsRuntime(options, logger, eventDispatcher);
  const agent = createAgentRuntime(options, logger, eventDispatcher, plugin, skills);
  const jobs = createJobRuntime(options, logger, eventDispatcher, plugin, agent);
  agent.agentToolGateway.bindJobs(jobs.jobStore, jobs.jobScheduler);
  if (jobs.pipelineStore && jobs.pipelineScheduler) {
    agent.agentToolGateway.bindPipelines(jobs.pipelineStore, jobs.pipelineScheduler);
  }
  const acp = createAcpRuntime(options, logger, eventDispatcher, agent);

  const aiConfiguration = createAiConfiguration(options, logger, agent);
  const buses = registerBuses(options, logger, eventDispatcher, clock, plugin, skills, agent, jobs, acp, aiConfiguration);
  if (plugin.pluginInstaller && options.userPluginsRoot) {
    agent.agentToolGateway.bindPluginRegistration({
      installer: plugin.pluginInstaller,
      repository: plugin.pluginRepository,
      runtimeManager: plugin.runtimeManager,
      syncPlugins: plugin.syncPlugins,
      userPluginsRoot: options.userPluginsRoot,
      ...(options.bundledPluginsRoot ? { bundledPluginsRoot: options.bundledPluginsRoot } : {}),
      askQuestions: agent.askQuestionService,
    });
  }
  const transport = createTransport(options, logger, eventDispatcher, buses);

  return {
    commandBus: buses.commandBus,
    queryBus: buses.queryBus,
    eventDispatcher,
    runtimeManager: plugin.runtimeManager,
    router: transport.router,
    wsServer: transport.wsServer,
    eventPublisher: transport.eventPublisher,
    pluginRepository: plugin.pluginRepository,
    syncPlugins: plugin.syncPlugins,
    skillRegistry: skills.skillRegistry,
    skillProvenance: skills.skillProvenance,
    skillUsage: skills.skillUsage,
    skillApprovalStaging: skills.skillApprovalStaging,
    skillCurator: skills.skillCurator,
    skillCuratorScheduler: skills.skillCuratorScheduler,
    backgroundReviewScheduler: agent.backgroundReviewScheduler,
    jobScheduler: jobs.jobScheduler,
    eventJobMatcher: jobs.eventJobMatcher,
    learningGraph: skills.learningGraph,
    memoryStore: skills.memoryStore,
    db: plugin.db,
    logger,
    configureAi: (settings) => aiConfiguration.configureAi(settings),
    removeAi: (providerId) => aiConfiguration.removeAi(providerId),
    configureAiRuntime: (settings) => aiConfiguration.configureAiRuntime(settings),
    configureBackgroundReview(settings) {
      agent.backgroundReviewScheduler.configure(settings);
    },
    configureCurator(settings: Partial<CuratorSettings>) {
      skills.skillCurator.configure(settings);
    },
    configureCuratorScheduler(settings: Partial<{ enabled: boolean; intervalHours: number; paused: boolean }>) {
      skills.skillCuratorScheduler.configure(settings);
    },
    configureJobScheduler(settings: Partial<JobSchedulerSettings>) {
      jobs.jobScheduler.configure(settings);
    },
  };
}

function createAiConfiguration(
  options: ContainerOptions,
  logger: Logger,
  agent: AgentRuntimeParts,
): AiConfigurationPort {
  return {
    configureAi(settings) {
      if (!settings.baseUrl) throw new Error("OpenAI-compatible provider requires a base URL");
      agent.agentProviderRegistry.set(new OpenAiCompatibleAgentProvider({
        id: settings.providerId,
        ...(settings.api ? { api: settings.api } : {}),
        baseUrl: settings.baseUrl,
        ...(settings.apiKey ? { apiKey: settings.apiKey } : {}),
        ...(settings.model ? { model: settings.model } : {}),
        logger,
        ...(options.ai?.retry ? {
          retry: {
            ...options.ai.retry,
            attemptBudget: settings.maxAttempts ?? options.ai.retry.attemptBudget,
            onRetry: (event) => {
              logger.warn("AI provider retry provider=%s attempt=%d delayMs=%d status=%d kind=%s", event.providerId, event.attempt, event.delayMs, event.status, event.kind);
            },
          },
        } : {}),
        stream: agent.aiRuntime.stream,
        vision: agent.aiRuntime.vision,
        ...(settings.timeoutMs !== undefined
          ? { timeoutMs: settings.timeoutMs }
          : options.ai?.timeoutMs !== undefined
            ? { timeoutMs: options.ai.timeoutMs }
            : {}),
      }));
    },
    removeAi(providerId) {
      agent.agentProviderRegistry.delete(providerId);
    },
    configureAiRuntime(settings) {
      const aiRuntime = agent.aiRuntime;
      aiRuntime.strategy = settings.strategy;
      aiRuntime.totalAttemptBudget = settings.totalAttemptBudget;
      aiRuntime.stream = settings.stream;
      aiRuntime.vision = settings.vision;
      aiRuntime.userPrompt = settings.userPrompt;
      if (typeof settings.maxToolRounds === "number") aiRuntime.maxToolRounds = settings.maxToolRounds;
      if (typeof settings.maxRepeatedToolCalls === "number") aiRuntime.maxRepeatedToolCalls = settings.maxRepeatedToolCalls;
      if (typeof settings.softRecoverAttempts === "number") aiRuntime.softRecoverAttempts = settings.softRecoverAttempts;
      if (typeof settings.maxConcurrentToolCalls === "number") aiRuntime.maxConcurrentToolCalls = settings.maxConcurrentToolCalls;
      if (typeof settings.compactionEnabled === "boolean" || typeof settings.maxInputTokens === "number" || typeof settings.reserveTokens === "number" || typeof settings.recentTurns === "number" || typeof settings.summaryMaxChars === "number") {
        aiRuntime.context = {
          compactionEnabled: typeof settings.compactionEnabled === "boolean" ? settings.compactionEnabled : aiRuntime.context?.compactionEnabled ?? true,
          maxInputTokens: typeof settings.maxInputTokens === "number" ? settings.maxInputTokens : aiRuntime.context?.maxInputTokens ?? 12000,
          reserveTokens: typeof settings.reserveTokens === "number" ? settings.reserveTokens : aiRuntime.context?.reserveTokens ?? 3000,
          recentTurns: typeof settings.recentTurns === "number" ? settings.recentTurns : aiRuntime.context?.recentTurns ?? 4,
          summaryMaxChars: typeof settings.summaryMaxChars === "number" ? settings.summaryMaxChars : aiRuntime.context?.summaryMaxChars ?? 12000,
        };
      }
    },
  };
}
