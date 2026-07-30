import fs from "node:fs/promises";
import path from "node:path";
import { resolvePath } from "./config.js";

const MAX_READ_BYTES = 10 * 1024 * 1024;
const MAX_TREE_DEPTH = 10;
const MAX_SEARCH_RESULTS = 500;

const TEXT_EXTENSIONS = new Set([
  ".txt", ".md", ".markdown", ".json", ".js", ".mjs", ".cjs", ".ts", ".tsx",
  ".jsx", ".html", ".htm", ".css", ".scss", ".less", ".xml", ".yaml", ".yml",
  ".toml", ".ini", ".cfg", ".conf", ".env", ".gitignore", ".svg", ".csv",
  ".log", ".sh", ".bash", ".zsh", ".fish", ".py", ".rb", ".go", ".rs",
  ".java", ".kt", ".swift", ".c", ".cpp", ".h", ".hpp", ".cs", ".php",
  ".pl", ".lua", ".r", ".sql", ".graphql", ".gql", ".vue", ".svelte",
]);

const IMAGE_EXTENSIONS = new Set([
  ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".ico", ".tiff", ".tif",
  ".avif", ".heic", ".heif",
]);

/**
 * @param {string} name
 */
export function detectFileType(name) {
  const ext = path.extname(name).toLowerCase();
  if (TEXT_EXTENSIONS.has(ext)) return "text";
  if (IMAGE_EXTENSIONS.has(ext)) return "image";
  if (ext === ".pdf") return "pdf";
  if ([".mp4", ".webm", ".avi", ".mov", ".mkv"].includes(ext)) return "video";
  if ([".mp3", ".wav", ".ogg", ".flac", ".m4a"].includes(ext)) return "audio";
  if ([".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar"].includes(ext)) return "archive";
  return "binary";
}

/**
 * @param {number} bytes
 */
export function formatFileSize(bytes) {
  if (bytes === 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

export class FileService {
  /**
   * @param {string} root
   */
  constructor(root) {
    this.root = root;
  }

  /**
   * @param {string} input
   */
  async listDir(input) {
    const dir = resolvePath(this.root, input);
    const entries = await fs.readdir(dir, { withFileTypes: true });
    const items = await Promise.all(
      entries.map(async (entry) => {
        const entryPath = path.join(dir, entry.name);
        const stat = await fs.stat(entryPath).catch(() => null);
        if (!stat) return null;
        return {
          name: entry.name,
          path: path.relative(this.root, entryPath) || entry.name,
          isDir: stat.isDirectory(),
          isFile: stat.isFile(),
          isSymlink: entry.isSymbolicLink(),
          size: stat.isFile() ? stat.size : 0,
          modified: stat.mtime.toISOString(),
          created: stat.birthtime.toISOString(),
          type: stat.isDirectory() ? "dir" : detectFileType(entry.name),
        };
      }),
    );
    return items.filter(Boolean).sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
  }

  /**
   * @param {string} input
   * @param {number} depth
   */
  async tree(input, depth = 3) {
    const clampedDepth = Math.min(Math.max(depth, 1), MAX_TREE_DEPTH);
    const dir = resolvePath(this.root, input);
    return this._buildTree(dir, clampedDepth);
  }

  async _buildTree(dir, depth) {
    if (depth <= 0) return null;
    const entries = await fs.readdir(dir, { withFileTypes: true }).catch(() => []);
    const children = await Promise.all(
      entries.map(async (entry) => {
        const entryPath = path.join(dir, entry.name);
        const stat = await fs.stat(entryPath).catch(() => null);
        if (!stat) return null;
        const node = {
          name: entry.name,
          path: path.relative(this.root, entryPath) || entry.name,
          isDir: stat.isDirectory(),
          size: stat.isFile() ? stat.size : 0,
          modified: stat.mtime.toISOString(),
          type: stat.isDirectory() ? "dir" : detectFileType(entry.name),
        };
        if (stat.isDirectory() && depth > 1) {
          node.children = await this._buildTree(entryPath, depth - 1);
        }
        return node;
      }),
    );
    return children.filter(Boolean).sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
  }

  /**
   * @param {string} input
   * @param {number} head
   * @param {number} tail
   */
  async readFile(input, head, tail) {
    const filePath = resolvePath(this.root, input);
    const stat = await fs.stat(filePath);
    if (stat.size > MAX_READ_BYTES) {
      throw new Error(`File too large (${formatFileSize(stat.size)}), max ${formatFileSize(MAX_READ_BYTES)}`);
    }
    const content = await fs.readFile(filePath, "utf8");
    const lines = content.split("\n");

    if (head && head > 0) {
      return { content: lines.slice(0, head).join("\n"), totalLines: lines.length, truncated: true };
    }
    if (tail && tail > 0) {
      return { content: lines.slice(-tail).join("\n"), totalLines: lines.length, truncated: true };
    }
    return { content, totalLines: lines.length, truncated: false };
  }

  /**
   * @param {string} input
   * @param {string} content
   */
  async writeFile(input, content) {
    const filePath = resolvePath(this.root, input);
    await fs.mkdir(path.dirname(filePath), { recursive: true });
    await fs.writeFile(filePath, content, "utf8");
    return { path: path.relative(this.root, filePath) || path.basename(filePath), written: true };
  }

  /**
   * Create an empty directory, including any missing parents.
   * @param {string} input
   */
  async makeDir(input) {
    const dirPath = resolvePath(this.root, input);
    await fs.mkdir(dirPath, { recursive: true });
    return { path: path.relative(this.root, dirPath) || path.basename(dirPath), created: true };
  }

  /**
   * @param {string} source
   * @param {string} destination
   */
  async moveFile(source, destination) {
    const src = resolvePath(this.root, source);
    const dst = resolvePath(this.root, destination);
    await fs.mkdir(path.dirname(dst), { recursive: true });
    await fs.rename(src, dst);
    return { from: path.relative(this.root, src), to: path.relative(this.root, dst), moved: true };
  }

  /**
   * @param {string} input
   * @param {boolean} recursive
   */
  async deleteFile(input, recursive) {
    const target = resolvePath(this.root, input);
    const stat = await fs.stat(target);
    if (stat.isDirectory() && !recursive) {
      const entries = await fs.readdir(target);
      if (entries.length > 0) {
        throw new Error("Directory is not empty. Use recursive=true to delete.");
      }
    }
    await fs.rm(target, { recursive });
    return { path: path.relative(this.root, target) || path.basename(target), deleted: true };
  }

  /**
   * @param {string} input
   * @param {string} pattern
   */
  async searchFiles(input, pattern) {
    const dir = resolvePath(this.root, input);
    const regex = globToRegex(pattern);
    const results = [];
    await this._searchRecursive(dir, regex, results);
    return results.slice(0, MAX_SEARCH_RESULTS);
  }

  async _searchRecursive(dir, regex, results) {
    if (results.length >= MAX_SEARCH_RESULTS) return;
    const entries = await fs.readdir(dir, { withFileTypes: true }).catch(() => []);
    for (const entry of entries) {
      if (results.length >= MAX_SEARCH_RESULTS) return;
      if (regex.test(entry.name)) {
        const entryPath = path.join(dir, entry.name);
        const stat = await fs.stat(entryPath).catch(() => null);
        if (stat) {
          results.push({
            name: entry.name,
            path: path.relative(this.root, entryPath) || entry.name,
            isDir: stat.isDirectory(),
            size: stat.isFile() ? stat.size : 0,
            modified: stat.mtime.toISOString(),
            type: stat.isDirectory() ? "dir" : detectFileType(entry.name),
          });
        }
      }
      if (entry.isDirectory()) {
        await this._searchRecursive(path.join(dir, entry.name), regex, results);
      }
    }
  }

  /**
   * @param {string} input
   */
  async fileInfo(input) {
    const filePath = resolvePath(this.root, input);
    const stat = await fs.stat(filePath);
    return {
      name: path.basename(filePath),
      path: path.relative(this.root, filePath) || path.basename(filePath),
      isDir: stat.isDirectory(),
      isFile: stat.isFile(),
      isSymlink: stat.isSymbolicLink(),
      size: stat.size,
      modified: stat.mtime.toISOString(),
      created: stat.birthtime.toISOString(),
      type: stat.isDirectory() ? "dir" : detectFileType(filePath),
      permissions: stat.mode.toString(8),
    };
  }
}

/**
 * @param {string} pattern
 */
function globToRegex(pattern) {
  const escaped = pattern
    .replace(/[.+^${}()|[\]\\]/g, "\\$&")
    .replace(/\*/g, ".*")
    .replace(/\?/g, ".");
  return new RegExp(`^${escaped}$`, "i");
}
