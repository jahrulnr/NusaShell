import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type {
  MemoryEntry,
  MemoryMutationResult,
  MemorySnapshot,
  MemoryStorePort,
  MemoryTarget,
} from "@nusashell/application";
import {
  addEntry,
  checkCapacity,
  charsOf,
  joinEntries,
  limitFor,
  MATCH_AMBIGUOUS,
  MATCH_EMPTY,
  removeEntry,
  replaceEntry,
  splitEntries,
  usageOf,
} from "@nusashell/application";

const MEMORY_FILE = "MEMORY.md";
const USER_FILE = "USER.md";

/**
 * Persistent agent memory stored as two Markdown files (MEMORY.md and
 * USER.md) under a root directory. Entries are delimited by §.
 * Writes are atomic (temp file + rename). The root is created on first use.
 */
export class FilesystemMemoryStore implements MemoryStorePort {
  constructor(private readonly root: string) {}

  async loadSnapshot(): Promise<MemorySnapshot> {
    const [memoryRaw, userRaw] = await Promise.all([
      this.readRaw(MEMORY_FILE),
      this.readRaw(USER_FILE),
    ]);
    const memory = splitEntries(memoryRaw);
    const user = splitEntries(userRaw);
    return {
      memory,
      user,
      usage: {
        memory: usageOf(memory, "memory"),
        user: usageOf(user, "user"),
      },
    };
  }

  async add(target: MemoryTarget, content: string): Promise<MemoryMutationResult> {
    const entries = await this.loadTarget(target);
    const next = addEntry(entries, content);
    return this.persistAndReturn(target, next);
  }

  async replace(target: MemoryTarget, oldText: string, content: string): Promise<MemoryMutationResult> {
    const entries = await this.loadTarget(target);
    const { entries: next, matchedIndex } = replaceEntry(entries, oldText, content);
    if (matchedIndex < 0) throw matchError(matchedIndex);
    return this.persistAndReturn(target, next);
  }

  async remove(target: MemoryTarget, oldText: string): Promise<MemoryMutationResult> {
    const entries = await this.loadTarget(target);
    const { entries: next, matchedIndex } = removeEntry(entries, oldText);
    if (matchedIndex < 0) throw matchError(matchedIndex);
    return this.persistAndReturn(target, next);
  }

  private async loadTarget(target: MemoryTarget): Promise<readonly MemoryEntry[]> {
    const raw = await this.readRaw(this.fileFor(target));
    return splitEntries(raw);
  }

  private async persistAndReturn(
    target: MemoryTarget,
    entries: readonly MemoryEntry[],
  ): Promise<MemoryMutationResult> {
    const cap = checkCapacity(entries, target);
    if (!cap.ok) {
      throw new Error(
        `Memory capacity exceeded for "${target}": ${charsOf(entries)}/${limitFor(target)} chars (overflow ${cap.overflow}). Remove or shorten entries first.`,
      );
    }
    await this.writeRaw(this.fileFor(target), joinEntries(entries));
    return {
      ok: true,
      data: {
        entries,
        usage: usageOf(entries, target),
      },
    };
  }

  private fileFor(target: MemoryTarget): string {
    return target === "memory" ? MEMORY_FILE : USER_FILE;
  }

  private async readRaw(filename: string): Promise<string> {
    try {
      return await readFile(join(this.root, filename), "utf8");
    } catch {
      return "";
    }
  }

  private async writeRaw(filename: string, content: string): Promise<void> {
    await mkdir(this.root, { recursive: true });
    const targetPath = join(this.root, filename);
    const tempPath = `${targetPath}.tmp`;
    await writeFile(tempPath, content, "utf8");
    await rename(tempPath, targetPath);
  }
}

function matchError(matchedIndex: number): Error {
  if (matchedIndex === MATCH_EMPTY) return new Error("old_text is required");
  if (matchedIndex === MATCH_AMBIGUOUS) return new Error("old_text matched multiple entries — provide a more specific fragment");
  return new Error("old_text did not match any entry");
}
