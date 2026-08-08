import {
  InMemoryPluginRepository,
  NodeChildProcessAdapter,
  McpClientFactory,
  FilesystemPluginRegistry,
  SqliteDatabase,
  SqlitePluginRepository,
  PluginInstaller,
  PluginSyncService,
  BundledPluginSeeder,
  MarkdownDocsIndex,
  SystemClock,
  type Logger,
} from "@nusashell/infrastructure";
import { PluginRuntimeManager, type PluginRepositoryPort } from "@nusashell/application";
import type { EventDispatcher } from "@nusashell/application";
import { fileURLToPath } from "node:url";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { resolve } from "node:path";
import type { ContainerOptions } from "../container.js";

export interface PluginRuntimeParts {
  readonly pluginRepository: PluginRepositoryPort;
  readonly runtimeManager: PluginRuntimeManager;
  readonly pluginInstaller: PluginInstaller | null;
  readonly syncPlugins: () => Promise<void>;
  readonly docsIndex: MarkdownDocsIndex;
  readonly db: SqliteDatabase | undefined;
}

function bundledResource(relativePath: string): string {
  const candidates = [
    new URL(`../../../../resources/${relativePath}`, import.meta.url),
    new URL(`../../../resources/${relativePath}`, import.meta.url),
  ].map((url) => fileURLToPath(url));
  return candidates.find((candidate) => existsSync(candidate)) ?? candidates[0]!;
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
  // #49 single writable root: when bundled seeding is enabled and the bundled
  // root differs from the user root, bundled plugins are copied into the user
  // root at startup (seed + version reconcile) and the user root becomes the
  // ONLY scanned root. The bundled root then serves as seed source only.
  const seedBundled = options.seedBundledPlugins !== false
    && !!bundledPluginsRoot
    && !!userPluginsRoot
    && bundledPluginsRoot !== userPluginsRoot;
  const pluginRoots = seedBundled
    ? [userPluginsRoot!]
    : [
        ...(bundledPluginsRoot ? [bundledPluginsRoot] : []),
        ...(userPluginsRoot && userPluginsRoot !== bundledPluginsRoot ? [userPluginsRoot] : []),
      ];

  if (options.dbPath) {
    db = new SqliteDatabase(options.dbPath);
    pluginRepository = new SqlitePluginRepository(db);
    if (pluginRoots.length > 0) {
      const syncService = new PluginSyncService(pluginRoots, pluginRepository, logger);
      syncPlugins = () => syncService.sync();
      // Seed bundled → user root before the first sync so a fresh install
      // resolves every bundled plugin from the single writable root.
      const prepare = seedBundled
        ? new BundledPluginSeeder({ bundledRoot: bundledPluginsRoot!, userRoot: userPluginsRoot!, logger }).seed()
        : Promise.resolve();
      prepare
        .then(() => syncService.sync())
        .catch((err) => {
          logger.warn({ err }, "Plugin sync failed during startup");
        });
    }
  } else if (pluginRoots.length > 0) {
    const filesystemRepository = new FilesystemPluginRegistry(pluginRoots, logger);
    pluginRepository = filesystemRepository;
    syncPlugins = seedBundled
      ? async () => {
          await new BundledPluginSeeder({ bundledRoot: bundledPluginsRoot!, userRoot: userPluginsRoot!, logger }).seed();
          await filesystemRepository.refresh();
        }
      : () => filesystemRepository.refresh();
    if (seedBundled) {
      new BundledPluginSeeder({ bundledRoot: bundledPluginsRoot!, userRoot: userPluginsRoot!, logger })
        .seed()
        .then(() => filesystemRepository.refresh())
        .catch((err) => {
          logger.warn({ err }, "Bundled plugin seed + refresh failed during startup");
        });
    }
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

  const docsRoot = options.docsRoot ?? bundledResource("agent/docs");
  const docsIndexStorageRoot = options.docsIndexStorageRoot ?? resolve(homedir(), ".nusashell", "agent", "docs-index");
  const docsIndex = new MarkdownDocsIndex(docsRoot, docsIndexStorageRoot);
  void docsIndex.reindex().catch((err) => {
    logger.warn({ err }, "Docs index initial build failed; will retry on demand");
  });

  const pluginInstaller = userPluginsRoot
    ? new PluginInstaller(userPluginsRoot, logger)
    : null;

  return { pluginRepository, runtimeManager, pluginInstaller, syncPlugins, docsIndex, db };
}
