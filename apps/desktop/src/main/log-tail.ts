export type ShellLogLevel = "debug" | "info" | "warn" | "error";
export type ShellLogSource = "backend" | "ipc" | "main" | "mcp" | "renderer" | "acp";

export interface ShellLogEntry {
  readonly id: number;
  readonly timestamp: string;
  readonly source: ShellLogSource;
  readonly level: ShellLogLevel;
  readonly message: string;
}

export class LogTail {
  private readonly entries: ShellLogEntry[] = [];
  private readonly listeners = new Set<(entry: ShellLogEntry) => void>();
  private nextId = 1;

  constructor(private readonly maxEntries = 1000) {}

  add(source: ShellLogSource, level: ShellLogLevel, message: string): ShellLogEntry {
    const entry: ShellLogEntry = {
      id: this.nextId++,
      timestamp: new Date().toISOString(),
      source,
      level,
      message,
    };

    this.entries.push(entry);
    if (this.entries.length > this.maxEntries) this.entries.shift();
    this.listeners.forEach((listener) => listener(entry));
    return entry;
  }

  list(): readonly ShellLogEntry[] {
    return this.entries;
  }

  subscribe(listener: (entry: ShellLogEntry) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
}
