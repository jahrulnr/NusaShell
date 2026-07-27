import { spawn, type ChildProcess } from "node:child_process";
import type { PluginProcessPort, ProcessHandle } from "@nusashell/application";

class NodeProcessHandle implements ProcessHandle {
  readonly pid: number;
  readonly exited: Promise<number>;
  private readonly child: ChildProcess;

  constructor(child: ChildProcess) {
    this.child = child;
    this.pid = child.pid ?? -1;
    this.exited = new Promise<number>((resolve, reject) => {
      child.once("exit", (code) => resolve(code ?? -1));
      child.once("error", (err) => reject(err));
    });
  }

  async kill(signal?: string): Promise<void> {
    if (!this.child.killed) {
      this.child.kill(signal as NodeJS.Signals ?? "SIGTERM");
    }
  }
}

export class NodeChildProcessAdapter implements PluginProcessPort {
  spawn(
    command: string,
    args: readonly string[],
    env: Readonly<Record<string, string>>,
  ): Promise<ProcessHandle> {
    const child = spawn(command, [...args], {
      env: { ...process.env, ...env },
      stdio: ["pipe", "pipe", "pipe"],
    });

    return Promise.resolve(new NodeProcessHandle(child));
  }
}
