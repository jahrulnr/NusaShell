import { appendFile, mkdir, readdir, unlink } from "node:fs/promises";
import { join } from "node:path";
import type {
  AgentTurnTelemetry,
  ProviderRequestTelemetry,
  TelemetryPort,
  TelemetryRecord,
} from "@nusashell/application";

const PROVIDER_REQUEST_PREFIX = "provider-requests";
const AGENT_TURN_PREFIX = "agent-turns";
const FILE_PATTERN = /^(provider-requests|agent-turns)-(\d{4}-\d{2}-\d{2})\.jsonl$/;

export interface JsonlTelemetryWriterOptions {
  /** Directory that holds the daily JSONL files (e.g. `{userData}/telemetry`). */
  readonly dir: string;
  /** Days to retain daily files. Older files are pruned lazily. Default 30. */
  readonly retentionDays?: number;
  /** Clock seam for the rotation date + retention cutoff. */
  readonly now?: () => Date;
  /** Optional observer for otherwise-swallowed write failures. */
  readonly onError?: (error: unknown) => void;
}

/**
 * Append-only, newline-delimited JSON telemetry sink under a per-day file
 * (`provider-requests-YYYY-MM-DD.jsonl` / `agent-turns-YYYY-MM-DD.jsonl`).
 *
 * Writes are serialized on an internal promise chain so concurrent turns never
 * interleave partial lines, and every failure is swallowed (optionally observed
 * via `onError`) so telemetry can never fail a user turn. Files older than the
 * retention window are pruned lazily on the first write.
 */
export class JsonlTelemetryWriter implements TelemetryPort {
  private queue: Promise<void> = Promise.resolve();
  private pruned = false;
  private readonly retentionDays: number;

  constructor(private readonly options: JsonlTelemetryWriterOptions) {
    this.retentionDays = Math.max(1, Math.floor(options.retentionDays ?? 30));
  }

  recordProviderRequest(record: ProviderRequestTelemetry): void {
    this.enqueue(PROVIDER_REQUEST_PREFIX, record);
  }

  recordTurn(record: AgentTurnTelemetry): void {
    this.enqueue(AGENT_TURN_PREFIX, record);
  }

  /** Await all pending writes. Primarily for tests and graceful shutdown. */
  async flush(): Promise<void> {
    await this.queue.catch(() => {});
  }

  private enqueue(prefix: string, record: TelemetryRecord): void {
    const now = this.options.now?.() ?? new Date();
    const day = now.toISOString().slice(0, 10);
    const file = join(this.options.dir, `${prefix}-${day}.jsonl`);
    const line = `${JSON.stringify(record)}\n`;
    this.queue = this.queue
      .then(async () => {
        await mkdir(this.options.dir, { recursive: true });
        if (!this.pruned) {
          this.pruned = true;
          await this.prune(now);
        }
        await appendFile(file, line, "utf8");
      })
      .catch((error) => {
        this.options.onError?.(error);
      });
  }

  private async prune(now: Date): Promise<void> {
    try {
      const cutoffMs = now.getTime() - this.retentionDays * 24 * 60 * 60 * 1000;
      const entries = await readdir(this.options.dir);
      await Promise.all(
        entries.map(async (name) => {
          const match = FILE_PATTERN.exec(name);
          if (!match) return;
          const fileDayMs = Date.parse(`${match[2]}T00:00:00.000Z`);
          if (Number.isFinite(fileDayMs) && fileDayMs < cutoffMs) {
            await unlink(join(this.options.dir, name)).catch(() => {});
          }
        }),
      );
    } catch {
      // Pruning is best-effort; never block a write on cleanup.
    }
  }
}
