import type { MemoryEntry, MemoryTarget, MemoryUsage } from "./ports/memory-store.port.js";

export const MEMORY_LIMIT = 2200;
export const USER_LIMIT = 1375;
export const ENTRY_DELIMITER = "\n§\n";

/** Persisted prefix; stripped from `text` and excluded from capacity accounting. */
const CREATED_AT_RE = /^<!--ns-created:(\d{4}-\d{2}-\d{2}T[\d:.]+Z)-->\r?\n?/;

const TARGET_LIMITS: Readonly<Record<MemoryTarget, number>> = {
  memory: MEMORY_LIMIT,
  user: USER_LIMIT,
};

export function limitFor(target: MemoryTarget): number {
  return TARGET_LIMITS[target];
}

export function splitEntries(raw: string): readonly MemoryEntry[] {
  const trimmed = raw.trim();
  if (trimmed.length === 0) return [];
  return trimmed
    .split(/§/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
    .map(parseEntrySegment);
}

export function joinEntries(entries: readonly MemoryEntry[]): string {
  return entries.map(serializeEntry).join(ENTRY_DELIMITER);
}

export function charsOf(entries: readonly MemoryEntry[]): number {
  if (entries.length === 0) return 0;
  return entries.reduce((sum, e) => sum + e.text.length, 0) + ENTRY_DELIMITER.length * (entries.length - 1);
}

export function usageOf(entries: readonly MemoryEntry[], target: MemoryTarget): MemoryUsage {
  return { chars: charsOf(entries), limit: limitFor(target) };
}

export function checkCapacity(entries: readonly MemoryEntry[], target: MemoryTarget): { ok: boolean; overflow: number } {
  const limit = limitFor(target);
  const total = charsOf(entries);
  return { ok: total <= limit, overflow: Math.max(0, total - limit) };
}

export const MATCH_AMBIGUOUS = -3;
export const MATCH_NOT_FOUND = -1;
export const MATCH_EMPTY = -2;

export function findUniqueMatch(entries: readonly MemoryEntry[], oldText: string): number {
  const needle = oldText.trim();
  if (needle.length === 0) return MATCH_EMPTY;
  let matchIndex = -1;
  for (let i = 0; i < entries.length; i++) {
    const entry = entries[i];
    if (entry && entry.text.includes(needle)) {
      if (matchIndex !== -1) return MATCH_AMBIGUOUS;
      matchIndex = i;
    }
  }
  return matchIndex;
}

export function addEntry(
  entries: readonly MemoryEntry[],
  content: string,
  now: () => Date = () => new Date(),
): readonly MemoryEntry[] {
  const text = content.trim();
  if (text.length === 0) return entries;
  return [...entries, { text, createdAt: now().toISOString() }];
}

export function replaceEntry(
  entries: readonly MemoryEntry[],
  oldText: string,
  content: string,
): { entries: readonly MemoryEntry[]; matchedIndex: number } {
  const index = findUniqueMatch(entries, oldText);
  if (index < 0) return { entries, matchedIndex: index };
  const text = content.trim();
  const next = [...entries];
  if (text.length === 0) {
    next.splice(index, 1);
  } else {
    const previous = entries[index]!;
    next[index] = { text, createdAt: previous.createdAt };
  }
  return { entries: next, matchedIndex: index };
}

export function removeEntry(
  entries: readonly MemoryEntry[],
  oldText: string,
): { entries: readonly MemoryEntry[]; matchedIndex: number } {
  const index = findUniqueMatch(entries, oldText);
  if (index < 0) return { entries, matchedIndex: index };
  const next = [...entries];
  next.splice(index, 1);
  return { entries: next, matchedIndex: index };
}

function parseEntrySegment(segment: string): MemoryEntry {
  const match = CREATED_AT_RE.exec(segment);
  if (!match) return { text: segment, createdAt: null };
  const createdAt = match[1]!;
  const text = segment.slice(match[0].length).trim();
  return { text, createdAt };
}

function serializeEntry(entry: MemoryEntry): string {
  if (!entry.createdAt) return entry.text;
  return `<!--ns-created:${entry.createdAt}-->\n${entry.text}`;
}
