import {
  createWriteStream,
  existsSync,
  mkdirSync,
  openSync,
  readdirSync,
  renameSync,
  statSync,
  unlinkSync,
} from "node:fs";
import { basename, dirname, extname, join } from "node:path";
import { Writable } from "node:stream";

export interface RotatingFileStreamOptions {
  readonly retentionDays?: number;
}

function toISODate(d: Date): string {
  return d.toISOString().slice(0, 10);
}

function addDays(d: Date, days: number): Date {
  const c = new Date(d);
  c.setDate(c.getDate() + days);
  return c;
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export class RotatingFileStream extends Writable {
  readonly #basePath: string;
  readonly #retentionDays: number;
  readonly #dir: string;
  readonly #baseName: string;
  readonly #ext: string;
  #stream: ReturnType<typeof createWriteStream> | null = null;
  #currentDate: string | null = null;
  #nextTimer: NodeJS.Timeout | null = null;
  #tail: Promise<void> = Promise.resolve();
  #destroyed = false;

  constructor(basePath: string, options: RotatingFileStreamOptions = {}) {
    super();
    this.#basePath = basePath;
    this.#retentionDays = Math.max(1, options.retentionDays ?? 3);
    this.#dir = dirname(basePath);
    this.#baseName = basename(basePath, extname(basePath));
    this.#ext = extname(basePath) || ".log";
    this.#initialize();
  }

  override _write(
    chunk: unknown,
    encoding: BufferEncoding,
    callback: (error?: Error | null) => void,
  ): void {
    const run = this.#runExclusive(async () => {
      if (this.#destroyed) return;
      const today = toISODate(new Date());
      if (this.#currentDate !== today) {
        await this.#rotate(today);
      }
      if (!this.#stream) return;
      await new Promise<void>((resolve, reject) => {
        this.#stream!.write(chunk, encoding, (err) => {
          if (err) reject(err);
          else resolve();
        });
      });
    });
    run.then(
      () => callback(),
      (err) => {
        this.#onError(err);
        callback();
      },
    );
  }

  override _final(callback: (error?: Error | null) => void): void {
    this.#runExclusive(async () => {
      await this.#closeStream();
      this.#clearTimer();
    }).then(
      () => callback(),
      (err) => {
        this.#onError(err);
        callback();
      },
    );
  }

  override _destroy(
    error: Error | null,
    callback: (error: Error | null) => void,
  ): void {
    this.#clearTimer();
    this.#closeStream().then(
      () => callback(error),
      (closeErr) => callback(closeErr ?? error),
    );
  }

  #initialize(): void {
    try {
      mkdirSync(this.#dir, { recursive: true });
      const today = toISODate(new Date());
      const exists = existsSync(this.#basePath);
      if (exists) {
        const { mtime } = statSync(this.#basePath);
        const mtimeDate = toISODate(mtime);
        if (mtimeDate === today) {
          this.#open(today);
        } else if (mtimeDate >= this.#cutoffDate(today)) {
          this.#archiveCurrentFile(mtimeDate);
          this.#open(today);
        } else {
          unlinkSync(this.#basePath);
          this.#open(today);
        }
      } else {
        this.#open(today);
      }
      this.#pruneOldArchives();
      this.#scheduleNextRotation();
    } catch (err) {
      this.#onError(err);
      this.#stream = null;
      this.#currentDate = null;
    }
  }

  #open(date: string): void {
    const fd = openSync(this.#basePath, "a");
    this.#stream = createWriteStream(this.#basePath, { fd, autoClose: true });
    this.#currentDate = date;
    this.#stream.on("error", (err) => this.#onError(err));
  }

  #archiveCurrentFile(date: string): void {
    let archivePath = join(this.#dir, `${this.#baseName}-${date}${this.#ext}`);
    if (existsSync(archivePath)) {
      let count = 1;
      do {
        archivePath = join(
          this.#dir,
          `${this.#baseName}-${date}.${count}${this.#ext}`,
        );
        count += 1;
      } while (existsSync(archivePath));
    }
    renameSync(this.#basePath, archivePath);
  }

  #pruneOldArchives(): void {
    const cutoff = this.#cutoffDate(toISODate(new Date()));
    const activeName = basename(this.#basePath);
    const regex = new RegExp(
      `^${escapeRegExp(this.#baseName)}-(\\d{4}-\\d{2}-\\d{2})(?:\\.\\d+)?${
        escapeRegExp(this.#ext)
      }$`,
    );

    for (const name of readdirSync(this.#dir)) {
      if (name === activeName) continue;
      const match = regex.exec(name);
      if (!match) continue;
      const fileDate = match[1];
      if (!fileDate || fileDate < cutoff) {
        try {
          unlinkSync(join(this.#dir, name));
        } catch (err) {
          this.#onError(err);
        }
      }
    }
  }

  #cutoffDate(today: string): string {
    const startOfToday = new Date(`${today}T00:00:00.000Z`);
    const cutoff = addDays(startOfToday, -(this.#retentionDays - 1));
    return toISODate(cutoff);
  }

  #scheduleNextRotation(): void {
    this.#clearTimer();
    const now = new Date();
    const next = new Date(now);
    next.setDate(next.getDate() + 1);
    next.setHours(0, 0, 0, 0);
    const ms = next.getTime() - now.getTime();
    this.#nextTimer = setTimeout(() => {
      this.#runExclusive(() => this.#rotate(toISODate(new Date()))).catch(
        (err) => this.#onError(err),
      );
    }, ms);
  }

  #clearTimer(): void {
    if (this.#nextTimer) {
      clearTimeout(this.#nextTimer);
      this.#nextTimer = null;
    }
  }

  async #rotate(today: string): Promise<void> {
    if (this.#currentDate === today && this.#stream) {
      this.#pruneOldArchives();
      this.#scheduleNextRotation();
      return;
    }
    await this.#closeStream();
    if (this.#currentDate && existsSync(this.#basePath)) {
      this.#archiveCurrentFile(this.#currentDate);
    }
    this.#open(today);
    this.#pruneOldArchives();
    this.#scheduleNextRotation();
  }

  #closeStream(): Promise<void> {
    const s = this.#stream;
    this.#stream = null;
    if (!s) return Promise.resolve();
    s.end();
    return new Promise<void>((resolve) => {
      let settled = false;
      const once = () => {
        if (settled) return;
        settled = true;
        resolve();
      };
      s.once("close", once);
      s.once("error", once);
      setTimeout(once, 1000);
    });
  }

  #runExclusive<T>(fn: () => Promise<T>): Promise<T> {
    const next = this.#tail.then(() => fn());
    this.#tail = next.then(
      () => undefined,
      () => undefined,
    );
    return next;
  }

  #onError(err: unknown): void {
    if (this.#destroyed) return;
    if (this.listenerCount("error") > 0) {
      this.emit("error", err);
    }
  }
}
