import { randomUUID } from "node:crypto";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import type {
  AgentCanvasArtifact,
  AgentCanvasArtifactKind,
  AgentConversation,
  AgentConversationAcp,
  AgentConversationCheckpoint,
  AgentConversationKind,
  AgentConversationMessage,
  AgentConversationStep,
  AgentConversationToolCall,
  AgentConversationSummary,
  AgentSubagentRun,
  AgentSubagentRunStatus,
  AgentSubagentStreamStep,
} from "../shared/agent-conversation-contract.js";

interface ConversationDocument {
  readonly version: 2;
  readonly conversations: readonly AgentConversation[];
}

const CANVAS_ARTIFACT_MAX_COUNT = 20;
const CANVAS_ARTIFACT_MAX_TOTAL_BYTES = 3 * 1024 * 1024;
const CANVAS_ARTIFACT_MAX_SOURCE_BYTES = 512 * 1024;
const SUBAGENT_RUN_MAX_COUNT = 50;

export class AgentConversationStore {
  private state: ConversationDocument | null = null;
  private mutation = Promise.resolve();

  constructor(
    private readonly path: string,
    private readonly now: () => Date = () => new Date(),
    private readonly createId: () => string = () => `conv_${randomUUID()}`,
  ) {}

  async list(): Promise<readonly AgentConversationSummary[]> {
    const state = await this.load();
    return [...state.conversations]
      .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))
      .map(({ messages, checkpoint: _checkpoint, ...conversation }) => ({
        ...conversation,
        messageCount: messages.length,
      }));
  }

  async create(options?: { kind?: AgentConversationKind; acp?: AgentConversationAcp }): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const timestamp = this.now().toISOString();
      const title = options?.kind === "acp" ? "New ACP conversation" : "New conversation";
      const conversation: AgentConversation = {
        id: this.createId(),
        title,
        createdAt: timestamp,
        updatedAt: timestamp,
        messages: [],
        ...(options?.kind ? { kind: options.kind } : {}),
        ...(options?.acp ? { acp: options.acp } : {}),
      };
      return [{ ...state, conversations: [conversation, ...state.conversations] }, conversation];
    });
  }

  async get(id: string): Promise<AgentConversation | null> {
    const state = await this.load();
    return state.conversations.find((conversation) => conversation.id === id) ?? null;
  }

  async appendMessage(id: string, message: AgentConversationMessage): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const current = requireConversation(state, id);
      const timestamp = this.now().toISOString();
      const savedMessage = { ...clampResumeMessages(message), createdAt: message.createdAt ?? timestamp };
      const title = current.messages.length === 0 && message.role === "user"
        ? conversationTitle(message.content)
        : current.title;
      const updated: AgentConversation = {
        ...current,
        title,
        updatedAt: timestamp,
        messages: [...current.messages, savedMessage],
      };
      return [replaceConversation(state, updated), updated];
    });
  }

  async saveCheckpoint(id: string, checkpoint: AgentConversationCheckpoint): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const current = requireConversation(state, id);
      const updated: AgentConversation = {
        ...current,
        updatedAt: this.now().toISOString(),
        checkpoint: {
          ...checkpoint,
          compactedMessageCount: Math.min(current.messages.length, Math.max(0, checkpoint.compactedMessageCount)),
          ...(Number.isInteger(checkpoint.compactionCount) && checkpoint.compactionCount >= 0
            ? { compactionCount: checkpoint.compactionCount }
            : {}),
        },
      };
      return [replaceConversation(state, updated), updated];
    });
  }

  async replaceLastInterrupted(id: string, message: AgentConversationMessage): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const current = requireConversation(state, id);
      const last = current.messages.at(-1);
      if (!last || last.role !== "assistant" || last.status !== "interrupted") {
        throw new Error("Last message is not an interrupted assistant message");
      }
      const savedMessage = { ...clampResumeMessages(message), createdAt: message.createdAt ?? this.now().toISOString() };
      const updated: AgentConversation = {
        ...current,
        updatedAt: this.now().toISOString(),
        messages: [...current.messages.slice(0, -1), savedMessage],
      };
      return [replaceConversation(state, updated), updated];
    });
  }

  async delete(id: string): Promise<void> {
    await this.mutate(async (state) => [
      { ...state, conversations: state.conversations.filter((conversation) => conversation.id !== id) },
      undefined,
    ]);
  }

  async setWorkspace(id: string, workspace: string): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const current = requireConversation(state, id);
      const { workspace: _oldWs, ...rest } = current;
      const updated: AgentConversation = {
        ...rest,
        ...(workspace ? { workspace } : {}),
        updatedAt: this.now().toISOString(),
      };
      return [replaceConversation(state, updated), updated];
    });
  }

  async upsertCanvasArtifact(id: string, artifact: AgentCanvasArtifact): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const current = requireConversation(state, id);
      if (artifact.conversationId !== id) {
        throw new Error("Canvas artifact conversationId does not match the conversation");
      }
      if (typeof artifact.source === "string" && artifact.source.length > CANVAS_ARTIFACT_MAX_SOURCE_BYTES) {
        throw new Error(`Canvas artifact source exceeds the ${CANVAS_ARTIFACT_MAX_SOURCE_BYTES} byte cap`);
      }
      const timestamp = this.now().toISOString();
      const existing = current.canvasArtifacts ?? [];
      const without = existing.filter((item) => item.id !== artifact.id);
      const next = [...without, { ...artifact, updatedAt: timestamp }];
      const evicted = evictCanvasArtifacts(next, current.activeCanvasArtifactId);
      const updated: AgentConversation = {
        ...current,
        canvasArtifacts: evicted,
        updatedAt: timestamp,
      };
      return [replaceConversation(state, updated), updated];
    });
  }

  async setActiveCanvasArtifact(id: string, artifactId: string | null): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const current = requireConversation(state, id);
      const timestamp = this.now().toISOString();
      const { activeCanvasArtifactId: _old, ...rest } = current;
      const updated: AgentConversation = {
        ...rest,
        ...(artifactId ? { activeCanvasArtifactId: artifactId } : {}),
        updatedAt: timestamp,
      };
      return [replaceConversation(state, updated), updated];
    });
  }

  async upsertSubagentRun(id: string, run: AgentSubagentRun): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const current = requireConversation(state, id);
      if (run.conversationId !== id) {
        throw new Error("Subagent run conversationId does not match the conversation");
      }
      const timestamp = this.now().toISOString();
      const existing = current.subagentRuns ?? [];
      const without = existing.filter((item) => item.id !== run.id);
      const next = [...without, { ...run, updatedAt: timestamp }];
      const evicted = next.slice(-SUBAGENT_RUN_MAX_COUNT);
      const updated: AgentConversation = {
        ...current,
        subagentRuns: evicted,
        updatedAt: timestamp,
      };
      return [replaceConversation(state, updated), updated];
    });
  }

  async setActiveSubagentRun(id: string, runId: string | null): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const current = requireConversation(state, id);
      const timestamp = this.now().toISOString();
      const { activeSubagentRunId: _old, ...rest } = current;
      const updated: AgentConversation = {
        ...rest,
        ...(runId ? { activeSubagentRunId: runId } : {}),
        updatedAt: timestamp,
      };
      return [replaceConversation(state, updated), updated];
    });
  }

  async updateSubagentRunStatus(
    id: string,
    runId: string,
    status: AgentSubagentRunStatus,
    patch?: { summary?: string; error?: string; steps?: readonly AgentSubagentStreamStep[] },
  ): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const current = requireConversation(state, id);
      const timestamp = this.now().toISOString();
      const runs = current.subagentRuns ?? [];
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
      const conversation: AgentConversation = {
        ...current,
        subagentRuns: updated,
        updatedAt: timestamp,
      };
      // Terminal statuses clear the active pointer when it matches this run so a
      // parent-turn abort does not leave activeSubagentRunId stuck on "running".
      const active = current.activeSubagentRunId;
      const terminal = status === "ok" || status === "fail" || status === "cancelled";
      let next = conversation;
      if (terminal && active === runId) {
        const { activeSubagentRunId: _drop, ...rest } = conversation;
        next = rest;
      }
      return [replaceConversation(state, next), next];
    });
  }

  private async load(): Promise<ConversationDocument> {
    if (this.state) return this.state;
    try {
      const parsed: unknown = JSON.parse(await readFile(this.path, "utf8"));
      this.state = normalizeDocument(parsed);
    } catch (error) {
      if (isFileNotFound(error)) {
        this.state = { version: 2, conversations: [] };
      } else {
        throw new Error("Could not load conversations", { cause: error });
      }
    }
    return this.state;
  }

  private async mutate<T>(
    operation: (state: ConversationDocument) => Promise<readonly [ConversationDocument, T]>,
  ): Promise<T> {
    let output!: T;
    const run = this.mutation.then(async () => {
      const [state, result] = await operation(await this.load());
      await this.persist(state);
      this.state = state;
      output = result;
    });
    this.mutation = run.catch(() => undefined);
    await run;
    return output;
  }

  private async persist(state: ConversationDocument): Promise<void> {
    await mkdir(dirname(this.path), { recursive: true });
    const temporaryPath = `${this.path}.tmp`;
    await writeFile(temporaryPath, JSON.stringify(state, null, 2), { mode: 0o600 });
    await rename(temporaryPath, this.path);
  }
}

function normalizeDocument(value: unknown): ConversationDocument {
  if (typeof value !== "object" || value === null || !Array.isArray((value as { conversations?: unknown }).conversations)) {
    throw new Error("Conversation file has an invalid shape");
  }
  const conversations = (value as { conversations: unknown[] }).conversations.flatMap((item) => {
    if (typeof item !== "object" || item === null) return [];
    const candidate = item as Partial<AgentConversation>;
    if (typeof candidate.id !== "string" || typeof candidate.createdAt !== "string" || typeof candidate.updatedAt !== "string") return [];
    const conversationId = candidate.id;
    const messages = Array.isArray(candidate.messages)
      ? candidate.messages.flatMap((item) => {
          if (isConversationMessage(item)) return [item];
          const repaired = repairConversationMessage(item);
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
    ? record.attempted.filter((id): id is string => typeof id === "string")
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

function isConversationAttachment(value: unknown): boolean {  if (typeof value !== "object" || value === null) return false;
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

function isCheckpoint(value: unknown): value is AgentConversationCheckpoint {
  if (typeof value !== "object" || value === null) return false;
  const checkpoint = value as Partial<AgentConversationCheckpoint>;
  return typeof checkpoint.summary === "string"
    && Number.isInteger(checkpoint.compactedMessageCount)
    && (checkpoint.via === "provider" || checkpoint.via === "extractive")
    && (checkpoint.compactionCount === undefined || (Number.isInteger(checkpoint.compactionCount) && checkpoint.compactionCount >= 0));
}

function requireConversation(state: ConversationDocument, id: string): AgentConversation {
  const conversation = state.conversations.find((item) => item.id === id);
  if (!conversation) throw new Error(`Conversation not found: ${id}`);
  return conversation;
}

function replaceConversation(state: ConversationDocument, updated: AgentConversation): ConversationDocument {
  return {
    ...state,
    conversations: state.conversations.map((conversation) => conversation.id === updated.id ? updated : conversation),
  };
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
