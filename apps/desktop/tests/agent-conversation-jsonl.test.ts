import { appendFile, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
  appendJsonlLine,
  readJsonlLines,
  countJsonlLines,
} from "../src/main/agent-conversation-jsonl.js";

const dirs: string[] = [];
async function tmp(): Promise<string> {
  const d = await mkdtemp(join(tmpdir(), "ns-jsonl-"));
  dirs.push(d);
  return d;
}
afterEach(async () => {
  while (dirs.length) {
    await rm(dirs.pop()!, { recursive: true, force: true });
  }
});

describe("agent-conversation-jsonl primitives (Codex-style append log)", () => {
  it("appends one JSON object per line and reads them back in order", async () => {
    const d = await tmp();
    const p = join(d, "conv.jsonl");
    await appendJsonlLine(p, { role: "user", content: "hello" });
    await appendJsonlLine(p, { role: "assistant", content: "hi", model: "gpt" });

    const lines = await readJsonlLines(p);
    expect(lines).toHaveLength(2);
    expect(lines[0]).toEqual({ role: "user", content: "hello" });
    expect(lines[1]).toEqual({ role: "assistant", content: "hi", model: "gpt" });
  });

  it("keeps each record on its own line (one write per record)", async () => {
    const d = await tmp();
    const p = join(d, "conv.jsonl");
    await appendJsonlLine(p, { a: 1 });
    await appendJsonlLine(p, { b: "line\nbreak", c: { nested: true } });

    const raw = await readFile(p, "utf8");
    // 2 records -> exactly 2 newline-terminated lines.
    expect(raw.split("\n").filter((l) => l.length > 0)).toHaveLength(2);
    // Embedded newline inside a value must be JSON-escaped, not raw.
    expect(raw).toContain("\\n");
    expect(raw).not.toContain('"line\nbreak"');
  });

  it("returns an empty array for a missing file", async () => {
    const d = await tmp();
    expect(await readJsonlLines(join(d, "missing.jsonl"))).toEqual([]);
    expect(await countJsonlLines(join(d, "missing.jsonl"))).toBe(0);
  });

  it("tolerates a trailing malformed/partial line by skipping it (crash-safe tail)", async () => {
    const d = await tmp();
    const p = join(d, "conv.jsonl");
    await appendJsonlLine(p, { role: "user", content: "ok" });
    // Simulate a torn write: a partial non-JSON line at the end (no trailing newline).
    await appendFile(p, '{"role":"assistant","content":"partial"', "utf8");

    const lines = await readJsonlLines(p);
    expect(lines).toHaveLength(1);
    expect(lines[0]).toEqual({ role: "user", content: "ok" });
  });
});
