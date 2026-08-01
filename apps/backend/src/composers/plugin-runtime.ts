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
import type { ContainerOptions } from "../container.js";

export interface PluginRuntimeParts {
  readonly pluginRepository: PluginRepositoryPort;
  readonly runtimeManager: PluginRuntimeManager;
  readonly pluginInstaller: PluginInstaller | null;
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

  if (options.dbPath) {
    db = new SqliteDatabase(options.dbPath);
    pluginRepository = new SqlitePluginRepository(db);
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

  const pluginInstaller = options.pluginsRoot
    ? new PluginInstaller(options.pluginsRoot, logger)
    : null;

  return { pluginRepository, runtimeManager, pluginInstaller, docsIndex, db };
}
