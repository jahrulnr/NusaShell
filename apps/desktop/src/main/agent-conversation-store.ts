import { randomUUID } from "node:crypto";
import { mkdir, readdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import {
  appendJsonlLine,
  jsonlFileSize,
  readJsonlLines,
} from "./agent-conversation-jsonl.js";
import type {
  AgentCanvasArtifact,
  AgentCanvasArtifactKind,
  AgentConversation,
  AgentConversationAcp,
  AgentConversationCheckpoint,
  AgentConversationKind,
  AgentConversationMessage,
  AgentConversationModelBinding,
  AgentConversationStep,
  AgentConversationToolCall,
  AgentConversationSummary,
  AgentSubagentRun,
  AgentSubagentRunStatus,
  AgentSubagentStreamStep,
} from "../shared/agent-conversation-contract.js";

/** Default per-conversation message JSONL cap (~8 MiB), Codex-style soft trim target. */
const DEFAULT_MAX_BYTES = 8 * 1024 * 1024;
const HISTORY_SOFT_CAP_RATIO = 0.8;

const CANVAS_ARTIFACT_MAX_COUNT = 20;
const CANVAS_ARTIFACT_MAX_TOTAL_BYTES = 3 * 1024 * 1024;
const CANVAS_ARTIFACT_MAX_SOURCE_BYTES = 512 * 1024;
const SUBAGENT_RUN_MAX_COUNT = 50;

const LEGACY_LOCK = "__legacy__";
const LIST_LOCK = "__list__";

/** Small metadata object stored as `<id>.meta.json` (not the message history). */
interface ConversationMeta {
  readonly id: string;
  readonly title: string;
  readonly createdAt: string;
  readonly updatedAt: string;
  readonly messageCount: number;
  readonly kind?: AgentConversationKind;
  readonly acp?: AgentConversationAcp;
  readonly workspace?: string;
  readonly model?: AgentConversationModelBinding;
  readonly checkpoint?: AgentConversationCheckpoint;
  readonly activeCanvasArtifactId?: string;
  readonly activeSubagentRunId?: string;
}

export class AgentConversationStore {
  private readonly conversationsDir: string;
  private readonly locks = new Map<string, Promise<unknown>>();
  private legacyReady = false;

  constructor(
    private readonly path: string,
    private readonly now: () => Date = () => new Date(),
    private readonly createId: () => string = () => `conv_${randomUUID()}`,
    /** Optional max JSONL size in bytes; default ~8 MiB. Used to trim oldest messages. */
    private readonly maxBytes: number = DEFAULT_MAX_BYTES,
  ) {
    this.conversationsDir = join(dirname(path), "conversations");
  }

  async list(): Promise<readonly AgentConversationSummary[]> {
    return this.withLock(LIST_LOCK, async () => {
      await this.ensureLegacyMigrated();
      const ids = await this.listConversationIds();
      const summaries: AgentConversationSummary[] = [];
      for (const id of ids) {
        const meta = await this.readMeta(id);
        if (!meta) continue;
        summaries.push(metaToSummary(meta));
      }
      return summaries.sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
    });
  }

  async create(options?: { kind?: AgentConversationKind; acp?: AgentConversationAcp }): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    const id = this.createId();
    return this.withLock(id, async () => {
      const timestamp = this.now().toISOString();
      const title = options?.kind === "acp" ? "New ACP conversation" : "New conversation";
      const meta: ConversationMeta = {
        id,
        title,
        createdAt: timestamp,
        updatedAt: timestamp,
        messageCount: 0,
        ...(options?.kind ? { kind: options.kind } : {}),
        ...(options?.acp ? { acp: options.acp } : {}),
      };
      await this.writeMeta(meta);
      return assemblyConversation(meta, [], [], []);
    });
  }

  async get(id: string): Promise<AgentConversation | null> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => this.loadConversation(id));
  }

  async appendMessage(id: string, message: AgentConversationMessage): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => {
      const meta = await this.requireMeta(id);
      const timestamp = this.now().toISOString();
      const savedMessage = { ...clampResumeMessages(message), createdAt: message.createdAt ?? timestamp };
      const title = meta.messageCount === 0 && message.role === "user"
        ? conversationTitle(message.content)
        : meta.title;
      await appendJsonlLine(this.messagesPath(id), savedMessage);
      let messageCount = meta.messageCount + 1;
      const trimmed = await this.trimMessagesIfNeeded(id);
      if (trimmed !== null) messageCount = trimmed;
      const nextMeta: ConversationMeta = {
        ...meta,
        title,
        updatedAt: timestamp,
        messageCount,
      };
      await this.writeMeta(nextMeta);
      return this.mustLoadConversation(id, nextMeta);
    });
  }

  async saveCheckpoint(id: string, checkpoint: AgentConversationCheckpoint): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => {
      const meta = await this.requireMeta(id);
      const nextMeta: ConversationMeta = {
        ...meta,
        updatedAt: this.now().toISOString(),
        checkpoint: {
          ...checkpoint,
          compactedMessageCount: Math.min(meta.messageCount, Math.max(0, checkpoint.compactedMessageCount)),
          ...(Number.isInteger(checkpoint.compactionCount) && (checkpoint.compactionCount ?? -1) >= 0
            ? { compactionCount: checkpoint.compactionCount }
            : {}),
        },
      };
      await this.writeMeta(nextMeta);
      return this.mustLoadConversation(id, nextMeta);
    });
  }

  async replaceLastInterrupted(id: string, message: AgentConversationMessage): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => {
      const meta = await this.requireMeta(id);
      const messages = await this.readMessages(id);
      const last = messages.at(-1);
      if (!last || last.role !== "assistant" || last.status !== "interrupted") {
        throw new Error("Last message is not an interrupted assistant message");
      }
      const savedMessage = { ...clampResumeMessages(message), createdAt: message.createdAt ?? this.now().toISOString() };
      const nextMessages = [...messages.slice(0, -1), savedMessage];
      await this.rewriteMessages(id, nextMessages);
      const nextMeta: ConversationMeta = {
        ...meta,
        updatedAt: this.now().toISOString(),
        messageCount: nextMessages.length,
      };
      await this.writeMeta(nextMeta);
      return this.mustLoadConversation(id, nextMeta);
    });
  }

  async delete(id: string): Promise<void> {
    await this.ensureLegacyMigrated();
    await this.withLock(id, async () => {
      await Promise.all([
        rm(this.metaPath(id), { force: true }),
        rm(this.messagesPath(id), { force: true }),
        rm(this.artifactsPath(id), { force: true }),
        rm(this.subagentsPath(id), { force: true }),
        rm(`${this.metaPath(id)}.tmp`, { force: true }),
        rm(`${this.artifactsPath(id)}.tmp`, { force: true }),
        rm(`${this.subagentsPath(id)}.tmp`, { force: true }),
        rm(`${this.messagesPath(id)}.tmp`, { force: true }),
      ]);
    });
  }

  async setWorkspace(id: string, workspace: string): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => {
      const meta = await this.requireMeta(id);
      const { workspace: _oldWs, ...rest } = meta;
      const nextMeta: ConversationMeta = {
        ...rest,
        ...(workspace ? { workspace } : {}),
        updatedAt: this.now().toISOString(),
      };
      await this.writeMeta(nextMeta);
      return this.mustLoadConversation(id, nextMeta);
    });
  }

  /**
   * Set (or clear) the per-conversation model binding (ticket #38).
   * Passing null clears the binding so the room falls back to the global model.
   */
  async setModel(id: string, model: AgentConversationModelBinding | null): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => {
      const meta = await this.requireMeta(id);
      const { model: _oldModel, ...rest } = meta;
      const nextMeta: ConversationMeta = {
        ...rest,
        ...(model ? { model } : {}),
        updatedAt: this.now().toISOString(),
      };
      await this.writeMeta(nextMeta);
      return this.mustLoadConversation(id, nextMeta);
    });
  }

  async upsertCanvasArtifact(id: string, artifact: AgentCanvasArtifact): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => {
      const meta = await this.requireMeta(id);
      if (artifact.conversationId !== id) {
        throw new Error("Canvas artifact conversationId does not match the conversation");
      }
      if (typeof artifact.source === "string" && artifact.source.length > CANVAS_ARTIFACT_MAX_SOURCE_BYTES) {
        throw new Error(`Canvas artifact source exceeds the ${CANVAS_ARTIFACT_MAX_SOURCE_BYTES} byte cap`);
      }
      const timestamp = this.now().toISOString();
      const existing = await this.readArtifacts(id);
      const without = existing.filter((item) => item.id !== artifact.id);
      const next = [...without, { ...artifact, updatedAt: timestamp }];
      const evicted = evictCanvasArtifacts(next, meta.activeCanvasArtifactId);
      await this.writeArtifacts(id, evicted);
      const nextMeta: ConversationMeta = { ...meta, updatedAt: timestamp };
      await this.writeMeta(nextMeta);
      return this.mustLoadConversation(id, nextMeta);
    });
  }

  async setActiveCanvasArtifact(id: string, artifactId: string | null): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => {
      const meta = await this.requireMeta(id);
      const timestamp = this.now().toISOString();
      const { activeCanvasArtifactId: _old, ...rest } = meta;
      const nextMeta: ConversationMeta = {
        ...rest,
        ...(artifactId ? { activeCanvasArtifactId: artifactId } : {}),
        updatedAt: timestamp,
      };
      await this.writeMeta(nextMeta);
      return this.mustLoadConversation(id, nextMeta);
    });
  }

  async upsertSubagentRun(id: string, run: AgentSubagentRun): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => {
      const meta = await this.requireMeta(id);
      if (run.conversationId !== id) {
        throw new Error("Subagent run conversationId does not match the conversation");
      }
      const timestamp = this.now().toISOString();
      const existing = await this.readSubagents(id);
      const without = existing.filter((item) => item.id !== run.id);
      const next = [...without, { ...run, updatedAt: timestamp }];
      const evicted = next.slice(-SUBAGENT_RUN_MAX_COUNT);
      await this.writeSubagents(id, evicted);
      const nextMeta: ConversationMeta = { ...meta, updatedAt: timestamp };
      await this.writeMeta(nextMeta);
      return this.mustLoadConversation(id, nextMeta);
    });
  }

  async setActiveSubagentRun(id: string, runId: string | null): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => {
      const meta = await this.requireMeta(id);
      const timestamp = this.now().toISOString();
      const { activeSubagentRunId: _old, ...rest } = meta;
      const nextMeta: ConversationMeta = {
        ...rest,
        ...(runId ? { activeSubagentRunId: runId } : {}),
        updatedAt: timestamp,
      };
      await this.writeMeta(nextMeta);
      return this.mustLoadConversation(id, nextMeta);
    });
  }

  async updateSubagentRunStatus(
    id: string,
    runId: string,
    status: AgentSubagentRunStatus,
    patch?: { summary?: string; error?: string; steps?: readonly AgentSubagentStreamStep[] },
  ): Promise<AgentConversation> {
    await this.ensureLegacyMigrated();
    return this.withLock(id, async () => {
      const meta = await this.requireMeta(id);
      const timestamp = this.now().toISOString();
      const runs = await this.readSubagents(id);
      const updated = runs.map((run) => {
        if (run.runId !== runId) return run;
        const { steps: _oldSteps, ...rest } = run;
        const nextSteps = patch?.steps !== undefined
          ? sanitizeSubagentSteps(patch.steps)
          : run.steps;
        return {
          ...rest,
          status,
          ...(patch?.summary !== undefined ? { summary: patch.summary } : {}),
          ...(patch?.error !== undefined ? { error: patch.error } : {}),
          ...(nextSteps?.length ? { steps: nextSteps } : {}),
          updatedAt: timestamp,
        };
      });
      await this.writeSubagents(id, updated);
      let nextMeta: ConversationMeta = { ...meta, updatedAt: timestamp };
      // Terminal statuses clear the active pointer when it matches this run so a
      // parent-turn abort does not leave activeSubagentRunId stuck on "running".
      const active = meta.activeSubagentRunId;
      const terminal = status === "ok" || status === "fail" || status === "cancelled";
      if (terminal && active === runId) {
        const { activeSubagentRunId: _drop, ...rest } = nextMeta;
        nextMeta = rest;
      }
      await this.writeMeta(nextMeta);
      return this.mustLoadConversation(id, nextMeta);
    });
  }

  /**
   * Per-conversation write lock (Codex-style). Concurrent ops on different
   * conversation ids do not wait on each other.
   */
  private async withLock<T>(key: string, operation: () => Promise<T>): Promise<T> {
    const previous = this.locks.get(key) ?? Promise.resolve();
    let output!: T;
    const run = previous.then(async () => {
      output = await operation();
    });
    this.locks.set(key, run.catch(() => undefined));
    await run;
    return output;
  }

  private async ensureLegacyMigrated(): Promise<void> {
    if (this.legacyReady) return;
    await this.withLock(LEGACY_LOCK, async () => {
      if (this.legacyReady) return;
      try {
        const raw = await readFile(this.path, "utf8");
        let parsed: unknown;
        try {
          parsed = JSON.parse(raw);
        } catch (error) {
          throw new Error("Could not load conversations", { cause: error });
        }
        let doc: { conversations: readonly AgentConversation[] };
        try {
          doc = normalizeDocument(parsed);
        } catch (error) {
          throw new Error("Could not load conversations", { cause: error });
        }
        await mkdir(this.conversationsDir, { recursive: true });
        for (const conversation of doc.conversations) {
          const existing = await this.readMeta(conversation.id);
          if (existing) continue;
          await this.materializeConversation(conversation);
        }
        await rename(this.path, `${this.path}.migrated`).catch((error) => {
          if (!isFileNotFound(error)) throw error;
        });
      } catch (error) {
        if (isFileNotFound(error)) {
          // No legacy monofile — pure new layout (or empty install).
        } else {
          throw error instanceof Error && error.message === "Could not load conversations"
            ? error
            : new Error("Could not load conversations", { cause: error });
        }
      }
      this.legacyReady = true;
    });
  }

  private async materializeConversation(conversation: AgentConversation): Promise<void> {
    const messageCount = conversation.messages.length;
    const meta: ConversationMeta = {
      id: conversation.id,
      title: conversation.title,
      createdAt: conversation.createdAt,
      updatedAt: conversation.updatedAt,
      messageCount,
      ...(conversation.kind ? { kind: conversation.kind } : {}),
      ...(conversation.acp ? { acp: conversation.acp } : {}),
      ...(conversation.workspace ? { workspace: conversation.workspace } : {}),
      ...(conversation.model ? { model: conversation.model } : {}),
      ...(conversation.checkpoint ? { checkpoint: conversation.checkpoint } : {}),
      ...(conversation.activeCanvasArtifactId ? { activeCanvasArtifactId: conversation.activeCanvasArtifactId } : {}),
      ...(conversation.activeSubagentRunId ? { activeSubagentRunId: conversation.activeSubagentRunId } : {}),
    };
    await this.writeMeta(meta);
    await this.rewriteMessages(conversation.id, conversation.messages);
    if (conversation.canvasArtifacts?.length) {
      await this.writeArtifacts(conversation.id, conversation.canvasArtifacts);
    }
    if (conversation.subagentRuns?.length) {
      await this.writeSubagents(conversation.id, conversation.subagentRuns);
    }
  }

  private async loadConversation(id: string): Promise<AgentConversation | null> {
    const meta = await this.readMeta(id);
    if (!meta) return null;
    return this.mustLoadConversation(id, meta);
  }

  private async mustLoadConversation(id: string, meta: ConversationMeta): Promise<AgentConversation> {
    const messages = await this.readMessages(id);
    const canvasArtifacts = await this.readArtifacts(id);
    const subagentRuns = await this.readSubagents(id);
    return assemblyConversation(meta, messages, canvasArtifacts, subagentRuns);
  }

  private async requireMeta(id: string): Promise<ConversationMeta> {
    const meta = await this.readMeta(id);
    if (!meta) throw new Error(`Conversation not found: ${id}`);
    return meta;
  }

  private async listConversationIds(): Promise<string[]> {
    try {
      const entries = await readdir(this.conversationsDir);
      return entries
        .filter((name) => name.endsWith(".meta.json") && !name.endsWith(".tmp"))
        .map((name) => basename(name, ".meta.json"));
    } catch (error) {
      if (isFileNotFound(error)) return [];
      throw error;
    }
  }

  private metaPath(id: string): string {
    return join(this.conversationsDir, `${id}.meta.json`);
  }

  private messagesPath(id: string): string {
    return join(this.conversationsDir, `${id}.jsonl`);
  }

  private artifactsPath(id: string): string {
    return join(this.conversationsDir, `${id}.artifacts.json`);
  }

  private subagentsPath(id: string): string {
    return join(this.conversationsDir, `${id}.subagents.json`);
  }

  private async readMeta(id: string): Promise<ConversationMeta | null> {
    try {
      const parsed: unknown = JSON.parse(await readFile(this.metaPath(id), "utf8"));
      return normalizeMeta(parsed, id);
    } catch (error) {
      if (isFileNotFound(error)) return null;
      throw new Error("Could not load conversations", { cause: error });
    }
  }

  private async writeMeta(meta: ConversationMeta): Promise<void> {
    await writeJsonAtomic(this.metaPath(meta.id), meta);
  }

  private async readMessages(id: string): Promise<AgentConversationMessage[]> {
    const lines = await readJsonlLines(this.messagesPath(id));
    return lines.flatMap((item) => {
      if (isConversationMessage(item)) return [item];
      const repaired = repairConversationMessage(item);
      return repaired ? [repaired] : [];
    });
  }

  private async rewriteMessages(id: string, messages: readonly AgentConversationMessage[]): Promise<void> {
    await mkdir(this.conversationsDir, { recursive: true });
    const body = messages.length === 0
      ? ""
      : `${messages.map((message) => JSON.stringify(message)).join("\n")}\n`;
    const target = this.messagesPath(id);
    const temporaryPath = `${target}.tmp`;
    await writeFile(temporaryPath, body, { mode: 0o600 });
    await rename(temporaryPath, target);
  }

  /**
   * Drop oldest messages until the JSONL is under the soft cap (0.8 * maxBytes).
   * Returns the new message count when trim ran, otherwise null.
   */
  private async trimMessagesIfNeeded(id: string): Promise<number | null> {
    if (this.maxBytes <= 0) return null;
    const size = await jsonlFileSize(this.messagesPath(id));
    if (size <= this.maxBytes) return null;
    const messages = await this.readMessages(id);
    if (messages.length <= 1) return messages.length;
    const target = Math.floor(this.maxBytes * HISTORY_SOFT_CAP_RATIO);
    let kept = [...messages];
    while (kept.length > 1) {
      const encoded = Buffer.byteLength(`${kept.map((m) => JSON.stringify(m)).join("\n")}\n`, "utf8");
      if (encoded <= target) break;
      kept.shift();
    }
    await this.rewriteMessages(id, kept);
    return kept.length;
  }

  private async readArtifacts(id: string): Promise<AgentCanvasArtifact[]> {
    try {
      const parsed: unknown = JSON.parse(await readFile(this.artifactsPath(id), "utf8"));
      if (!Array.isArray(parsed)) return [];
      return parsed.flatMap((entry) => {
        const artifact = normalizeCanvasArtifact(entry, id);
        return artifact ? [artifact] : [];
      });
    } catch (error) {
      if (isFileNotFound(error)) return [];
      throw error;
    }
  }

  private async writeArtifacts(id: string, artifacts: readonly AgentCanvasArtifact[]): Promise<void> {
    if (artifacts.length === 0) {
      await rm(this.artifactsPath(id), { force: true });
      return;
    }
    await writeJsonAtomic(this.artifactsPath(id), artifacts);
  }

  private async readSubagents(id: string): Promise<AgentSubagentRun[]> {
    try {
      const parsed: unknown = JSON.parse(await readFile(this.subagentsPath(id), "utf8"));
      if (!Array.isArray(parsed)) return [];
      return parsed.flatMap((entry) => {
        const run = normalizeSubagentRun(entry, id);
        return run ? [run] : [];
      });
    } catch (error) {
      if (isFileNotFound(error)) return [];
      throw error;
    }
  }

  private async writeSubagents(id: string, runs: readonly AgentSubagentRun[]): Promise<void> {
    if (runs.length === 0) {
      await rm(this.subagentsPath(id), { force: true });
      return;
    }
    await writeJsonAtomic(this.subagentsPath(id), runs);
  }
}

function assemblyConversation(
  meta: ConversationMeta,
  messages: readonly AgentConversationMessage[],
  canvasArtifacts: readonly AgentCanvasArtifact[],
  subagentRuns: readonly AgentSubagentRun[],
): AgentConversation {
  const activeCanvasArtifactId = meta.activeCanvasArtifactId
    && canvasArtifacts.some((artifact) => artifact.id === meta.activeCanvasArtifactId)
    ? meta.activeCanvasArtifactId
    : undefined;
  const activeSubagentRunId = meta.activeSubagentRunId
    && subagentRuns.some((run) => run.runId === meta.activeSubagentRunId)
    ? meta.activeSubagentRunId
    : undefined;
  return {
    id: meta.id,
    title: meta.title,
    createdAt: meta.createdAt,
    updatedAt: meta.updatedAt,
    messages,
    ...(meta.checkpoint ? { checkpoint: meta.checkpoint } : {}),
    ...(meta.workspace ? { workspace: meta.workspace } : {}),
    ...(meta.model ? { model: meta.model } : {}),
    ...(meta.kind ? { kind: meta.kind } : {}),
    ...(meta.acp ? { acp: meta.acp } : {}),
    ...(canvasArtifacts.length ? { canvasArtifacts } : {}),
    ...(activeCanvasArtifactId ? { activeCanvasArtifactId } : {}),
    ...(subagentRuns.length ? { subagentRuns } : {}),
    ...(activeSubagentRunId ? { activeSubagentRunId } : {}),
  };
}

function metaToSummary(meta: ConversationMeta): AgentConversationSummary {
  const {
    messageCount,
    checkpoint: _checkpoint,
    ...rest
  } = meta;
  return {
    ...rest,
    messageCount,
  };
}

function normalizeMeta(value: unknown, expectedId: string): ConversationMeta | null {
  if (typeof value !== "object" || value === null) return null;
  const candidate = value as Partial<ConversationMeta> & { id?: string };
  if (typeof candidate.id !== "string" || candidate.id !== expectedId) return null;
  if (typeof candidate.createdAt !== "string" || typeof candidate.updatedAt !== "string") return null;
  const messageCount = Number.isInteger(candidate.messageCount) && (candidate.messageCount ?? -1) >= 0
    ? (candidate.messageCount as number)
    : 0;
  return {
    id: candidate.id,
    title: typeof candidate.title === "string" ? candidate.title : "New conversation",
    createdAt: candidate.createdAt,
    updatedAt: candidate.updatedAt,
    messageCount,
    ...(isCheckpoint(candidate.checkpoint) ? { checkpoint: candidate.checkpoint } : {}),
    ...(typeof candidate.workspace === "string" && candidate.workspace ? { workspace: candidate.workspace } : {}),
    ...(isModelBinding(candidate.model) ? { model: candidate.model } : {}),
    ...(candidate.kind === "acp" || candidate.kind === "agent" ? { kind: candidate.kind } : {}),
    ...(isAcp(candidate.acp) ? { acp: candidate.acp } : {}),
    ...(typeof candidate.activeCanvasArtifactId === "string" ? { activeCanvasArtifactId: candidate.activeCanvasArtifactId } : {}),
    ...(typeof candidate.activeSubagentRunId === "string" ? { activeSubagentRunId: candidate.activeSubagentRunId } : {}),
  };
}

async function writeJsonAtomic(path: string, value: unknown): Promise<void> {
  await mkdir(dirname(path), { recursive: true });
  const temporaryPath = `${path}.tmp`;
  await writeFile(temporaryPath, JSON.stringify(value, null, 2), { mode: 0o600 });
  await rename(temporaryPath, path);
}

/** Legacy monofile shape (version 1/2) — used only for one-time migration. */
function normalizeDocument(value: unknown): { version: 2; conversations: readonly AgentConversation[] } {
  if (typeof value !== "object" || value === null || !Array.isArray((value as { conversations?: unknown }).conversations)) {
    throw new Error("Conversation file has an invalid shape");
  }
  const conversations = (value as { conversations: unknown[] }).conversations.flatMap((item) => {
    if (typeof item !== "object" || item === null) return [];
    const candidate = item as Partial<AgentConversation>;
    if (typeof candidate.id !== "string" || typeof candidate.createdAt !== "string" || typeof candidate.updatedAt !== "string") return [];
    const conversationId = candidate.id;
    const messages = Array.isArray(candidate.messages)
      ? candidate.messages.flatMap((entry) => {
          if (isConversationMessage(entry)) return [entry];
          const repaired = repairConversationMessage(entry);
          return repaired ? [repaired] : [];
        })
      : [];
    const canvasArtifacts = Array.isArray(candidate.canvasArtifacts)
      ? candidate.canvasArtifacts.flatMap((entry) => {
          const artifact = normalizeCanvasArtifact(entry, conversationId);
          return artifact ? [artifact] : [];
        })
      : [];
    const activeCanvasArtifactId = typeof candidate.activeCanvasArtifactId === "string"
      && canvasArtifacts.some((artifact) => artifact.id === candidate.activeCanvasArtifactId)
        ? candidate.activeCanvasArtifactId
        : undefined;
    const subagentRuns = Array.isArray(candidate.subagentRuns)
      ? candidate.subagentRuns.flatMap((entry) => {
          const run = normalizeSubagentRun(entry, conversationId);
          return run ? [run] : [];
        })
      : [];
    const activeSubagentRunId = typeof candidate.activeSubagentRunId === "string"
      && subagentRuns.some((run) => run.runId === candidate.activeSubagentRunId)
        ? candidate.activeSubagentRunId
        : undefined;
    return [{
      id: candidate.id,
      title: typeof candidate.title === "string" ? candidate.title : "New conversation",
      createdAt: candidate.createdAt,
      updatedAt: candidate.updatedAt,
      messages,
      ...(isCheckpoint(candidate.checkpoint) ? { checkpoint: candidate.checkpoint } : {}),
      ...(typeof candidate.workspace === "string" && candidate.workspace ? { workspace: candidate.workspace } : {}),
      ...(isModelBinding(candidate.model) ? { model: candidate.model } : {}),
      ...(candidate.kind === "acp" || candidate.kind === "agent" ? { kind: candidate.kind } : {}),
      ...(isAcp(candidate.acp) ? { acp: candidate.acp } : {}),
      ...(canvasArtifacts.length ? { canvasArtifacts } : {}),
      ...(activeCanvasArtifactId ? { activeCanvasArtifactId } : {}),
      ...(subagentRuns.length ? { subagentRuns } : {}),
      ...(activeSubagentRunId ? { activeSubagentRunId } : {}),
    }];
  });
  return { version: 2, conversations };
}

function normalizeCanvasArtifact(value: unknown, conversationId: string): AgentCanvasArtifact | null {
  if (typeof value !== "object" || value === null) return null;
  const record = value as Record<string, unknown>;
  if (typeof record.id !== "string"
    || typeof record.sourceMessageId !== "string"
    || typeof record.source !== "string"
    || typeof record.createdAt !== "string"
    || typeof record.updatedAt !== "string") {
    return null;
  }
  if (record.kind !== "html" && record.kind !== "svg" && record.kind !== "mermaid") return null;
  if (typeof record.fenceIndex !== "number" || !Number.isFinite(record.fenceIndex) || record.fenceIndex < 0) return null;
  if (record.source.length > CANVAS_ARTIFACT_MAX_SOURCE_BYTES) return null;
  const ownerConversationId = typeof record.conversationId === "string" ? record.conversationId : conversationId;
  if (ownerConversationId !== conversationId) return null;
  return {
    id: record.id,
    conversationId: conversationId,
    sourceMessageId: record.sourceMessageId,
    fenceIndex: record.fenceIndex,
    kind: record.kind as AgentCanvasArtifactKind,
    title: typeof record.title === "string" ? record.title : record.kind,
    source: record.source,
    createdAt: record.createdAt,
    updatedAt: record.updatedAt,
  };
}

function normalizeSubagentRun(value: unknown, conversationId: string): AgentSubagentRun | null {
  if (typeof value !== "object" || value === null) return null;
  const record = value as Record<string, unknown>;
  if (typeof record.id !== "string"
    || typeof record.runId !== "string"
    || typeof record.sourceMessageId !== "string"
    || typeof record.providerId !== "string"
    || typeof record.prompt !== "string"
    || typeof record.createdAt !== "string"
    || typeof record.updatedAt !== "string") {
    return null;
  }
  if (record.status !== "running" && record.status !== "ok" && record.status !== "fail" && record.status !== "cancelled") return null;
  const ownerConversationId = typeof record.conversationId === "string" ? record.conversationId : conversationId;
  if (ownerConversationId !== conversationId) return null;
  const attempted = Array.isArray(record.attempted)
    ? record.attempted.filter((item): item is string => typeof item === "string")
    : undefined;
  const steps = Array.isArray(record.steps)
    ? sanitizeSubagentSteps(record.steps)
    : undefined;
  return {
    id: record.id,
    conversationId,
    sourceMessageId: record.sourceMessageId,
    runId: record.runId,
    providerId: record.providerId,
    ...(typeof record.title === "string" ? { title: record.title } : {}),
    prompt: record.prompt,
    status: record.status as AgentSubagentRunStatus,
    ...(typeof record.summary === "string" ? { summary: record.summary } : {}),
    ...(typeof record.error === "string" ? { error: record.error } : {}),
    ...(attempted?.length ? { attempted } : {}),
    ...(steps?.length ? { steps } : {}),
    createdAt: record.createdAt,
    updatedAt: record.updatedAt,
  };
}

function sanitizeSubagentSteps(value: unknown): AgentSubagentStreamStep[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const steps: AgentSubagentStreamStep[] = [];
  for (const entry of value) {
    if (typeof entry !== "object" || entry === null) continue;
    const step = structuredClone(entry) as Record<string, unknown>;
    repairSubagentStepRecord(step);
    if (isConversationStep(step)) {
      steps.push(step);
      continue;
    }
    if (step.type === "plan" && Array.isArray(step.steps)) {
      const planSteps = step.steps.flatMap((item) => {
        if (typeof item !== "object" || item === null) return [];
        const plan = item as Record<string, unknown>;
        if (typeof plan.text !== "string") return [];
        return [{
          text: plan.text.slice(0, 4_000),
          ...(plan.done === true ? { done: true } : {}),
        }];
      });
      if (planSteps.length) steps.push({ type: "plan", steps: planSteps });
    }
  }
  return steps.length ? steps : undefined;
}

function repairSubagentStepRecord(step: Record<string, unknown>): void {
  repairStepRecord(step);
}

function evictCanvasArtifacts(artifacts: readonly AgentCanvasArtifact[], activeId?: string): readonly AgentCanvasArtifact[] {
  let list = [...artifacts].sort((left, right) => left.createdAt.localeCompare(right.createdAt));
  while (list.length > CANVAS_ARTIFACT_MAX_COUNT) {
    const removable = list.findIndex((artifact) => artifact.id !== activeId);
    if (removable === -1) break;
    list.splice(removable, 1);
  }
  while (list.reduce((total, artifact) => total + artifact.source.length, 0) > CANVAS_ARTIFACT_MAX_TOTAL_BYTES) {
    const removable = list.findIndex((artifact) => artifact.id !== activeId);
    if (removable === -1) break;
    list.splice(removable, 1);
  }
  return list;
}

function isFileNotFound(error: unknown): boolean {
  return typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT";
}

function isConversationMessage(value: unknown): value is AgentConversationMessage {
  if (typeof value !== "object" || value === null) return false;
  const message = value as Partial<AgentConversationMessage>;
  return (message.role === "user" || message.role === "assistant")
    && typeof message.content === "string"
    && (message.reasoning === undefined || (
      message.role === "assistant"
      && typeof message.reasoning === "string"
      && message.reasoning.length <= 1_000_000
    ))
    && (message.steps === undefined || (
      message.role === "assistant"
      && Array.isArray(message.steps)
      && message.steps.every(isConversationStep)
    ))
    && (message.attachments === undefined || (
      message.role === "user"
      && Array.isArray(message.attachments)
      && message.attachments.length <= 4
      && message.attachments.every(isConversationAttachment)
    ))
    && (message.status === undefined || (
      message.role === "assistant"
      && (message.status === "complete" || message.status === "interrupted")
    ))
    && (message.resumeMessages === undefined || (
      message.role === "assistant"
      && Array.isArray(message.resumeMessages)
    ));
}

function isConversationStep(value: unknown): value is AgentConversationStep {
  if (typeof value !== "object" || value === null) return false;
  const step = value as Record<string, unknown>;
  if (step.type === "reasoning" || step.type === "text") {
    return typeof step.content === "string" && step.content.length <= 1_000_000;
  }
  if (step.type === "tool_calls") {
    return Array.isArray(step.calls) && step.calls.every(isConversationToolCall);
  }
  return false;
}

function isConversationToolCall(value: unknown): value is AgentConversationToolCall {
  if (typeof value !== "object" || value === null) return false;
  const call = value as Record<string, unknown>;
  if (!(typeof call.id === "string"
    && typeof call.name === "string"
    && typeof call.ok === "boolean"
    && (call.error === undefined || typeof call.error === "string"))) {
    return false;
  }
  if (call.args !== undefined) {
    if (typeof call.args !== "object" || call.args === null || Array.isArray(call.args)) return false;
    try {
      if (JSON.stringify(call.args).length > 8_000) return false;
    } catch {
      return false;
    }
  }
  if (call.output !== undefined && (typeof call.output !== "string" || call.output.length > 12_000)) {
    return false;
  }
  return true;
}

/**
 * Salvage a persisted message whose only violations are over-length clamped
 * fields (e.g. tool output saved as 12_002 chars by an older clamp). Clamp
 * instead of dropping so a restart does not erase the visible chat history.
 */
function repairConversationMessage(value: unknown): AgentConversationMessage | null {
  if (typeof value !== "object" || value === null) return null;
  const message = structuredClone(value) as Record<string, unknown>;
  if (Array.isArray(message.toolCalls)) message.toolCalls.forEach(repairToolCallRecord);
  if (Array.isArray(message.steps)) message.steps.forEach(repairStepRecord);
  if (typeof message.reasoning === "string" && message.reasoning.length > 1_000_000) {
    message.reasoning = message.reasoning.slice(0, 1_000_000);
  }
  return isConversationMessage(message) ? message : null;
}

function repairStepRecord(step: unknown): void {
  if (typeof step !== "object" || step === null) return;
  const record = step as Record<string, unknown>;
  if ((record.type === "reasoning" || record.type === "text") && typeof record.content === "string" && record.content.length > 1_000_000) {
    record.content = record.content.slice(0, 1_000_000);
  }
  if (record.type === "tool_calls" && Array.isArray(record.calls)) record.calls.forEach(repairToolCallRecord);
}

function repairToolCallRecord(call: unknown): void {
  if (typeof call !== "object" || call === null) return;
  const record = call as Record<string, unknown>;
  if (typeof record.output === "string" && record.output.length > 12_000) {
    record.output = record.output.slice(0, 12_000);
  }
  const args = record.args;
  if (args !== undefined && typeof args === "object" && args !== null && !Array.isArray(args)) {
    try {
      if (JSON.stringify(args).length > 8_000) delete record.args;
    } catch {
      delete record.args;
    }
  }
}

function isConversationAttachment(value: unknown): boolean {
  if (typeof value !== "object" || value === null) return false;
  const attachment = value as Record<string, unknown>;
  const validBase = typeof attachment.name === "string"
    && attachment.name.length > 0
    && attachment.name.length <= 255
    && typeof attachment.mediaType === "string"
    && attachment.mediaType.length > 0;
  if (!validBase) return false;
  if (attachment.type === "text") {
    return typeof attachment.content === "string" && attachment.content.length <= 4_000_000;
  }
  return (attachment.type === "image" || attachment.type === "file")
    && typeof attachment.dataUrl === "string"
    && attachment.dataUrl.length <= 6_000_000
    && /^data:[^;,]+;base64,/i.test(attachment.dataUrl);
}

function isAcp(value: unknown): value is AgentConversationAcp {
  if (typeof value !== "object" || value === null) return false;
  const acp = value as Partial<AgentConversationAcp>;
  return typeof acp.providerId === "string" && acp.providerId.length > 0;
}

function isModelBinding(value: unknown): value is AgentConversationModelBinding {
  if (typeof value !== "object" || value === null) return false;
  const binding = value as Partial<AgentConversationModelBinding>;
  return typeof binding.modelKey === "string" && binding.modelKey.length > 0
    && typeof binding.effort === "string"
    && (binding.explicit === undefined || typeof binding.explicit === "boolean");
}

function isCheckpoint(value: unknown): value is AgentConversationCheckpoint {
  if (typeof value !== "object" || value === null) return false;
  const checkpoint = value as Partial<AgentConversationCheckpoint>;
  return typeof checkpoint.summary === "string"
    && Number.isInteger(checkpoint.compactedMessageCount)
    && (checkpoint.via === "provider" || checkpoint.via === "extractive")
    && (checkpoint.compactionCount === undefined || (Number.isInteger(checkpoint.compactionCount) && checkpoint.compactionCount >= 0));
}

function conversationTitle(content: string): string {
  const normalized = content.trim().replace(/\s+/g, " ");
  if (!normalized) return "New conversation";
  return normalized.length <= 60 ? normalized : `${normalized.slice(0, 57)}…`;
}

const RESUME_MESSAGES_MAX_BYTES = 512 * 1024;

/**
 * Drop `resumeMessages` when the serialized message exceeds the resume budget.
 * The interrupted assistant message keeps its `steps`/`toolCalls` for display,
 * but Retry falls back to a restart when the snapshot was too large to persist.
 */
function clampResumeMessages(message: AgentConversationMessage): AgentConversationMessage {
  if (!message.resumeMessages) return message;
  try {
    const serialized = JSON.stringify(message);
    if (serialized.length <= RESUME_MESSAGES_MAX_BYTES) return message;
  } catch {
    // fall through to drop
  }
  const { resumeMessages: _drop, ...rest } = message;
  return rest;
}
