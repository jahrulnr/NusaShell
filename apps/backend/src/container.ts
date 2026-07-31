import {
  SystemClock,
  InMemoryPluginRepository,
  NodeChildProcessAdapter,
  McpClientFactory,
  FilesystemPluginRegistry,
  SqliteDatabase,
  SqlitePluginRepository,
  PluginInstaller,
  PluginSyncService,
  FilesystemPromptLoader,
  FilesystemReviewStateStore,
  MarkdownDocsIndex,
  FilesystemSkillRegistry,
  FilesystemSkillProvenance,
  FilesystemSkillUsage,
  SkillApprovalStaging,
  type PendingSkillWrite,
  FilesystemMemoryStore,
  createLogger,
  AgentProviderRegistry,
  StaticAgentProvider,
  OpenAiCompatibleAgentProvider,
  SqliteJobStore,
  JsonJobStore,
  type Logger,
  type LogObserver,
} from "@nusashell/infrastructure";
import type { PluginRepositoryPort, SkillRegistryPort, SkillProvenancePort, SkillUsagePort, CuratorSettings, MemoryStorePort } from "@nusashell/application";
import {
  CommandBus,
  QueryBus,
  EventDispatcher,
  PluginRuntimeManager,
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
  McpAgentToolGateway,
  ReviewAgentToolGateway,
  AgentTurnRunner,
  InProcessAgentTurnWorker,
  BackgroundReviewScheduler,
  DEFAULT_REVIEW_SETTINGS,
  type BackgroundReviewSettings,
  SkillCuratorService,
  SkillCuratorScheduler,
  DEFAULT_CURATOR_SETTINGS,
  DEFAULT_SCHEDULER_SETTINGS,
  LearningGraphService,
  RunAgentTurnHandler,
  CancelAgentTurnHandler,
  AnswerAskQuestionHandler,
  AskQuestionService,
  AgentTurnCoordinator,
  JobAgentToolGateway,
  JobAgentExecutor,
  JobScheduler,
  DEFAULT_JOB_EXECUTOR_SETTINGS,
  AddJobHandler,
  SetJobEnabledHandler,
  RunJobNowHandler,
  RemoveJobHandler,
  ListJobsHandler,
  JobOutputHandler,
  ValidateScheduleHandler,
  type JobStorePort,
  type JobSchedulerSettings,
  type AgentRuntimeSettings,
  createAgentTextDeltaEvent,
  createAgentReasoningDeltaEvent,
  createAgentToolCallStartEvent,
  createAgentToolCallEndEvent,
  createAgentContextUpdateEvent,
  type AgentProvider,
  SystemPingHandler,
  SystemVersionHandler,
} from "@nusashell/application";
import {
  MessageRouter,
  WebSocketServer,
  WebSocketEventPublisher,
} from "@nusashell/transport-ws";

export interface ContainerOptions {
  readonly port: number;
  readonly host?: string;
  readonly pluginsRoot?: string;
  readonly promptsRoot?: string;
  readonly docsRoot?: string;
  readonly docsIndexStorageRoot?: string;
  readonly skillsRoot?: string;
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
  readonly skillRegistry: SkillRegistryPort;
  readonly skillProvenance: SkillProvenancePort;
  readonly skillUsage: SkillUsagePort;
  readonly skillApprovalStaging: SkillApprovalStaging;
  readonly skillCurator: SkillCuratorService;
  readonly skillCuratorScheduler: SkillCuratorScheduler;
  readonly backgroundReviewScheduler: BackgroundReviewScheduler;
  readonly jobScheduler: JobScheduler;
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

  let pluginRepository: PluginRepositoryPort;
  let db: SqliteDatabase | undefined;

  if (options.dbPath) {
    db = new SqliteDatabase(options.dbPath);
    pluginRepository = new SqlitePluginRepository(db);
    // Sync filesystem plugins into SQLite so bundled plugins are registered
    if (options.pluginsRoot) {
      const syncService = new PluginSyncService(options.pluginsRoot, pluginRepository, logger);
      syncService.sync().catch((err) => {
        logger.warn({ err }, "Plugin sync failed during startup");
      });
    }
  } else if (options.pluginsRoot) {
    pluginRepository = new FilesystemPluginRegistry(options.pluginsRoot, logger);
  } else {
    pluginRepository = new InMemoryPluginRepository();
  }

  const processAdapter = new NodeChildProcessAdapter(logger);
  const mcpClientFactory = new McpClientFactory(logger);

  const eventDispatcher = new EventDispatcher();
  const aiRuntime: AgentRuntimeSettings & { stream: boolean; vision: "auto" | "on" | "off"; userPrompt: string } = {
    strategy: options.ai?.strategy ?? "failover" as "failover" | "round-robin" | "switch",
    totalAttemptBudget: options.ai?.totalAttemptBudget ?? 4,
    stream: options.ai?.stream ?? true,
    vision: options.ai?.vision ?? "auto" as "auto" | "on" | "off",
    userPrompt: options.ai?.userPrompt ?? "",
    maxToolRounds: options.ai?.maxToolRounds ?? 50,
    maxRepeatedToolCalls: options.ai?.maxRepeatedToolCalls ?? 50,
    ...(options.ai?.context ? { context: options.ai.context } : {}),
  };

  const pluginInstaller = options.pluginsRoot
    ? new PluginInstaller(options.pluginsRoot, logger)
    : null;

  const runtimeManager = new PluginRuntimeManager({
    pluginRepository,
    processAdapter,
    mcpClientFactory,
    eventDispatcher,
    clock,
    logger,
    ...(options.resolvePluginRuntimeEnvironment
      ? { resolveRuntimeEnvironment: options.resolvePluginRuntimeEnvironment }
      : {}),
  });
  const docsRoot = options.docsRoot ?? new URL("../../../resources/agent/docs", import.meta.url).pathname;
  const docsIndexStorageRoot = options.docsIndexStorageRoot ?? new URL("../../../.nusashell/agent/docs-index", import.meta.url).pathname;
  const docsIndex = new MarkdownDocsIndex(docsRoot, docsIndexStorageRoot);
  void docsIndex.reindex().catch((err) => {
    logger.warn({ err }, "Docs index initial build failed; will retry on demand");
  });

  const skillsRoot = options.skillsRoot ?? new URL("../../../.nusashell/agent/skills", import.meta.url).pathname;
  const skillRegistry = new FilesystemSkillRegistry(skillsRoot);
  const skillProvenance = new FilesystemSkillProvenance(skillsRoot);
  const skillUsage = new FilesystemSkillUsage(skillsRoot);
  const skillApprovalStaging = new SkillApprovalStaging(skillsRoot);
  const skillCurator = new SkillCuratorService({
    registry: skillRegistry,
    provenance: skillProvenance,
    usage: skillUsage,
    eventDispatcher,
    logger,
  });
  const skillCuratorScheduler = new SkillCuratorScheduler({
    curator: skillCurator,
    stateRoot: skillsRoot,
    logger,
  });
  void skillCuratorScheduler.initialize().catch((err) => {
    logger.warn({ err }, "Skill curator scheduler initialization failed");
  });
  const memoryRoot = options.memoryRoot ?? new URL("../../../.nusashell/agent/memory", import.meta.url).pathname;
  const memoryStore = new FilesystemMemoryStore(memoryRoot);
  const learningGraph = new LearningGraphService({
    registry: skillRegistry,
    usage: skillUsage,
    provenance: skillProvenance,
    memoryStore,
  });
  const askQuestionService = new AskQuestionService();
  const agentToolGateway = new McpAgentToolGateway(runtimeManager, docsIndex, skillRegistry, logger, memoryStore, skillProvenance, skillApprovalStaging, skillUsage, askQuestionService);
  const promptLoader = new FilesystemPromptLoader(
    options.promptsRoot ?? new URL("../../../resources/agent/prompts", import.meta.url).pathname,
  );
  const agentProviders: AgentProvider[] = options.ai?.stubEnabled ? [new StaticAgentProvider()] : [];
  if (options.ai?.baseUrl) {
    agentProviders.push(new OpenAiCompatibleAgentProvider({
      id: options.ai.providerId,
      ...(options.ai.api ? { api: options.ai.api } : {}),
      baseUrl: options.ai.baseUrl,
      ...(options.ai.apiKey ? { apiKey: options.ai.apiKey } : {}),
      ...(options.ai.model ? { model: options.ai.model } : {}),
      logger,
      ...(options.ai.retry ? { retry: {
        ...options.ai.retry,
        onRetry: (event) => {
          logger.warn("AI provider retry provider=%s attempt=%d delayMs=%d status=%d kind=%s", event.providerId, event.attempt, event.delayMs, event.status, event.kind);
        },
      } } : {}),
      stream: aiRuntime.stream,
      vision: aiRuntime.vision,
      ...(options.ai.timeoutMs !== undefined ? { timeoutMs: options.ai.timeoutMs } : {}),
    }));
  }
  const agentProviderRegistry = new AgentProviderRegistry(agentProviders);
  const agentTurnCoordinator = new AgentTurnCoordinator();
  const reviewGateway = new ReviewAgentToolGateway(agentToolGateway);
  const reviewStateStore = new FilesystemReviewStateStore(memoryRoot);
  const backgroundReviewScheduler = new BackgroundReviewScheduler({
    stateStore: reviewStateStore,
    promptLoader,
    providerRegistry: agentProviderRegistry,
    reviewGateway,
    runnerFactory: ({ provider, toolGateway, maxToolRounds }) => {
      const worker = new InProcessAgentTurnWorker(provider, toolGateway, logger);
      return new AgentTurnRunner(worker, { maxToolRounds });
    },
    defaultProviderId: options.ai?.providerId || (options.ai?.stubEnabled ? "stub" : ""),
    eventDispatcher,
    logger,
  });
  if (options.backgroundReview) {
    backgroundReviewScheduler.configure(options.backgroundReview);
  }

  // ---- Job automation waist ----
  const jobsRoot = options.jobsRoot ?? new URL("../../../.nusashell/agent/jobs", import.meta.url).pathname;
  let jobStore: JobStorePort;
  if (db) {
    jobStore = new SqliteJobStore(db);
  } else {
    jobStore = new JsonJobStore(jobsRoot);
  }
  const jobToolGateway = new JobAgentToolGateway(agentToolGateway);
  const jobExecutor = new JobAgentExecutor({
    providerRegistry: agentProviderRegistry,
    toolGateway: jobToolGateway,
    defaultProviderId: options.ai?.providerId || (options.ai?.stubEnabled ? "stub" : ""),
    logger,
  });
  const jobScheduler = new JobScheduler({
    store: jobStore,
    executor: jobExecutor,
    callToolHandler: new CallToolHandler(runtimeManager),
    eventDispatcher,
    jobsRoot,
    executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS,
    logger,
  });
  if (options.jobs) {
    jobScheduler.configure(options.jobs);
  }

  const commandBus = new CommandBus();
  commandBus.register("start-plugin", new StartPluginHandler(runtimeManager));
  commandBus.register("stop-plugin", new StopPluginHandler(runtimeManager));
  commandBus.register("restart-plugin", new RestartPluginHandler(runtimeManager));
  commandBus.register("call-tool", new CallToolHandler(runtimeManager));
  commandBus.register("cancel-tool-call", new CancelToolCallHandler(runtimeManager));
  commandBus.register("set-plugin-autostart", new SetPluginAutostartHandler(runtimeManager));
  commandBus.register("run-agent-turn", new RunAgentTurnHandler(
    agentProviderRegistry,
    agentToolGateway,
    options.ai?.providerId || (options.ai?.stubEnabled ? "stub" : ""),
    aiRuntime,
    logger,
    agentTurnCoordinator,
    (traceId, delta) => {
      void eventDispatcher.publish(createAgentTextDeltaEvent(traceId, delta));
    },
    (traceId, delta) => {
      void eventDispatcher.publish(createAgentReasoningDeltaEvent(traceId, delta));
    },
    (traceId, call) => {
      void eventDispatcher.publish(createAgentToolCallStartEvent(traceId, call));
    },
    (traceId, execution) => {
      void eventDispatcher.publish(createAgentToolCallEndEvent(traceId, execution));
    },
    (traceId, update) => {
      void eventDispatcher.publish(createAgentContextUpdateEvent(traceId, update.estimatedTokens, update.usage));
    },
    promptLoader,
    aiRuntime.userPrompt,
    memoryStore,
    (result) => { void backgroundReviewScheduler.tick(result); void skillCuratorScheduler.tick(); },
  ));
  commandBus.register("cancel-agent-turn", new CancelAgentTurnHandler(agentTurnCoordinator));
  commandBus.register("answer-ask-question", new AnswerAskQuestionHandler(askQuestionService));
  commandBus.register("add-job", new AddJobHandler(jobStore));
  commandBus.register("set-job-enabled", new SetJobEnabledHandler(jobStore));
  commandBus.register("run-job-now", new RunJobNowHandler(jobScheduler));
  commandBus.register("remove-job", new RemoveJobHandler(jobStore));
  if (pluginInstaller) {
    commandBus.register("install-plugin", new InstallPluginHandler(pluginInstaller, eventDispatcher, clock));
    commandBus.register("uninstall-plugin", new UninstallPluginHandler(pluginInstaller, runtimeManager, pluginRepository, eventDispatcher, clock));
  }

  const queryBus = new QueryBus();
  queryBus.register("list-plugins", new ListPluginsHandler(runtimeManager));
  queryBus.register("get-plugin", new GetPluginHandler(runtimeManager));
  queryBus.register("get-plugin-state", new GetPluginStateHandler(runtimeManager));
  queryBus.register("list-tools", new ListToolsHandler(runtimeManager));
  queryBus.register("list-prompts", new ListPromptsHandler(runtimeManager));
  queryBus.register("get-prompt", new GetPromptHandler(runtimeManager));
  queryBus.register("list-resources", new ListResourcesHandler(runtimeManager));
  queryBus.register("list-resource-templates", new ListResourceTemplatesHandler(runtimeManager));
  queryBus.register("read-resource", new ReadResourceHandler(runtimeManager));
  queryBus.register("system-ping", new SystemPingHandler());
  queryBus.register("system-version", new SystemVersionHandler());
  queryBus.register("list-jobs", new ListJobsHandler(jobStore));
  queryBus.register("job-output", new JobOutputHandler(jobStore));
  queryBus.register("validate-schedule", new ValidateScheduleHandler());

  const router = new MessageRouter({ commandBus, queryBus, logger });

  const wsServer = new WebSocketServer(router, {
    port: options.port,
    host: options.host ?? "0.0.0.0",
    logger,
  });

  const eventPublisher = new WebSocketEventPublisher(wsServer.sessionRegistry, wsServer.subscriptionRegistry);
  eventDispatcher.onAny(eventPublisher);

  return {
    commandBus,
    queryBus,
    eventDispatcher,
    runtimeManager,
    router,
    wsServer,
    eventPublisher,
    pluginRepository,
    skillRegistry,
    skillProvenance,
    skillUsage,
    skillApprovalStaging,
    skillCurator,
    skillCuratorScheduler,
    backgroundReviewScheduler,
    jobScheduler,
    learningGraph,
    memoryStore,
    db,
    logger,
    configureAi(settings) {
      if (!settings.baseUrl) throw new Error("OpenAI-compatible provider requires a base URL");
      agentProviderRegistry.set(new OpenAiCompatibleAgentProvider({
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
        stream: aiRuntime.stream,
        vision: aiRuntime.vision,
        ...(settings.timeoutMs !== undefined
          ? { timeoutMs: settings.timeoutMs }
          : options.ai?.timeoutMs !== undefined
            ? { timeoutMs: options.ai.timeoutMs }
            : {}),
      }));
    },
    removeAi(providerId) {
      agentProviderRegistry.delete(providerId);
    },
    configureBackgroundReview(settings) {
      backgroundReviewScheduler.configure(settings);
    },
    configureCurator(settings: Partial<CuratorSettings>) {
      skillCurator.configure(settings);
    },
    configureCuratorScheduler(settings: Partial<{ enabled: boolean; intervalHours: number; paused: boolean }>) {
      skillCuratorScheduler.configure(settings);
    },
    configureJobScheduler(settings: Partial<JobSchedulerSettings>) {
      jobScheduler.configure(settings);
    },
    configureAiRuntime(settings) {
      aiRuntime.strategy = settings.strategy;
      aiRuntime.totalAttemptBudget = settings.totalAttemptBudget;
      aiRuntime.stream = settings.stream;
      aiRuntime.vision = settings.vision;
      aiRuntime.userPrompt = settings.userPrompt;
      if (typeof settings.maxToolRounds === "number") aiRuntime.maxToolRounds = settings.maxToolRounds;
      if (typeof settings.maxRepeatedToolCalls === "number") aiRuntime.maxRepeatedToolCalls = settings.maxRepeatedToolCalls;
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
