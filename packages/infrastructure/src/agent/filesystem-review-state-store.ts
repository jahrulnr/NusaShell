import { readFile, writeFile, mkdir } from "node:fs/promises";
import { join } from "node:path";
import type { ReviewState, ReviewStateStorePort } from "@nusashell/application";

const STATE_FILE = ".review-state.json";

const DEFAULT_STATE: ReviewState = {
  turnsSinceMemory: 0,
  toolRoundsSinceSkill: 0,
};

/**
 * Stores background review counters in a JSON sidecar file. Uses atomic
 * write-then-rename for crash safety.
 */
export class FilesystemReviewStateStore implements ReviewStateStorePort {
  constructor(private readonly memoryRoot: string) {}

  async load(): Promise<ReviewState> {
    try {
      const raw = await readFile(join(this.memoryRoot, STATE_FILE), "utf8");
      const parsed = JSON.parse(raw) as ReviewState;
      return {
        turnsSinceMemory: parsed.turnsSinceMemory ?? 0,
        toolRoundsSinceSkill: parsed.toolRoundsSinceSkill ?? 0,
        ...(parsed.lastReviewAt ? { lastReviewAt: parsed.lastReviewAt } : {}),
      };
    } catch {
      return { ...DEFAULT_STATE };
    }
  }

  async save(state: ReviewState): Promise<void> {
    await mkdir(this.memoryRoot, { recursive: true });
    const path = join(this.memoryRoot, STATE_FILE);
    const tmp = `${path}.tmp`;
    await writeFile(tmp, JSON.stringify(state, null, 2), "utf8");
    await writeFile(path, await readFile(tmp, "utf8"), "utf8");
    try { await import("node:fs/promises").then(f => f.unlink(tmp)); } catch { /* best effort */ }
  }
}
