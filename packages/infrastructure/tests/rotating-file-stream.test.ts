import {
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmdirSync,
  unlinkSync,
  utimesSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RotatingFileStream } from "../src/logging/rotating-file-stream.js";

function makeTempDir(): string {
  return mkdtempSync(join(tmpdir(), "rfs-"));
}

function cleanupDir(dir: string): void {
  try {
    for (const name of readdirSync(dir)) {
      unlinkSync(join(dir, name));
    }
    rmdirSync(dir);
  } catch {
    // best-effort cleanup
  }
}

function writeToStream(stream: RotatingFileStream, chunk: string): Promise<void> {
  return new Promise((resolve, reject) => {
    stream.write(chunk, (err: Error | null | undefined) => (err ? reject(err) : resolve()));
  });
}

function endStream(stream: RotatingFileStream): Promise<void> {
  return new Promise((resolve, reject) => {
    stream.end((err: Error | null | undefined) => (err ? reject(err) : resolve()));
  });
}

describe("RotatingFileStream", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("appends to the active log file", async () => {
    const dir = makeTempDir();
    const basePath = join(dir, "nusashell.log");
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-31T12:00:00.000Z"));

    const stream = new RotatingFileStream(basePath, { retentionDays: 3 });
    await writeToStream(stream, "line1\n");
    await endStream(stream);

    expect(readFileSync(basePath, "utf8")).toBe("line1\n");
    expect(readdirSync(dir).sort()).toEqual(["nusashell.log"]);

    cleanupDir(dir);
  });

  it("rotates the active file and creates a dated archive when the day changes", async () => {
    const dir = makeTempDir();
    const basePath = join(dir, "nusashell.log");
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-31T23:59:59.000Z"));

    const stream = new RotatingFileStream(basePath, { retentionDays: 3 });
    await writeToStream(stream, "day-31\n");

    vi.setSystemTime(new Date("2026-08-01T00:00:01.000Z"));
    await writeToStream(stream, "day-01\n");
    await endStream(stream);

    expect(readdirSync(dir).sort()).toEqual([
      "nusashell-2026-07-31.log",
      "nusashell.log",
    ]);
    expect(readFileSync(join(dir, "nusashell-2026-07-31.log"), "utf8")).toBe(
      "day-31\n",
    );
    expect(readFileSync(basePath, "utf8")).toBe("day-01\n");

    cleanupDir(dir);
  });

  it("prunes archives older than retention days on startup", () => {
    const dir = makeTempDir();
    const basePath = join(dir, "nusashell.log");
    const today = new Date("2026-07-31T12:00:00.000Z");
    writeFileSync(basePath, "active\n");
    utimesSync(basePath, today, today);

    writeFileSync(join(dir, "nusashell-2026-07-25.log"), "old\n");
    writeFileSync(join(dir, "nusashell-2026-07-28.log"), "three-days-ago\n");
    writeFileSync(join(dir, "nusashell-2026-07-29.log"), "two-days-ago\n");
    writeFileSync(join(dir, "nusashell-2026-07-30.log"), "yesterday\n");

    vi.useFakeTimers();
    vi.setSystemTime(today);

    const stream = new RotatingFileStream(basePath, { retentionDays: 3 });
    stream.destroy();

    expect(readdirSync(dir).sort()).toEqual([
      "nusashell-2026-07-29.log",
      "nusashell-2026-07-30.log",
      "nusashell.log",
    ]);

    cleanupDir(dir);
  });

  it("archives an old active file within retention on startup", () => {
    const dir = makeTempDir();
    const basePath = join(dir, "nusashell.log");
    const today = new Date("2026-07-31T12:00:00.000Z");
    const yesterday = new Date("2026-07-30T10:00:00.000Z");

    writeFileSync(basePath, "yesterday\n");
    utimesSync(basePath, yesterday, yesterday);
    writeFileSync(join(dir, "nusashell-2026-07-29.log"), "two-days-ago\n");

    vi.useFakeTimers();
    vi.setSystemTime(today);

    const stream = new RotatingFileStream(basePath, { retentionDays: 3 });
    stream.destroy();

    expect(readdirSync(dir).sort()).toEqual([
      "nusashell-2026-07-29.log",
      "nusashell-2026-07-30.log",
      "nusashell.log",
    ]);
    expect(readFileSync(join(dir, "nusashell-2026-07-30.log"), "utf8")).toBe(
      "yesterday\n",
    );
    expect(readFileSync(basePath, "utf8")).toBe("");

    cleanupDir(dir);
  });

  it("deletes an active file older than retention on startup", () => {
    const dir = makeTempDir();
    const basePath = join(dir, "nusashell.log");
    const today = new Date("2026-07-31T12:00:00.000Z");
    const old = new Date("2026-07-25T10:00:00.000Z");

    writeFileSync(basePath, "ancient\n");
    utimesSync(basePath, old, old);

    vi.useFakeTimers();
    vi.setSystemTime(today);

    const stream = new RotatingFileStream(basePath, { retentionDays: 3 });
    stream.destroy();

    expect(readdirSync(dir)).toEqual(["nusashell.log"]);
    expect(readFileSync(basePath, "utf8")).toBe("");

    cleanupDir(dir);
  });

  it("uses a numbered suffix when an archive for the same date already exists", () => {
    const dir = makeTempDir();
    const basePath = join(dir, "nusashell.log");
    const today = new Date("2026-07-31T12:00:00.000Z");
    const yesterday = new Date("2026-07-30T10:00:00.000Z");

    writeFileSync(basePath, "second\n");
    utimesSync(basePath, yesterday, yesterday);
    writeFileSync(join(dir, "nusashell-2026-07-30.log"), "first\n");

    vi.useFakeTimers();
    vi.setSystemTime(today);

    const stream = new RotatingFileStream(basePath, { retentionDays: 3 });
    stream.destroy();

    expect(readdirSync(dir).sort()).toEqual([
      "nusashell-2026-07-30.1.log",
      "nusashell-2026-07-30.log",
      "nusashell.log",
    ]);
    expect(readFileSync(join(dir, "nusashell-2026-07-30.1.log"), "utf8")).toBe(
      "second\n",
    );
    expect(readFileSync(join(dir, "nusashell-2026-07-30.log"), "utf8")).toBe(
      "first\n",
    );

    cleanupDir(dir);
  });
});
