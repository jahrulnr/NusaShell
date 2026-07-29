import { randomUUID } from "node:crypto";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import type {
  AgentConversation,
  AgentConversationCheckpoint,
  AgentConversationMessage,
  AgentConversationSummary,
} from "../shared/agent-conversation-contract.js";

interface ConversationDocument {
  readonly version: 1;
  readonly conversations: readonly AgentConversation[];
}

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

  async create(): Promise<AgentConversation> {
    return this.mutate(async (state) => {
      const timestamp = this.now().toISOString();
      const conversation: AgentConversation = {
        id: this.createId(),
        title: "New conversation",
        createdAt: timestamp,
        updatedAt: timestamp,
        messages: [],
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
      const savedMessage = { ...message, createdAt: message.createdAt ?? timestamp };
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
        },
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

  private async load(): Promise<ConversationDocument> {
    if (this.state) return this.state;
    try {
      const parsed: unknown = JSON.parse(await readFile(this.path, "utf8"));
      this.state = normalizeDocument(parsed);
    } catch (error) {
      if (isFileNotFound(error)) {
        this.state = { version: 1, conversations: [] };
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
    const messages = Array.isArray(candidate.messages)
      ? candidate.messages.filter(isConversationMessage)
      : [];
    return [{
      id: candidate.id,
      title: typeof candidate.title === "string" ? candidate.title : "New conversation",
      createdAt: candidate.createdAt,
      updatedAt: candidate.updatedAt,
      messages,
      ...(isCheckpoint(candidate.checkpoint) ? { checkpoint: candidate.checkpoint } : {}),
    }];
  });
  return { version: 1, conversations };
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
    && (message.attachments === undefined || (
      message.role === "user"
      && Array.isArray(message.attachments)
      && message.attachments.length <= 4
      && message.attachments.every(isConversationAttachment)
    ));
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

function isCheckpoint(value: unknown): value is AgentConversationCheckpoint {
  if (typeof value !== "object" || value === null) return false;
  const checkpoint = value as Partial<AgentConversationCheckpoint>;
  return typeof checkpoint.summary === "string"
    && Number.isInteger(checkpoint.compactedMessageCount)
    && (checkpoint.via === "provider" || checkpoint.via === "extractive");
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
