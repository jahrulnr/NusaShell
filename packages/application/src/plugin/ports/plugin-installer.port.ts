export interface PluginInstallerPort {
  installFromUrl(url: string): Promise<{ installPath: string; pluginId: string; version: string }>;
  installFromPath(localPath: string): Promise<{ installPath: string; pluginId: string; version: string }>;
  uninstall(pluginId: string): Promise<void>;
}
