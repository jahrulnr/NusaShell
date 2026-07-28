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
  createLogger,
  AgentProviderRegistry,
  StaticAgentProvider,
  OpenAiCompatibleAgentProvider,
  type Logger,
  type LogObserver,
} from "@nusashell/infrastructure";
import type { PluginRepositoryPort } from "@nusashell/application";
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
  ListPluginsHandler,
  GetPluginHandler,
  GetPluginStateHandler,
  CallToolHandler,
  CancelToolCallHandler,
  ListToolsHandler,
  McpAgentToolGateway,
  RunAgentTurnHandler,
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
  readonly dbPath?: string;
  readonly logLevel?: string;
  readonly loggerObserver?: LogObserver;
  readonly ai?: {
    readonly providerId: string;
    readonly model?: string;
    readonly baseUrl?: string;
    readonly apiKey?: string;
    readonly maxToolRounds: number;
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
  readonly db?: SqliteDatabase | undefined;
  readonly logger: Logger;
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
  });
  const agentToolGateway = new McpAgentToolGateway(runtimeManager);
  const agentProviders: AgentProvider[] = [new StaticAgentProvider()];
  if (options.ai?.baseUrl && options.ai.apiKey && options.ai.model) {
    agentProviders.push(new OpenAiCompatibleAgentProvider({
      baseUrl: options.ai.baseUrl,
      apiKey: options.ai.apiKey,
      model: options.ai.model,
    }));
  }
  const agentProviderRegistry = new AgentProviderRegistry(agentProviders);

  const commandBus = new CommandBus();
  commandBus.register("start-plugin", new StartPluginHandler(runtimeManager));
  commandBus.register("stop-plugin", new StopPluginHandler(runtimeManager));
  commandBus.register("restart-plugin", new RestartPluginHandler(runtimeManager));
  commandBus.register("call-tool", new CallToolHandler(runtimeManager));
  commandBus.register("cancel-tool-call", new CancelToolCallHandler(runtimeManager));
  commandBus.register("run-agent-turn", new RunAgentTurnHandler(
    agentProviderRegistry,
    agentToolGateway,
    options.ai?.providerId ?? "stub",
    options.ai?.maxToolRounds ?? 8,
    logger,
  ));
  if (pluginInstaller) {
    commandBus.register("install-plugin", new InstallPluginHandler(pluginInstaller, eventDispatcher, clock));
    commandBus.register("uninstall-plugin", new UninstallPluginHandler(pluginInstaller, eventDispatcher, clock));
  }

  const queryBus = new QueryBus();
  queryBus.register("list-plugins", new ListPluginsHandler(runtimeManager));
  queryBus.register("get-plugin", new GetPluginHandler(runtimeManager));
  queryBus.register("get-plugin-state", new GetPluginStateHandler(runtimeManager));
  queryBus.register("list-tools", new ListToolsHandler(runtimeManager));
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
    db,
    logger,
  };
}
