import type { PluginProcessPort, ProcessHandle } from "@nusashell/application";

export class FakeProcessHandle implements ProcessHandle {
  readonly pid: number;
  private exitReject: ((error: unknown) => void) | null = null;
  private exitResolve: ((code: number) => void) | null = null;
  readonly exited: Promise<number>;
  private killed = false;

  constructor(pid: number) {
    this.pid = pid;
    this.exited = new Promise<number>((resolve, reject) => {
      this.exitResolve = resolve;
      this.exitReject = reject;
    });
  }

  async kill(_signal?: string): Promise<void> {
    this.killed = true;
    if (this.exitResolve) {
      this.exitResolve(0);
    }
  }

  emitExit(code: number): void {
    if (this.exitResolve) {
      this.exitResolve(code);
    }
  }

  emitError(error: unknown): void {
    if (this.exitReject) {
      this.exitReject(error);
    }
  }

  wasKilled(): boolean {
    return this.killed;
  }
}

export class FakeProcessAdapter implements PluginProcessPort {
  private nextPid = 1000;
  readonly handles: FakeProcessHandle[] = [];

  spawn(
    _command: string,
    _args: readonly string[],
    _env: Readonly<Record<string, string>>,
  ): Promise<ProcessHandle> {
    const handle = new FakeProcessHandle(this.nextPid++);
    this.handles.push(handle);
    return Promise.resolve(handle);
  }
}
