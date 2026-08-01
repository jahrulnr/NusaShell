import { existsSync, readFileSync } from "node:fs";
import { platform as nodePlatform } from "node:os";
import type { RuntimeOsProbe } from "@nusashell/application";

/**
 * Infrastructure adapter that probes the real host OS for runtime detection.
 * Uses `node:os` and `node:fs` so the application layer doesn't have to.
 */
export class NodeRuntimeOsProbe implements RuntimeOsProbe {
  readonly platform: string = nodePlatform();

  fileExists(path: string): boolean {
    return existsSync(path);
  }

  readTextFile(path: string): string | undefined {
    try {
      return readFileSync(path, "utf8");
    } catch {
      return undefined;
    }
  }
}
