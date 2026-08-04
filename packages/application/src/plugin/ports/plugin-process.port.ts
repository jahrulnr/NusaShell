export interface ProcessHandle {
  readonly pid: number;
  readonly exited: Promise<number>;
  kill(signal?: string): Promise<void>;
  /**
   * Kill the entire process group (the process and all its children).
   * On Unix, sends the signal to process group `-pid`.
   * On Windows, uses `taskkill /T /F /PID`.
   * Falls back to `kill()` if process-group kill is not supported.
   */
  killGroup?(signal?: string): Promise<void>;
}

export interface PluginProcessPort {
  spawn(command: string, args: readonly string[], env: Readonly<Record<string, string>>): Promise<ProcessHandle>;
}
