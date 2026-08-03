export type MemoryTarget = "memory" | "user";

export interface MemoryEntry {
  readonly text: string;
  /** ISO-8601 creation time when known; null for legacy undated entries. */
  readonly createdAt: string | null;
}

export interface MemoryUsage {
  readonly chars: number;
  readonly limit: number;
}

export interface MemorySnapshot {
  readonly memory: readonly MemoryEntry[];
  readonly user: readonly MemoryEntry[];
  readonly usage: {
    readonly memory: MemoryUsage;
    readonly user: MemoryUsage;
  };
}

export interface MemoryMutationResult {
  readonly ok: true;
  readonly data: {
    readonly entries: readonly MemoryEntry[];
    readonly usage: MemoryUsage;
  };
}

export interface MemoryStorePort {
  loadSnapshot(): Promise<MemorySnapshot>;
  add(target: MemoryTarget, content: string): Promise<MemoryMutationResult>;
  replace(target: MemoryTarget, oldText: string, content: string): Promise<MemoryMutationResult>;
  remove(target: MemoryTarget, oldText: string): Promise<MemoryMutationResult>;
}
