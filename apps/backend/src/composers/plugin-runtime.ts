import {
  InMemoryPluginRepository,
  NodeChildProcessAdapter,
  McpClientFactory,
  FilesystemPluginRegistry,
  SqliteDatabase,
  SqlitePluginRepository,
  PluginInstaller,
  PluginSyncService,
  MarkdownDocsIndex,
  SystemClock,
  type Logger,
} from "@nusashell/infrastructure";
import { PluginRuntimeManager, type PluginRepositoryPort } from "@nusashell/application";
import type { EventDispatcher } from "@nusashell/application";
import { fileURLToPath } from "node:url";
import type { ContainerOptions } from "../container.js";

export interface PluginRuntimeParts {
  readonly pluginRepository: PluginRepositoryPort;
  readonly runtimeManager: PluginRuntimeManager;
  readonly pluginInstaller: PluginInstaller | null;
  readonly syncPlugins: () => Promise<void>;
  readonly docsIndex: MarkdownDocsIndex;
  readonly db: SqliteDatabase | undefined;
}

export function createPluginRuntime(
  options: ContainerOptions,
  logger: Logger,
  eventDispatcher: EventDispatcher,
  clock: SystemClock,
): PluginRuntimeParts {
  let pluginRepository: PluginRepositoryPort;
  let db: SqliteDatabase | undefined;
  let syncPlugins: () => Promise<void> = async () => {};

  const bundledPluginsRoot = options.bundledPluginsRoot;
  const userPluginsRoot = options.userPluginsRoot ?? options.pluginsRoot;
  const pluginRoots = [
    ...(bundledPluginsRoot ? [bundledPluginsRoot] : []),
    ...(userPluginsRoot && userPluginsRoot !== bundledPluginsRoot ? [userPluginsRoot] : []),
  ];

  if (options.dbPath) {
    db = new SqliteDatabase(options.dbPath);
    pluginRepository = new SqlitePluginRepository(db);
    if (pluginRoots.length > 0) {
      const syncService = new PluginSyncService(pluginRoots, pluginRepository, logger);
      syncPlugins = () => syncService.sync();
      syncService.sync().catch((err) => {
        logger.warn({ err }, "Plugin sync failed during startup");
      });
    }
  } else if (pluginRoots.length > 0) {
    const filesystemRepository = new FilesystemPluginRegistry(pluginRoots, logger);
    pluginRepository = filesystemRepository;
    syncPlugins = () => filesystemRepository.refresh();
  } else {
    pluginRepository = new InMemoryPluginRepository();
  }

  const processAdapter = new NodeChildProcessAdapter(logger);
  const mcpClientFactory = new McpClientFactory(logger);

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

  const docsRoot = options.docsRoot ?? fileURLToPath(new URL("../../../resources/agent/docs", import.meta.url));
  const docsIndexStorageRoot = options.docsIndexStorageRoot ?? fileURLToPath(new URL("../../../.nusashell/agent/docs-index", import.meta.url));
  const docsIndex = new MarkdownDocsIndex(docsRoot, docsIndexStorageRoot);
  void docsIndex.reindex().catch((err) => {
    logger.warn({ err }, "Docs index initial build failed; will retry on demand");
  });

  const pluginInstaller = userPluginsRoot
    ? new PluginInstaller(userPluginsRoot, logger)
    : null;

  return { pluginRepository, runtimeManager, pluginInstaller, syncPlugins, docsIndex, db };
}
