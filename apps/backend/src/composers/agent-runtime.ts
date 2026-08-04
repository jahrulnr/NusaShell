import {
  FilesystemPromptLoader,
  FilesystemReviewStateStore,
  AgentProviderRegistry,
  StaticAgentProvider,
  OpenAiCompatibleAgentProvider,
  NodeRuntimeOsProbe,
  type Logger,
} from "@nusashell/infrastructure";
import {
  McpAgentToolGateway,
  ReviewAgentToolGateway,
  AgentTurnRunner,
  BackgroundReviewScheduler,
  AskQuestionService,
  AgentTurnCoordinator,
  StreamSeqRegistry,
  InMemoryActiveTurnProjection,
  InMemoryConversationTodoPort,
  AsyncToolRuntime,
  createAgentAskRequestEvent,
  createAgentTodoUpdatedEvent,
  createAgentToolJobStartedEvent,
  createAgentToolJobUpdateEvent,
  createAgentToolJobEndedEvent,
  type AgentRuntimeSettings,
  type AgentProvider,
  type EventDispatcher,
} from "@nusashell/application";
import { fileURLToPath } from "node:url";
import type { ContainerOptions } from "../container.js";
import type { PluginRuntimeParts } from "./plugin-runtime.js";
import type { SkillsRuntimeParts } from "./skills-runtime.js";

export interface AgentRuntimeParts {
  readonly agentToolGateway: McpAgentToolGateway;
  readonly agentProviderRegistry: AgentProviderRegistry;
  readonly agentTurnCoordinator: AgentTurnCoordinator;
  readonly streamSeqRegistry: StreamSeqRegistry;
  readonly promptLoader: FilesystemPromptLoader;
  readonly askQuestionService: AskQuestionService;
  readonly reviewGateway: ReviewAgentToolGateway;
  readonly backgroundReviewScheduler: BackgroundReviewScheduler;
  readonly aiRuntime: AgentRuntimeSettings & { stream: boolean; vision: "auto" | "on" | "off"; userPrompt: string };
  readonly withStreamSeq: <T extends { readonly aggregateId: string }>(event: T) => T & { streamSeq: number };
  readonly runtimeOsProbe: NodeRuntimeOsProbe;
  readonly activeTurns: InMemoryActiveTurnProjection;
  readonly conversationTodos: InMemoryConversationTodoPort;
  readonly asyncToolRuntime: AsyncToolRuntime;
}

export function createAgentRuntime(
  options: ContainerOptions,
  logger: Logger,
  eventDispatcher: EventDispatcher,
  plugin: PluginRuntimeParts,
  skills: SkillsRuntimeParts,
): AgentRuntimeParts {
  const aiRuntime: AgentRuntimeParts["aiRuntime"] = {
    strategy: options.ai?.strategy ?? "failover" as "failover" | "round-robin" | "switch",
    totalAttemptBudget: options.ai?.totalAttemptBudget ?? 4,
    stream: options.ai?.stream ?? true,
    vision: options.ai?.vision ?? "auto" as "auto" | "on" | "off",
    userPrompt: options.ai?.userPrompt ?? "",
    maxToolRounds: options.ai?.maxToolRounds ?? 50,
    maxRepeatedToolCalls: options.ai?.maxRepeatedToolCalls ?? 50,
    softRecoverAttempts: options.ai?.softRecoverAttempts ?? 1,
    maxConcurrentToolCalls: options.ai?.maxConcurrentToolCalls ?? 8,
    ...(options.ai?.context ? { context: options.ai.context } : {}),
  };

  const askQuestionService = new AskQuestionService();
  const agentToolGateway = new McpAgentToolGateway(
    plugin.runtimeManager,
    plugin.docsIndex,
    skills.skillRegistry,
    logger,
    skills.memoryStore,
    skills.skillProvenance,
    skills.skillApprovalStaging,
    skills.skillUsage,
    askQuestionService,
  );

  const conversationTodos = new InMemoryConversationTodoPort();
  agentToolGateway.bindTodos(conversationTodos, (conversationId, items) => {
    void eventDispatcher.publish(createAgentTodoUpdatedEvent(conversationId, items));
  });

  const asyncToolRuntime = new AsyncToolRuntime({
    onStarted: (handle) => {
      void eventDispatcher.publish(withStreamSeq(createAgentToolJobStartedEvent(
        handle.handleId,
        handle.conversationId,
        handle.kind,
        handle.toolName,
        JSON.stringify(handle.args).slice(0, 500),
        { ...(handle.pluginId ? { pluginId: handle.pluginId } : {}), ...(handle.traceId ? { traceId: handle.traceId } : {}) },
      )));
    },
    onProgress: (handle, tail, bytes) => {
      void eventDispatcher.publish(withStreamSeq(createAgentToolJobUpdateEvent(
        handle.handleId,
        handle.conversationId,
        handle.status,
        tail.slice(-2000),
        bytes,
        0,
      )));
    },
    onEnded: (handle) => {
      const ok = handle.status === "ok";
      void eventDispatcher.publish(withStreamSeq(createAgentToolJobEndedEvent(
        handle.handleId,
        handle.conversationId,
        ok,
        handle.endReason ?? (ok ? "completed" : "failed"),
        { ...(handle.error ? { error: handle.error } : {}), ...(handle.result !== undefined ? { output: handle.result } : {}) },
      )));
    },
  });
  agentToolGateway.bindAsyncToolRuntime(asyncToolRuntime);

  const promptLoader = new FilesystemPromptLoader(
    options.promptsRoot ?? fileURLToPath(new URL("../../../resources/agent/prompts", import.meta.url)),
  );

  const agentProviders: AgentProvider[] = options.ai?.stubEnabled ? [new StaticAgentProvider()] : [];
  if (options.ai?.baseUrl) {
    agentProviders.push(new OpenAiCompatibleAgentProvider({
      id: options.ai.providerId,
      ...(options.ai.api ? { api: options.ai.api } : {}),
      baseUrl: options.ai.baseUrl,
      ...(options.ai.apiKey ? { apiKey: options.ai.apiKey } : {}),
      ...(options.ai.model ? { model: options.ai.model } : {}),
      logger,
      ...(options.ai.retry ? { retry: {
        ...options.ai.retry,
        onRetry: (event) => {
          logger.warn("AI provider retry provider=%s attempt=%d delayMs=%d status=%d kind=%s", event.providerId, event.attempt, event.delayMs, event.status, event.kind);
        },
      } } : {}),
      stream: aiRuntime.stream,
      vision: aiRuntime.vision,
      ...(options.ai.timeoutMs !== undefined ? { timeoutMs: options.ai.timeoutMs } : {}),
    }));
  }
  const agentProviderRegistry = new AgentProviderRegistry(agentProviders);
  const agentTurnCoordinator = new AgentTurnCoordinator();
  const streamSeqRegistry = new StreamSeqRegistry();
  const withStreamSeq = <T extends { readonly aggregateId: string }>(event: T): T & { streamSeq: number } => ({
    ...event,
    streamSeq: streamSeqRegistry.next(event.aggregateId),
  });

  askQuestionService.setOnAsk((pending) => {
    logger.info(
      "Agent ask pending turnId=%s callId=%s question=%s",
      pending.turnId,
      pending.callId,
      pending.request.question.slice(0, 120),
    );
    void eventDispatcher.publish(withStreamSeq(createAgentAskRequestEvent(
      pending.turnId,
      pending.callId,
      pending.request.question,
      pending.request.options,
      pending.request.allowFreeText,
      pending.request.multiSelect,
    )));
  });

  const reviewGateway = new ReviewAgentToolGateway(agentToolGateway);
  const reviewStateStore = new FilesystemReviewStateStore(
    options.memoryRoot ?? fileURLToPath(new URL("../../../.nusashell/agent/memory", import.meta.url)),
  );
  const backgroundReviewScheduler = new BackgroundReviewScheduler({
    stateStore: reviewStateStore,
    promptLoader,
    providerRegistry: agentProviderRegistry,
    reviewGateway,
    runnerFactory: ({ provider, toolGateway, maxToolRounds }) =>
      new AgentTurnRunner({
        provider,
        toolGateway,
        defaultMaxToolRounds: maxToolRounds,
        logger,
      }),
    defaultProviderId: options.ai?.providerId || (options.ai?.stubEnabled ? "stub" : ""),
    eventDispatcher,
    logger,
  });
  if (options.backgroundReview) {
    backgroundReviewScheduler.configure(options.backgroundReview);
  }

  return {
    agentToolGateway, agentProviderRegistry, agentTurnCoordinator, streamSeqRegistry,
    promptLoader, askQuestionService, reviewGateway, backgroundReviewScheduler,
    aiRuntime, withStreamSeq, runtimeOsProbe: new NodeRuntimeOsProbe(),
    activeTurns: new InMemoryActiveTurnProjection(),
    conversationTodos,
    asyncToolRuntime,
  };
}
