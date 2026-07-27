export interface ProcessHandle {
  readonly pid: number;
  readonly exited: Promise<number>;
  kill(signal?: string): Promise<void>;
}

export interface PluginProcessPort {
  spawn(command: string, args: readonly string[], env: Readonly<Record<string, string>>): Promise<ProcessHandle>;
}
