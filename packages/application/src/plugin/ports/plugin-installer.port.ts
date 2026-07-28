export interface PluginInstallerPort {
  installFromUrl(url: string): Promise<{ installPath: string; pluginId: string }>;
  installFromPath(localPath: string): Promise<{ installPath: string; pluginId: string }>;
  uninstall(pluginId: string): Promise<void>;
}
