import {
  SystemClock,
  InMemoryPluginRepository,
  NodeChildProcessAdapter,
  McpClientFactory,
  FilesystemPluginRegistry,
  SqliteDatabase,
  SqlitePluginRepository,
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
  ListPluginsHandler,
  GetPluginHandler,
  GetPluginStateHandler,
  CallToolHandler,
  CancelToolCallHandler,
  ListToolsHandler,
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
}

export function createContainer(options: ContainerOptions): Container {
  const clock = new SystemClock();

  let pluginRepository: PluginRepositoryPort;
  let db: SqliteDatabase | undefined;

  if (options.dbPath) {
    db = new SqliteDatabase(options.dbPath);
    pluginRepository = new SqlitePluginRepository(db);
  } else if (options.pluginsRoot) {
    pluginRepository = new FilesystemPluginRegistry(options.pluginsRoot);
  } else {
    pluginRepository = new InMemoryPluginRepository();
  }

  const processAdapter = new NodeChildProcessAdapter();
  const mcpClientFactory = new McpClientFactory();

  const eventDispatcher = new EventDispatcher();

  const runtimeManager = new PluginRuntimeManager({
    pluginRepository,
    processAdapter,
    mcpClientFactory,
    eventDispatcher,
    clock,
  });

  const commandBus = new CommandBus();
  commandBus.register("start-plugin", new StartPluginHandler(runtimeManager));
  commandBus.register("stop-plugin", new StopPluginHandler(runtimeManager));
  commandBus.register("restart-plugin", new RestartPluginHandler(runtimeManager));
  commandBus.register("call-tool", new CallToolHandler(runtimeManager));
  commandBus.register("cancel-tool-call", new CancelToolCallHandler(runtimeManager));

  const queryBus = new QueryBus();
  queryBus.register("list-plugins", new ListPluginsHandler(runtimeManager));
  queryBus.register("get-plugin", new GetPluginHandler(runtimeManager));
  queryBus.register("get-plugin-state", new GetPluginStateHandler(runtimeManager));
  queryBus.register("list-tools", new ListToolsHandler(runtimeManager));

  const router = new MessageRouter({ commandBus, queryBus });

  const wsServer = new WebSocketServer(router, {
    port: options.port,
    host: options.host ?? "0.0.0.0",
  });

  const eventPublisher = new WebSocketEventPublisher(wsServer.sessionRegistry);
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
  };
}
