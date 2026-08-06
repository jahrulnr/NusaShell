#!/usr/bin/env node
// Token-efficiency telemetry report / exporter for NusaShell.
//
// Reads the append-only JSONL telemetry written by JsonlTelemetryWriter
// (`provider-requests-*.jsonl` + `agent-turns-*.jsonl`) and computes the
// turn-level metrics from the proposal (cache hit rate, fresh tokens per
// completed turn, provider requests per turn, failure waste, etc.).
//
// Usage:
//   node scripts/telemetry-report.mjs <telemetryDir> [--format json|csv]
//   node scripts/telemetry-report.mjs <telemetryDir> --format csv > turns.csv
//
// The metric functions are exported for unit testing; the CLI only runs when
// invoked directly.

import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

/** Parse newline-delimited JSON, skipping blank and malformed lines. */
export function parseJsonl(text) {
  const records = [];
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    try {
      records.push(JSON.parse(trimmed));
    } catch {
      // Skip partial/corrupt trailing lines rather than failing the report.
    }
  }
  return records;
}

/** Split a mixed record list into provider requests and turns. */
export function partitionRecords(records) {
  const providerRequests = [];
  const turns = [];
  for (const record of records) {
    if (record && record.kind === "provider_request") providerRequests.push(record);
    else if (record && record.kind === "agent_turn") turns.push(record);
  }
  return { providerRequests, turns };
}

function freshOf(usage) {
  if (!usage) return 0;
  return Math.max(0, (usage.inputTokens ?? 0) - (usage.cachedInputTokens ?? 0));
}

function sum(values) {
  return values.reduce((total, value) => total + value, 0);
}

function median(values) {
  return percentile(values, 50);
}

function percentile(values, p) {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const rank = (p / 100) * (sorted.length - 1);
  const low = Math.floor(rank);
  const high = Math.ceil(rank);
  if (low === high) return sorted[low];
  const weight = rank - low;
  return sorted[low] * (1 - weight) + sorted[high] * weight;
}

function ratio(numerator, denominator) {
  return denominator > 0 ? numerator / denominator : 0;
}

/**
 * Compute the turn-level efficiency report from parsed telemetry records.
 * Pure: takes already-parsed arrays so it can be unit-tested without fs.
 */
export function computeReport({ providerRequests = [], turns = [] } = {}) {
  const byStatus = { completed: 0, failed: 0, cancelled: 0, superseded: 0 };
  for (const turn of turns) {
    if (turn.status in byStatus) byStatus[turn.status] += 1;
  }
  const completedTurns = turns.filter((turn) => turn.status === "completed");

  // Provider-request token totals (source of truth for cache hit rate).
  const withUsage = providerRequests.filter((request) => request.usage);
  const inputTokens = sum(withUsage.map((request) => request.usage.inputTokens ?? 0));
  const cachedInputTokens = sum(withUsage.map((request) => request.usage.cachedInputTokens ?? 0));
  const outputTokens = sum(withUsage.map((request) => request.usage.outputTokens ?? 0));
  const reasoningOutputTokens = sum(withUsage.map((request) => request.usage.reasoningOutputTokens ?? 0));
  const freshInputTokens = inputTokens - cachedInputTokens;

  // Provider requests grouped per trace (≈ per turn) for amplification stats.
  const requestsPerTrace = new Map();
  for (const request of providerRequests) {
    requestsPerTrace.set(request.traceId, (requestsPerTrace.get(request.traceId) ?? 0) + 1);
  }
  const perTraceCounts = [...requestsPerTrace.values()];

  // Turn-level token usage for failure waste + fresh-tokens-per-turn.
  const turnTotalTokens = (turn) =>
    (turn.usage?.inputTokens ?? 0) + (turn.usage?.outputTokens ?? 0);
  const totalTurnTokens = sum(turns.map(turnTotalTokens));
  const wastedTurnTokens = sum(
    turns.filter((turn) => turn.status !== "completed").map(turnTotalTokens),
  );
  const freshPerCompleted = completedTurns.map((turn) => freshOf(turn.usage));
  const roundsPerTurn = turns.map((turn) => turn.rounds ?? 0);

  return {
    generatedAt: new Date().toISOString(),
    providerRequests: providerRequests.length,
    turns: turns.length,
    turnsByStatus: byStatus,
    tokens: {
      inputTokens,
      cachedInputTokens,
      freshInputTokens,
      outputTokens,
      reasoningOutputTokens,
    },
    cacheHitRate: ratio(cachedInputTokens, inputTokens),
    freshTokenRatio: ratio(freshInputTokens, inputTokens),
    providerRequestsPerTurn: ratio(providerRequests.length, turns.length),
    providerRequestsPerCompletedTurn: ratio(providerRequests.length, completedTurns.length),
    providerRequestsPerTraceMedian: median(perTraceCounts),
    providerRequestsPerTraceP95: percentile(perTraceCounts, 95),
    roundsPerTurnMedian: median(roundsPerTurn),
    roundsPerTurnP95: percentile(roundsPerTurn, 95),
    freshTokensPerCompletedTurn: ratio(sum(freshPerCompleted), completedTurns.length),
    // Cost is not captured by the current provider adapters (usage only), so
    // cost-per-turn is reported as null until cost passthrough lands.
    costPerCompletedTurn: null,
    failureWasteRatio: ratio(wastedTurnTokens, totalTurnTokens),
  };
}

const TURN_CSV_COLUMNS = [
  "traceId",
  "conversationId",
  "status",
  "startedAt",
  "completedAt",
  "durationMs",
  "providerId",
  "model",
  "rounds",
  "toolCalls",
  "toolsSucceeded",
  "toolsFailed",
  "compactionCount",
  "inputTokens",
  "cachedInputTokens",
  "freshInputTokens",
  "outputTokens",
  "reasoningOutputTokens",
];

function csvCell(value) {
  const text = value === undefined || value === null ? "" : String(value);
  return /[",\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

/** Render the turn records as CSV (header + one row per turn). */
export function turnsToCsv(turns) {
  const rows = [TURN_CSV_COLUMNS.join(",")];
  for (const turn of turns) {
    rows.push([
      turn.traceId,
      turn.conversationId,
      turn.status,
      turn.startedAt,
      turn.completedAt,
      turn.durationMs,
      turn.providerId,
      turn.model,
      turn.rounds,
      turn.tools?.calls,
      turn.tools?.succeeded,
      turn.tools?.failed,
      turn.compaction?.count,
      turn.usage?.inputTokens,
      turn.usage?.cachedInputTokens,
      freshOf(turn.usage),
      turn.usage?.outputTokens,
      turn.usage?.reasoningOutputTokens,
    ].map(csvCell).join(","));
  }
  return rows.join("\n");
}

/** Read + parse every telemetry JSONL file in a directory. */
export async function loadTelemetryDir(dir) {
  const entries = await readdir(dir);
  const files = entries.filter((name) => /\.jsonl$/.test(name));
  const records = [];
  for (const name of files) {
    const text = await readFile(join(dir, name), "utf8");
    records.push(...parseJsonl(text));
  }
  return partitionRecords(records);
}

async function main(argv) {
  const args = argv.slice(2);
  const dir = args.find((arg) => !arg.startsWith("--"));
  const formatIndex = args.indexOf("--format");
  const format = formatIndex !== -1 ? args[formatIndex + 1] : "json";
  if (!dir) {
    process.stderr.write("Usage: node scripts/telemetry-report.mjs <telemetryDir> [--format json|csv]\n");
    process.exitCode = 1;
    return;
  }
  const data = await loadTelemetryDir(dir);
  if (format === "csv") {
    process.stdout.write(`${turnsToCsv(data.turns)}\n`);
    return;
  }
  process.stdout.write(`${JSON.stringify(computeReport(data), null, 2)}\n`);
}

const invokedPath = process.argv[1] ? pathToFileURL(process.argv[1]).href : "";
if (import.meta.url === invokedPath) {
  main(process.argv).catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}
