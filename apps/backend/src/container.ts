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
  MarkdownDocsIndex,
  FilesystemSkillRegistry,
  createLogger,
  AgentProviderRegistry,
  StaticAgentProvider,
  OpenAiCompatibleAgentProvider,
  type Logger,
  type LogObserver,
} from "@nusashell/infrastructure";
import type { PluginRepositoryPort, SkillRegistryPort } from "@nusashell/application";
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
  RunAgentTurnHandler,
  CancelAgentTurnHandler,
  AgentTurnCoordinator,
  createAgentTextDeltaEvent,
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
  readonly dbPath?: string;
  readonly logLevel?: string;
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
    readonly strategy?: "failover" | "round-robin" | "switch";
    readonly totalAttemptBudget?: number;
    readonly stream?: boolean;
    readonly vision?: "auto" | "on" | "off";
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
  }): void;
  removeAi(providerId: string): void;
}

export function createContainer(options: ContainerOptions): Container {
  const clock = new SystemClock();
  const logger = createLogger(options.logLevel ?? "info", options.loggerObserver);

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
  const aiRuntime = {
    strategy: options.ai?.strategy ?? "failover" as "failover" | "round-robin" | "switch",
    totalAttemptBudget: options.ai?.totalAttemptBudget ?? 4,
    stream: options.ai?.stream ?? true,
    vision: options.ai?.vision ?? "auto" as "auto" | "on" | "off",
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
  const agentToolGateway = new McpAgentToolGateway(runtimeManager, docsIndex, skillRegistry);
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
      ...(options.ai.retry ? { retry: options.ai.retry } : {}),
      stream: aiRuntime.stream,
      vision: aiRuntime.vision,
      ...(options.ai.timeoutMs !== undefined ? { timeoutMs: options.ai.timeoutMs } : {}),
    }));
  }
  const agentProviderRegistry = new AgentProviderRegistry(agentProviders);
  const agentTurnCoordinator = new AgentTurnCoordinator();

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
    options.ai?.maxToolRounds ?? 50,
    logger,
    options.ai?.context,
    aiRuntime,
    agentTurnCoordinator,
    (traceId, delta) => {
      void eventDispatcher.publish(createAgentTextDeltaEvent(traceId, delta));
    },
    promptLoader,
  ));
  commandBus.register("cancel-agent-turn", new CancelAgentTurnHandler(agentTurnCoordinator));
  if (pluginInstaller) {
    commandBus.register("install-plugin", new InstallPluginHandler(pluginInstaller, eventDispatcher, clock));
    commandBus.register("uninstall-plugin", new UninstallPluginHandler(pluginInstaller, eventDispatcher, clock));
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

  const router = new MessageRouter({ commandBus, queryBus });

  const wsServer = new WebSocketServer(router, {
    port: options.port,
    host: options.host ?? "0.0.0.0",
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
        ...(options.ai?.retry ? {
          retry: {
            ...options.ai.retry,
            attemptBudget: settings.maxAttempts ?? options.ai.retry.attemptBudget,
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
    configureAiRuntime(settings) {
      aiRuntime.strategy = settings.strategy;
      aiRuntime.totalAttemptBudget = settings.totalAttemptBudget;
      aiRuntime.stream = settings.stream;
      aiRuntime.vision = settings.vision;
    },
  };
}
