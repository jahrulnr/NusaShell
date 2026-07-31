import type { MemorySnapshot } from "../../memory/ports/memory-store.port.js";
import { ENTRY_DELIMITER } from "../../memory/index.js";

/**
 * Format a memory snapshot into a system-prompt block.
 * Returns undefined when both targets are empty (no block injected).
 */
export function formatMemoryPrompt(snapshot: MemorySnapshot): string | undefined {
  const blocks: string[] = [];

  if (snapshot.memory.length > 0) {
    blocks.push(formatBlock("MEMORY (personal notes)", snapshot.memory.map((e: { text: string }) => e.text), snapshot.usage.memory.chars, snapshot.usage.memory.limit));
  }

  if (snapshot.user.length > 0) {
    blocks.push(formatBlock("USER PROFILE", snapshot.user.map((e: { text: string }) => e.text), snapshot.usage.user.chars, snapshot.usage.user.limit));
  }

  return blocks.length > 0 ? blocks.join("\n\n") : undefined;
}

function formatBlock(header: string, entries: readonly string[], chars: number, limit: number): string {
  const pct = Math.round((chars / limit) * 100);
  return `${header} [${pct}% — ${chars}/${limit} chars]\n${entries.join(ENTRY_DELIMITER)}`;
}
