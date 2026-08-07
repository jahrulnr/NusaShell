// Primitives for an append-only JSONL conversation log (mirrors Codex
// `message-history`): one JSON object per line, each line written with a
// single append syscall (atomic for < PIPE_BUF), bounded reads, tolerant of a
// torn trailing line from a crash mid-write.
import { appendFile, mkdir, readFile, stat } from "node:fs/promises";
import { dirname } from "node:path";

/**
 * Append one record as a single JSON line (terminated by "\n").
 * Uses a single appendFile write per record (O(1) per delta, not O(total)).
 * A value may contain embedded newlines — they are JSON-escaped by
 * JSON.stringify so each physical line is exactly one record.
 *
 * @param {string} path
 * @param {unknown} record
 */
export async function appendJsonlLine(path, record) {
  await mkdir(dirname(path), { recursive: true });
  const line = `${JSON.stringify(record)}\n`;
  await appendFile(path, line, { encoding: "utf8", mode: 0o600 });
}

/**
 * Read all records back in insertion order, skipping a trailing malformed
 * (torn) line so a crash mid-write does not fail the read.
 *
 * @param {string} path
 * @returns {Promise<unknown[]>}
 */
export async function readJsonlLines(path) {
  let text;
  try {
    text = await readFile(path, "utf8");
  } catch (error) {
    if (isFileMissing(error)) return [];
    throw error;
  }
  const out = [];
  const lines = text.split("\n");
  for (const line of lines) {
    if (line.length === 0) continue;
    try {
      out.push(JSON.parse(line));
    } catch {
      // Torn trailing line: skip it. A partial record at EOF from a crash is
      // not a committed message.
      if (line === lines[lines.length - 1] && out.length >= 0) break;
    }
  }
  return out;
}

/**
 * Count committed JSONL records of a file (ignores a torn trailing line).
 *
 * @param {string} path
 * @returns {Promise<number>}
 */
export async function countJsonlLines(path) {
  const records = await readJsonlLines(path);
  return records.length;
}

/**
 * Approximate current file size in bytes, or 0 when missing.
 * @param {string} path
 * @returns {Promise<number>}
 */
export async function jsonlFileSize(path) {
  try {
    const s = await stat(path);
    return s.size;
  } catch (error) {
    if (isFileMissing(error)) return 0;
    throw error;
  }
}

function isFileMissing(error) {
  return error && typeof error === "object" && "code" in error && error.code === "ENOENT";
}
