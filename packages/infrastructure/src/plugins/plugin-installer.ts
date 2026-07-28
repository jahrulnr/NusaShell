import { readFile, writeFile, mkdir, rm, readdir, stat, access } from "node:fs/promises";
import { join, resolve, basename, extname } from "node:path";
import { tmpdir } from "node:os";
import { randomBytes } from "node:crypto";
import { pipeline } from "node:stream/promises";
import { Readable } from "node:stream";
import { createWriteStream } from "node:fs";
import type { PluginInstallerPort } from "@nusashell/application";
import { ManifestSchema } from "@nusashell/contracts";
import type { Logger } from "pino";
import AdmZip from "adm-zip";
import * as tar from "tar";

export class PluginInstaller implements PluginInstallerPort {
  constructor(
    private readonly pluginsRoot: string,
    private readonly logger?: Logger,
  ) {}

  async installFromUrl(url: string): Promise<{ installPath: string; pluginId: string; version: string }> {
    const tmpDir = join(tmpdir(), `nusashell-plugin-${randomBytes(8).toString("hex")}`);
    await mkdir(tmpDir, { recursive: true });

    const fileName = basename(new URL(url).pathname) || "plugin-download";
    const downloadPath = join(tmpDir, fileName);

    try {
      this.logger?.info({ url, downloadPath }, "Downloading plugin archive");
      const res = await fetch(url);
      if (!res.ok || !res.body) {
        throw new Error(`Download failed: ${res.status} ${res.statusText}`);
      }
      await pipeline(Readable.fromWeb(res.body as never), createWriteStream(downloadPath));

      return await this.installFromArchive(downloadPath, tmpDir);
    } finally {
      await rm(tmpDir, { recursive: true, force: true }).catch(() => {});
    }
  }

  async installFromPath(localPath: string): Promise<{ installPath: string; pluginId: string; version: string }> {
    const info = await stat(localPath).catch(() => null);
    if (!info) {
      throw new Error(`Path not found: ${localPath}`);
    }

    if (info.isDirectory()) {
      return await this.installFromDirectory(localPath);
    }

    const tmpDir = join(tmpdir(), `nusashell-plugin-${randomBytes(8).toString("hex")}`);
    await mkdir(tmpDir, { recursive: true });
    try {
      return await this.installFromArchive(localPath, tmpDir);
    } finally {
      await rm(tmpDir, { recursive: true, force: true }).catch(() => {});
    }
  }

  async uninstall(pluginId: string): Promise<void> {
    const pluginDir = join(this.pluginsRoot, pluginId);
    const exists = await access(pluginDir).then(() => true).catch(() => false);
    if (!exists) {
      throw new Error(`Plugin directory not found: ${pluginDir}`);
    }
    this.logger?.info({ pluginId, pluginDir }, "Uninstalling plugin");
    await rm(pluginDir, { recursive: true, force: true });
  }

  private async installFromArchive(archivePath: string, workDir: string): Promise<{ installPath: string; pluginId: string; version: string }> {
    const ext = extname(archivePath).toLowerCase();
    const isTar = ext === ".gz" || ext === ".tgz" || archivePath.endsWith(".tar.gz");
    const isZip = ext === ".zip";

    if (!isTar && !isZip) {
      throw new Error(`Unsupported archive format: ${archivePath}. Supported: .zip, .tar.gz, .tgz`);
    }

    const extractDir = join(workDir, "extracted");
    await mkdir(extractDir, { recursive: true });

    if (isZip) {
      const zip = new AdmZip(archivePath);
      zip.extractAllTo(extractDir, true);
    } else {
      await tar.x({ file: archivePath, cwd: extractDir });
    }

    const pluginDir = await this.findPluginDir(extractDir);
    return await this.installFromDirectory(pluginDir);
  }

  private async installFromDirectory(dir: string): Promise<{ installPath: string; pluginId: string; version: string }> {
    const manifestPath = join(dir, "manifest.json");
    const raw = await readFile(manifestPath, "utf-8");
    const parsedJson: unknown = JSON.parse(raw);
    const schemaResult = ManifestSchema.safeParse(parsedJson);
    if (!schemaResult.success) {
      throw new Error(`Invalid manifest.json: ${schemaResult.error.message}`);
    }
    const manifest = schemaResult.data;
    const pluginId = manifest.id;
    const version = manifest.version;

    const destDir = join(this.pluginsRoot, pluginId);
    const exists = await access(destDir).then(() => true).catch(() => false);
    if (exists) {
      this.logger?.info({ pluginId, destDir }, "Plugin already installed, replacing");
      await rm(destDir, { recursive: true, force: true });
    }

    await mkdir(join(this.pluginsRoot), { recursive: true });
    await this.copyDir(dir, destDir);

    this.logger?.info({ pluginId, destDir, version }, "Plugin installed successfully");
    return { installPath: destDir, pluginId, version };
  }

  private async findPluginDir(root: string): Promise<string> {
    const manifestPath = join(root, "manifest.json");
    const hasManifest = await access(manifestPath).then(() => true).catch(() => false);
    if (hasManifest) return root;

    const entries = await readdir(root);
    for (const entry of entries) {
      const fullPath = join(root, entry);
      const info = await stat(fullPath);
      if (info.isDirectory()) {
        const childManifest = join(fullPath, "manifest.json");
        const hasChild = await access(childManifest).then(() => true).catch(() => false);
        if (hasChild) return fullPath;
      }
    }

    throw new Error("No manifest.json found in extracted archive");
  }

  private async copyDir(src: string, dest: string): Promise<void> {
    await mkdir(dest, { recursive: true });
    const entries = await readdir(src, { withFileTypes: true });
    for (const entry of entries) {
      const srcPath = join(src, entry.name);
      const destPath = join(dest, entry.name);
      if (entry.isDirectory()) {
        await this.copyDir(srcPath, destPath);
      } else if (entry.isFile()) {
        const data = await readFile(srcPath);
        await writeFile(destPath, data);
      }
    }
  }
}
