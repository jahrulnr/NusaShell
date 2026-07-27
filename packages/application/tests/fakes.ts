import type {
  ClockPort,
  McpClientFactoryPort,
  McpClientPort,
  PluginProcessPort,
  PluginRepositoryPort,
  ProcessHandle,
  ToolDescriptor,
} from "../src/index.js";
import {
  Plugin,
  PluginId,
  PluginManifest,
  PluginVersion,
  type PluginManifestInput,
} from "@nusashell/domain";

export class FakeClock implements ClockPort {
  private current: Date;

  constructor(initial: Date = new Date("2026-01-01T00:00:00Z")) {
    this.current = initial;
  }

  now(): Date {
    return this.current;
  }

  advance(ms: number): void {
    this.current = new Date(this.current.getTime() + ms);
  }
}

export class FakeMcpClient implements McpClientPort {
  readonly tools: ToolDescriptor[] = [];
  private connected = false;
  private closeCallback: (() => void) | null = null;
  readonly callLog: Array<{
    name: string;
    args: Readonly<Record<string, unknown>>;
  }> = [];
  private readonly callResults = new Map<string, unknown>();
  private readonly callDelays = new Map<string, number>();
  private callShouldThrow = false;

  get pid(): number | null {
    return this.connected ? 1234 : null;
  }

  setToolResult(name: string, result: unknown): void {
    this.callResults.set(name, result);
  }

  setToolDelay(name: string, ms: number): void {
    this.callDelays.set(name, ms);
  }

  setThrowOnCall(should: boolean): void {
    this.callShouldThrow = should;
  }

  onClose(callback: () => void): void {
    this.closeCallback = callback;
  }

  emitClose(): void {
    if (this.closeCallback) {
      this.closeCallback();
    }
  }

  async connect(): Promise<void> {
    this.connected = true;
  }

  async close(): Promise<void> {
    this.connected = false;
  }

  isConnected(): boolean {
    return this.connected;
  }

  async listTools(): Promise<readonly ToolDescriptor[]> {
    return this.tools;
  }

  async callTool(
    name: string,
    args: Readonly<Record<string, unknown>>,
  ): Promise<unknown> {
    this.callLog.push({ name, args });
    if (this.callShouldThrow) {
      throw new Error("MCP call failed (fake)");
    }
    const delay = this.callDelays.get(name);
    if (delay) {
      await new Promise((resolve) => setTimeout(resolve, delay));
    }
    return this.callResults.get(name) ?? { ok: true };
  }
}

export class FakeMcpClientFactory implements McpClientFactoryPort {
  readonly created: FakeMcpClient[] = [];

  createForStdio(
    _command: string,
    _args: readonly string[],
    _env: Readonly<Record<string, string>>,
    _cwd?: string,
  ): McpClientPort {
    const client = new FakeMcpClient();
    this.created.push(client);
    return client;
  }

  createForHttp(_url: string): McpClientPort {
    const client = new FakeMcpClient();
    this.created.push(client);
    return client;
  }

  createForSse(_url: string): McpClientPort {
    const client = new FakeMcpClient();
    this.created.push(client);
    return client;
  }
}

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

export class FakePluginRepository implements PluginRepositoryPort {
  private readonly plugins = new Map<string, Plugin>();

  add(plugin: Plugin): void {
    this.plugins.set(PluginId.toString(plugin.id), plugin);
  }

  findById(id: PluginId): Promise<Plugin | null> {
    return Promise.resolve(this.plugins.get(PluginId.toString(id)) ?? null);
  }

  list(): Promise<readonly Plugin[]> {
    return Promise.resolve([...this.plugins.values()]);
  }

  save(plugin: Plugin): Promise<void> {
    this.plugins.set(PluginId.toString(plugin.id), plugin);
    return Promise.resolve();
  }

  remove(id: PluginId): Promise<void> {
    this.plugins.delete(PluginId.toString(id));
    return Promise.resolve();
  }
}

export function makeManifest(
  overrides: Partial<PluginManifestInput> = {},
): PluginManifest {
  const raw: PluginManifestInput = {
    id: "com.example.notes",
    name: "Notes",
    version: "1.0.0",
    icon: "note",
    ui: { entry: "index.html" },
    mcp: {
      transport: "stdio",
      command: "node",
      env: {},
    },
    ...overrides,
  };
  const result = PluginManifest.create(raw);
  if (!result.ok) {
    throw new Error(`Invalid manifest: ${result.error.message}`);
  }
  return result.value;
}

export function makePlugin(
  id: string = "com.example.notes",
  overrides: Partial<PluginManifestInput> = {},
  enabled: boolean = true,
): Plugin {
  const manifest = makeManifest({ id, ...overrides });
  const idResult = PluginId.create(id);
  if (!idResult.ok) {
    throw new Error(`Invalid plugin id: ${id}`);
  }
  const versionResult = PluginVersion.create(manifest.version.toString());
  if (!versionResult.ok) {
    throw new Error(`Invalid version`);
  }
  return Plugin.create({
    id: idResult.value,
    version: versionResult.value,
    manifest,
    enabled,
    installPath: `/plugins/${id}`,
    installedAt: new Date("2026-01-01T00:00:00Z"),
  });
}
