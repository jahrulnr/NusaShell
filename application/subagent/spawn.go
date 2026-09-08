package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
)

func (s *Service) acpReady(id string) (*domain.AcpAgent, Runtime, *contracts.RPCError) {
	if s.deps.Agents == nil || s.deps.Runtime == nil {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ACP runtime is not available"}
	}
	agent, err := s.deps.Agents.Get(id)
	if err != nil {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return agent, s.deps.Runtime, nil
}

// acpRun resolves the runtime that owns a run id and returns its snapshot:
// live ACP sessions first, then internal delegate runs.
func (s *Service) acpRun(id string) (*domain.AcpRun, Runtime, *contracts.RPCError) {
	runtime, rpcErr := s.runRuntime(id)
	if rpcErr != nil {
		return nil, nil, rpcErr
	}
	run, ok := runtime.Get(id)
	if !ok {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "acp run not found"}
	}
	return run, runtime, nil
}

// runRuntime resolves the runtime that owns a run id: live ACP sessions
// first, then internal delegate runs.
func (s *Service) runRuntime(id string) (Runtime, *contracts.RPCError) {
	if s.deps.Runtime != nil {
		if _, ok := s.deps.Runtime.Get(id); ok {
			return s.deps.Runtime, nil
		}
	}
	if s.delegates != nil {
		if _, ok := s.delegates.Get(id); ok {
			return s.delegates, nil
		}
	}
	return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "acp run not found"}
}

// EnabledAgents returns registered ACP agents that are enabled.
func (s *Service) EnabledAgents() []*domain.AcpAgent {
	if s == nil || s.deps.Agents == nil {
		return nil
	}
	var out []*domain.AcpAgent
	for _, agent := range s.deps.Agents.List() {
		if agent.Enabled {
			out = append(out, agent)
		}
	}
	return out
}

// DispatchSubagent is the `subagent` tool implementation: one tool whose
// op selects the action. conversationID and toolCallID are passed from the
// App wrapper (context.go stays in root). op defaults to spawn so calls
// imitating the pre-dispatcher roster keep working.
func (s *Service) DispatchSubagent(ctx context.Context, conversationID, toolCallID string, argsJSON []byte) (string, error) {
	var args struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	switch strings.TrimSpace(args.Op) {
	case "", "spawn":
		return s.SpawnSubagents(ctx, conversationID, toolCallID, argsJSON)
	case "steer":
		return s.SteerRun(argsJSON)
	case "stop":
		return s.StopRun(argsJSON)
	case "wait":
		return s.WaitRunTool(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown subagent op %q (spawn, steer, stop, wait)", args.Op)
	}
}

// SpawnSubagents is the spawn op of the `subagent` tool. conversationID and
// toolCallID are passed from the App wrapper (context.go stays in root).
// agent_id selects the target: an ACP agent id, or "internal" for a
// NusaShell delegate run on this engine. An omitted agent_id uses the
// default enabled ACP agent, falling back to the internal delegate when no
// ACP agent is enabled.
func (s *Service) SpawnSubagents(ctx context.Context, conversationID, toolCallID string, argsJSON []byte) (string, error) {
	var args struct {
		Prompt    string `json:"prompt"`
		Title     string `json:"title"`
		AgentID   string `json:"agent_id"`
		Workspace string `json:"workspace"`
		ModeID    string `json:"mode_id"`
		ModelID   string `json:"model_id"`
		Async     bool   `json:"async"`
		Count     int    `json:"count"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	title := domain.NormalizeAcpRunTitle(args.Title)
	count := args.Count
	if count <= 0 {
		count = 1
	}
	if count > domain.MaxAcpSpawnCount {
		return "", fmt.Errorf("count must be between 1 and %d", domain.MaxAcpSpawnCount)
	}
	workspace := strings.TrimSpace(args.Workspace)
	if workspace == "" && conversationID != "" && s.deps.Conversations != nil {
		if conv, err := s.deps.Conversations.Get(conversationID); err == nil {
			workspace = conv.Workspace
		}
	}
	if workspace != "" && !domain.PathRooted(workspace) {
		return "", fmt.Errorf("workspace must be an absolute path")
	}

	agentID := strings.TrimSpace(args.AgentID)
	if agentID == internalDelegateAgentID {
		return s.spawnInternal(conversationID, toolCallID, args.Prompt, title, workspace, count)
	}

	var agent *domain.AcpAgent
	var err error
	if agentID != "" {
		if s.deps.Agents == nil {
			return "", fmt.Errorf("ACP subagents are not configured")
		}
		agent, err = s.deps.Agents.Get(agentID)
		if err != nil {
			return "", fmt.Errorf("unknown ACP agent %q", args.AgentID)
		}
	} else {
		list := s.EnabledAgents()
		if len(list) == 0 {
			// No ACP agent is enabled: the internal delegate is the
			// default delegation target.
			return s.spawnInternal(conversationID, toolCallID, args.Prompt, title, workspace, count)
		}
		agent = list[0]
	}
	if !agent.Enabled {
		return "", fmt.Errorf("ACP agent %q is disabled", agent.Name)
	}
	if workspace == "" {
		workspace = agent.DefaultWorkspace
		if workspace != "" && !domain.PathRooted(workspace) {
			return "", fmt.Errorf("workspace must be an absolute path")
		}
	}

	// Parent plan handoff: subagents do not receive the parent
	// conversation, so a running task's plan must travel explicitly. When
	// the parent conversation has a todo brief, point the subagent at the
	// mirrored plan file (read-first); when the file is missing or the
	// subagent runs in a different workspace (sandbox may refuse paths
	// outside it), inline a compact Objective + Done when summary instead
	// of relying on the subagent's memory of a plan it never saw.
	prompt := args.Prompt
	if s.deps.Todos != nil && conversationID != "" {
		if brief := s.deps.Todos.GetBrief(conversationID); strings.TrimSpace(brief) != "" {
			prompt = withParentPlan(prompt, s.deps.Todos.PlanPath(conversationID), brief, workspace)
		}
	}

	results := make([]domain.AcpSpawned, count)
	for i := 0; i < count; i++ {
		runTitle := title
		if title != "" && count > 1 {
			runTitle = fmt.Sprintf("%s (%d)", title, i+1)
		}
		run, err := s.deps.Runtime.Spawn(ctx, SpawnRequest{
			Agent:            agent,
			ConversationID:   conversationID,
			ParentToolCallID: toolCallID,
			Prompt:           prompt,
			Title:            runTitle,
			Workspace:        workspace,
			ModeID:           args.ModeID,
			ModelID:          args.ModelID,
		})
		results[i] = domain.AcpSpawned{Run: run, Err: err}
		if err == nil && run != nil {
			s.EmitRun(contracts.EventAcpRunStarted, run)
			s.trackPending(run.ConversationID, run.ID, domain.SubagentToolName)
		}
	}
	// Always async: the parent agent gets "starting" immediately and is
	// free to continue other work. When the subagent finishes, the OnDone
	// callback updates the original tool call with the summary and
	// triggers a new turn (tool injection) so the parent agent processes
	// the result without blocking.
	return domain.FormatSpawnResult(results), nil
}

// spawnInternal starts count internal delegate runs through the delegate
// runtime. The model comes from ResolveDelegateModel; the run executes the
// conversation rules headless in a hidden pipeline room.
func (s *Service) spawnInternal(conversationID, toolCallID, prompt, title, workspace string, count int) (string, error) {
	if s.delegates == nil {
		return "", fmt.Errorf("internal delegation is not available in this build")
	}
	modelID, err := s.ResolveDelegateModel(conversationID)
	if err != nil {
		return "", err
	}
	results := make([]domain.AcpSpawned, count)
	for i := 0; i < count; i++ {
		runTitle := title
		if title != "" && count > 1 {
			runTitle = fmt.Sprintf("%s (%d)", title, i+1)
		}
		run, err := s.delegates.Spawn(context.Background(), SpawnRequest{
			ConversationID:   conversationID,
			ParentToolCallID: toolCallID,
			Prompt:           prompt,
			Title:            runTitle,
			Workspace:        workspace,
			ModelID:          modelID,
		})
		results[i] = domain.AcpSpawned{Run: run, Err: err}
	}
	return domain.FormatSpawnResult(results), nil
}

// SpawnDelegate is the legacy `delegate` tool entry point. It behaves
// exactly like SpawnSubagents with agent_id forced to the internal delegate
// so rosters cached before the merge keep working.
func (s *Service) SpawnDelegate(ctx context.Context, conversationID, toolCallID string, argsJSON []byte) (string, error) {
	var args map[string]any
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	args["agent_id"] = internalDelegateAgentID
	merged, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	return s.SpawnSubagents(ctx, conversationID, toolCallID, merged)
}

// withParentPlan appends the parent conversation's plan context to a
// subagent prompt. planPath is the mirrored plan file ("" when unresolvable);
// brief is the full brief body. When the plan file exists on disk, the
// subagent is told to read it first. When the file is missing or the
// subagent workspace differs from the plan file's location (a sandboxed
// agent may refuse paths outside its workspace), a compact Objective +
// Done when summary is inlined instead so the plan intent always travels.
func withParentPlan(prompt, planPath, brief, workspace string) string {
	var sb strings.Builder
	sb.WriteString(prompt)
	sb.WriteString("\n\n")
	if planPath != "" {
		if _, err := os.Stat(planPath); err == nil {
			sb.WriteString("Parent plan file (read this first): ")
			sb.WriteString(planPath)
			sb.WriteString("\n")
			if workspace != "" && !strings.HasPrefix(planPath, workspace+string(os.PathSeparator)) {
				// The plan file lives outside the subagent workspace;
				// include a summary as a fallback in case the sandbox
				// refuses to read it.
				if summary := domain.SummarizeBrief(brief); summary != "" {
					sb.WriteString("\nParent plan summary (in case the file is unreadable):\n")
					sb.WriteString(summary)
					sb.WriteString("\n")
				}
			}
			return sb.String()
		}
	}
	if summary := domain.SummarizeBrief(brief); summary != "" {
		sb.WriteString("Parent plan summary:\n")
		sb.WriteString(summary)
		sb.WriteString("\n")
	}
	return sb.String()
}

func (s *Service) SteerRun(argsJSON []byte) (string, error) {
	var args struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	_, rpcErr := s.HandleRunsSteer(contracts.AcpRunSteerRequest{ID: args.ID, Text: args.Text})
	if rpcErr != nil {
		return "", fmt.Errorf("%s", rpcErr.Message)
	}
	run, _, getErr := s.acpRun(args.ID)
	if getErr != nil {
		return "", fmt.Errorf("%s", getErr.Message)
	}
	return domain.SubagentSteerResult(run), nil
}

func (s *Service) StopRun(argsJSON []byte) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	_, rpcErr := s.HandleRunsStop(contracts.AcpRunIDRequest{ID: args.ID})
	if rpcErr != nil {
		return "", fmt.Errorf("%s", rpcErr.Message)
	}
	run, _, getErr := s.acpRun(args.ID)
	if getErr != nil {
		return "", fmt.Errorf("%s", getErr.Message)
	}
	outputPath := s.PersistRun(run)
	return domain.SubagentCompletionResult(run, outputPath), nil
}

func (s *Service) WaitRunTool(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID        string `json:"id"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	run, rpcErr := s.WaitRun(ctx, contracts.AcpRunWaitRequest{ID: args.ID, TimeoutMS: args.TimeoutMS})
	if rpcErr != nil {
		return "", fmt.Errorf("%s", rpcErr.Message)
	}
	outputPath := s.PersistRun(run)
	return domain.SubagentCompletionResult(run, outputPath), nil
}
