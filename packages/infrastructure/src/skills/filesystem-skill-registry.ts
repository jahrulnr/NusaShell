import { randomBytes } from "node:crypto";
import {
  access,
  mkdir,
  readFile,
  readdir,
  rename,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import { extname, isAbsolute, resolve, sep } from "node:path";
import type {
  SkillDetail,
  SkillFileEntry,
  SkillReadResult,
  SkillRegistryPort,
  SkillSummary,
  ArchivedSkillSummary,
} from "@nusashell/application";
import AdmZip from "adm-zip";

const MAX_ARCHIVE_ENTRIES = 500;
const MAX_ARCHIVE_BYTES = 32 * 1024 * 1024;
const MAX_FILE_BYTES = 8 * 1024 * 1024;
const MAX_EDITABLE_BYTES = 1024 * 1024;
const MAX_DESCRIPTION_CHARS = 60;
const SKILL_ID = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const SUPPORT_FILE_PREFIXES = ["references/", "templates/", "scripts/", "assets/"];

export class FilesystemSkillRegistry implements SkillRegistryPort {
  constructor(private readonly root: string) {}

  async list(): Promise<readonly SkillSummary[]> {
    await mkdir(this.root, { recursive: true });
    const entries = await readdir(this.root, { withFileTypes: true });
    const skills = await Promise.all(entries
      .filter((entry) => entry.isDirectory() && SKILL_ID.test(entry.name))
      .map((entry) => this.get(entry.name).catch(() => null)));
    return skills
      .filter((skill): skill is SkillDetail => skill !== null)
      .map(({ files: _files, ...summary }) => summary)
      .sort((left, right) => left.name.localeCompare(right.name));
  }

  async search(query: string, limit = 20): Promise<readonly SkillSummary[]> {
    const normalized = query.trim().toLocaleLowerCase();
    const skills = await this.list();
    return skills
      .filter((skill) => !normalized || `${skill.name} ${skill.description}`.toLocaleLowerCase().includes(normalized))
      .slice(0, clamp(limit, 1, 100));
  }

  async get(skillId: string): Promise<SkillDetail> {
    const skillRoot = this.skillRoot(skillId);
    const skillFile = resolve(skillRoot, "SKILL.md");
    const metadata = parseFrontmatter(await readFile(skillFile, "utf8"));
    if (metadata.name !== skillId) throw new Error(`SKILL.md name must match its installed id: ${skillId}`);
    const files = await this.walk(skillRoot);
    const info = await stat(skillFile);
    return {
      id: skillId,
      name: metadata.name,
      description: metadata.description,
      fileCount: files.filter((file) => file.type === "file").length,
      updatedAt: info.mtime.toISOString(),
      files,
    };
  }

  async read(skillId: string, path = "SKILL.md", offset = 0, maxChars = 20_000): Promise<SkillReadResult> {
    const target = this.skillPath(skillId, path);
    const info = await stat(target);
    if (!info.isFile()) throw new Error(`Skill file not found: ${path}`);
    const data = await readFile(target);
    const editable = isUtf8Text(data) && data.byteLength <= MAX_EDITABLE_BYTES;
    if (!editable) {
      return { skillId, path: normalizedRelative(path), sizeBytes: data.byteLength, editable, truncated: false };
    }
    const content = data.toString("utf8");
    const start = clamp(offset, 0, content.length);
    const limit = clamp(maxChars, 1, 100_000);
    const text = content.slice(start, start + limit);
    const nextOffset = start + text.length;
    return {
      skillId,
      path: normalizedRelative(path),
      content: text,
      sizeBytes: data.byteLength,
      editable,
      truncated: nextOffset < content.length,
      ...(nextOffset < content.length ? { nextOffset } : {}),
    };
  }

  async installFromArchive(archivePath: string): Promise<SkillDetail> {
    const extension = extname(archivePath).toLocaleLowerCase();
    if (extension !== ".zip" && extension !== ".skill") {
      throw new Error("Skill package must use .skill or .zip");
    }
    const zip = new AdmZip(archivePath);
    const entries = zip.getEntries();
    if (entries.length === 0 || entries.length > MAX_ARCHIVE_ENTRIES) throw new Error("Skill archive has an invalid file count");

    let expandedBytes = 0;
    for (const entry of entries) {
      assertArchivePath(entry.entryName);
      if (isSymlink(entry)) throw new Error(`Skill archive contains a symbolic link: ${entry.entryName}`);
      expandedBytes += entry.header.size;
      if (entry.header.size > MAX_FILE_BYTES || expandedBytes > MAX_ARCHIVE_BYTES) {
        throw new Error("Skill archive exceeds the expanded size limit");
      }
    }

    const prefix = skillRootPrefix(entries.map((entry) => entry.entryName));
    const outsidePackage = entries.find((entry) =>
      prefix
      && !entry.entryName.startsWith(prefix)
      && !entry.entryName.startsWith("__MACOSX/"));
    if (outsidePackage) throw new Error(`Skill archive contains files outside its package root: ${outsidePackage.entryName}`);
    const skillEntry = entries.find((entry) => stripPrefix(entry.entryName, prefix) === "SKILL.md");
    if (!skillEntry) throw new Error("Skill package must contain SKILL.md");
    const metadata = parseFrontmatter(skillEntry.getData().toString("utf8"));
    if (!SKILL_ID.test(metadata.name)) throw new Error("SKILL.md name must be a lowercase slug");

    await mkdir(this.root, { recursive: true });
    const destination = this.skillRoot(metadata.name);
    if (await exists(destination)) throw new Error(`Skill is already installed: ${metadata.name}`);
    const staging = resolve(this.root, `.install-${randomBytes(8).toString("hex")}`);
    await mkdir(staging);
    try {
      for (const entry of entries) {
        const path = stripPrefix(entry.entryName, prefix);
        if (!path || path.startsWith("__MACOSX/")) continue;
        const target = safeChild(staging, path, "unsafe archive path");
        if (entry.isDirectory) await mkdir(target, { recursive: true });
        else {
          await mkdir(resolve(target, ".."), { recursive: true });
          await writeFile(target, entry.getData(), { flag: "wx" });
        }
      }
      await rename(staging, destination);
    } catch (error) {
      await rm(staging, { recursive: true, force: true });
      throw error;
    }
    return this.get(metadata.name);
  }

  async create(skillId: string, skillMd: string): Promise<SkillDetail> {
    if (!SKILL_ID.test(skillId)) throw new Error("Invalid skill id");
    const metadata = parseFrontmatter(skillMd);
    if (metadata.name !== skillId) throw new Error(`SKILL.md name must match its id: ${skillId}`);
    if (metadata.description.length > MAX_DESCRIPTION_CHARS) {
      throw new Error(`SKILL.md description must be ${MAX_DESCRIPTION_CHARS} characters or fewer (got ${metadata.description.length})`);
    }
    const destination = this.skillRoot(skillId);
    if (await exists(destination)) throw new Error(`Skill already exists: ${skillId}`);
    await mkdir(destination, { recursive: true });
    await writeFile(resolve(destination, "SKILL.md"), skillMd, "utf8");
    return this.get(skillId);
  }

  async write(skillId: string, path: string, content: string): Promise<SkillReadResult> {
    if (Buffer.byteLength(content, "utf8") > MAX_EDITABLE_BYTES) throw new Error("Skill file exceeds the editable size limit");
    const normalizedPath = normalizedRelative(path);
    if (normalizedPath === "SKILL.md") {
      const metadata = parseFrontmatter(content);
      if (metadata.name !== skillId) throw new Error(`SKILL.md name must remain ${skillId}`);
      if (metadata.description.length > MAX_DESCRIPTION_CHARS) {
        throw new Error(`SKILL.md description must be ${MAX_DESCRIPTION_CHARS} characters or fewer (got ${metadata.description.length})`);
      }
    }
    const target = this.skillPath(skillId, normalizedPath);
    const fileExists = await exists(target);
    if (fileExists) {
      const current = await readFile(target);
      if (!isUtf8Text(current) || current.byteLength > MAX_EDITABLE_BYTES) throw new Error("Only UTF-8 text skill files can be edited");
    } else {
      if (normalizedPath === "SKILL.md") throw new Error("SKILL.md does not exist; use create() instead");
      if (!SUPPORT_FILE_PREFIXES.some((prefix) => normalizedPath.startsWith(prefix))) {
        throw new Error(`Support file creation is only allowed under: ${SUPPORT_FILE_PREFIXES.join(", ")}`);
      }
      await mkdir(resolve(target, ".."), { recursive: true });
    }
    await writeFile(target, content, "utf8");
    return this.read(skillId, path, 0, 100_000);
  }

  async delete(skillId: string): Promise<void> {
    const target = this.skillRoot(skillId);
    if (!await exists(resolve(target, "SKILL.md"))) throw new Error(`Skill not found: ${skillId}`);
    await rm(target, { recursive: true, force: false });
  }

  async archive(skillId: string): Promise<void> {
    const source = this.skillRoot(skillId);
    if (!await exists(resolve(source, "SKILL.md"))) throw new Error(`Skill not found: ${skillId}`);
    const archiveDir = resolve(this.root, ".archive");
    await mkdir(archiveDir, { recursive: true });
    const destination = resolve(archiveDir, skillId);
    if (await exists(destination)) throw new Error(`Skill already archived: ${skillId}`);
    await rename(source, destination);
  }

  async restore(skillId: string): Promise<void> {
    if (!SKILL_ID.test(skillId)) throw new Error("Invalid skill id");
    const archiveDir = resolve(this.root, ".archive");
    const source = resolve(archiveDir, skillId);
    if (!await exists(resolve(source, "SKILL.md"))) throw new Error(`Archived skill not found: ${skillId}`);
    const destination = this.skillRoot(skillId);
    if (await exists(destination)) throw new Error(`Skill already exists: ${skillId}`);
    await rename(source, destination);
  }

  async listArchived(): Promise<readonly ArchivedSkillSummary[]> {
    const archiveDir = resolve(this.root, ".archive");
    if (!await exists(archiveDir)) return [];
    const entries = await readdir(archiveDir, { withFileTypes: true });
    const skills = await Promise.all(entries
      .filter((entry) => entry.isDirectory() && SKILL_ID.test(entry.name))
      .map(async (entry) => {
        try {
          const skillFile = resolve(archiveDir, entry.name, "SKILL.md");
          const metadata = parseFrontmatter(await readFile(skillFile, "utf8"));
          const info = await stat(resolve(archiveDir, entry.name));
          return {
            id: entry.name,
            name: metadata.name,
            description: metadata.description,
            archivedAt: info.mtime.toISOString(),
          } satisfies ArchivedSkillSummary;
        } catch {
          return null;
        }
      }));
    return skills.filter((skill): skill is ArchivedSkillSummary => skill !== null)
      .sort((left, right) => left.name.localeCompare(right.name));
  }

  private skillRoot(skillId: string): string {
    if (!SKILL_ID.test(skillId)) throw new Error("Invalid skill id");
    return safeChild(this.root, skillId, "Invalid skill id");
  }

  private skillPath(skillId: string, path: string): string {
    return safeChild(this.skillRoot(skillId), normalizedRelative(path), "Invalid skill path");
  }

  private async walk(root: string, directory = ""): Promise<SkillFileEntry[]> {
    const entries = await readdir(resolve(root, directory), { withFileTypes: true });
    const output: SkillFileEntry[] = [];
    for (const entry of entries.sort((left, right) => left.name.localeCompare(right.name))) {
      const path = directory ? `${directory}/${entry.name}` : entry.name;
      const target = resolve(root, path);
      if (entry.isSymbolicLink()) continue;
      if (entry.isDirectory()) {
        output.push({ path, type: "directory", sizeBytes: 0, editable: false });
        output.push(...await this.walk(root, path));
      } else if (entry.isFile()) {
        const info = await stat(target);
        const editable = info.size <= MAX_EDITABLE_BYTES
          ? isUtf8Text(await readFile(target))
          : false;
        output.push({
          path,
          type: "file",
          sizeBytes: info.size,
          editable,
        });
      }
    }
    return output;
  }
}

function parseFrontmatter(content: string): { name: string; description: string } {
  const match = /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/.exec(content);
  if (!match) throw new Error("SKILL.md must begin with YAML frontmatter");
  const values = new Map<string, string>();
  for (const line of match[1]!.split(/\r?\n/)) {
    const field = /^([A-Za-z][\w-]*):\s*(.+?)\s*$/.exec(line);
    if (field) values.set(field[1]!, unquote(field[2]!));
  }
  const name = values.get("name")?.trim() ?? "";
  const description = values.get("description")?.trim() ?? "";
  if (!name || !description) throw new Error("SKILL.md frontmatter requires name and description");
  return { name, description };
}

function unquote(value: string): string {
  if ((value.startsWith("\"") && value.endsWith("\"")) || (value.startsWith("'") && value.endsWith("'"))) {
    return value.slice(1, -1);
  }
  return value;
}

function normalizedRelative(path: string): string {
  const normalized = path.replaceAll("\\", "/").replace(/^\.\/+/, "");
  if (!normalized || normalized.startsWith("/") || normalized.split("/").some((segment) => !segment || segment === "..")) {
    throw new Error("Invalid skill path");
  }
  return normalized;
}

function safeChild(root: string, path: string, message: string): string {
  if (isAbsolute(path)) throw new Error(message);
  const resolvedRoot = resolve(root);
  const target = resolve(resolvedRoot, path);
  if (target !== resolvedRoot && !target.startsWith(`${resolvedRoot}${sep}`)) throw new Error(message);
  return target;
}

function assertArchivePath(path: string): void {
  const normalized = path.replaceAll("\\", "/");
  if (normalized.startsWith("/") || /^[A-Za-z]:\//.test(normalized) || normalized.split("/").some((part) => part === "..")) {
    throw new Error(`Unsafe archive path: ${path}`);
  }
}

function skillRootPrefix(paths: readonly string[]): string {
  const candidates = paths.filter((path) => path.endsWith("/SKILL.md"));
  if (paths.includes("SKILL.md")) candidates.unshift("SKILL.md");
  if (candidates.length !== 1) throw new Error("Skill archive must contain exactly one SKILL.md");
  if (candidates[0] === "SKILL.md") return "";
  const prefix = candidates[0]!.slice(0, -"SKILL.md".length);
  if (prefix.slice(0, -1).includes("/")) throw new Error("SKILL.md must be at the package root or inside one wrapper directory");
  return prefix;
}

function stripPrefix(path: string, prefix: string): string {
  if (!prefix) return path.replace(/\/$/, "");
  return path.startsWith(prefix) ? path.slice(prefix.length).replace(/\/$/, "") : "";
}

function isSymlink(entry: AdmZip.IZipEntry): boolean {
  const mode = (entry.header.attr >>> 16) & 0o170000;
  return mode === 0o120000;
}

function isUtf8Text(data: Buffer): boolean {
  if (data.includes(0)) return false;
  try {
    new TextDecoder("utf-8", { fatal: true }).decode(data);
    return true;
  } catch {
    return false;
  }
}

async function exists(path: string): Promise<boolean> {
  return access(path).then(() => true).catch(() => false);
}

function clamp(value: number, min: number, max: number): number {
  if (!Number.isFinite(value)) return min;
  return Math.max(min, Math.min(max, Math.trunc(value)));
}
