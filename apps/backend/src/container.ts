import {
  SystemClock,
  InMemoryPluginRepository,
  NodeChildProcessAdapter,
  McpClientFactory,
  FilesystemPluginRegistry,
} from "@nusashell/infrastructure";
import {
  CommandBus,
  QueryBus,
  EventDispatcher,
  PluginRuntimeManager,
  StartPluginHandler,
  StopPluginHandler,
  ListPluginsHandler,
  CallToolHandler,
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
}

export interface Container {
  readonly commandBus: CommandBus;
  readonly queryBus: QueryBus;
  readonly eventDispatcher: EventDispatcher;
  readonly runtimeManager: PluginRuntimeManager;
  readonly router: MessageRouter;
  readonly wsServer: WebSocketServer;
  readonly eventPublisher: WebSocketEventPublisher;
  readonly pluginRepository: FilesystemPluginRegistry | InMemoryPluginRepository;
}

export function createContainer(options: ContainerOptions): Container {
  const clock = new SystemClock();

  const pluginRepository = options.pluginsRoot
    ? new FilesystemPluginRegistry(options.pluginsRoot)
    : new InMemoryPluginRepository();

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
  commandBus.register("call-tool", new CallToolHandler(runtimeManager));

  const queryBus = new QueryBus();
  queryBus.register("list-plugins", new ListPluginsHandler(runtimeManager));

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
  };
}
