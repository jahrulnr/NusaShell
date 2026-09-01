package application

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"

	"gopkg.in/yaml.v3"
)

func (a *App) acpReady(id string) (*domain.AcpAgent, AcpRuntime, *contracts.RPCError) {
	if a.AcpAgents == nil || a.Acp == nil {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ACP runtime is not available"}
	}
	agent, err := a.AcpAgents.Get(id)
	if err != nil {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return agent, a.Acp, nil
}

func (a *App) acpRun(id string) (*domain.AcpRun, AcpRuntime, *contracts.RPCError) {
	if a.Acp == nil {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ACP runtime is not available"}
	}
	run, ok := a.Acp.Get(id)
	if !ok {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "acp run not found"}
	}
	return run, a.Acp, nil
}

func (a *App) enabledAcpAgents() []*domain.AcpAgent {
	if a.AcpAgents == nil {
		return nil
	}
	var out []*domain.AcpAgent
	for _, agent := range a.AcpAgents.List() {
		if agent.Enabled {
			out = append(out, agent)
		}
	}
	return out
}

// SpawnSubagents is the `subagent` tool implementation. It spawns one or
// more ACP subagents and always returns "starting" immediately: the parent
// agent is free to continue other work, and when a subagent finishes the
// OnDone callback updates the original tool call with the summary and
// triggers a new turn (tool injection) so the parent processes the result
// without blocking.
func (a *App) SpawnSubagents(ctx context.Context, argsJSON []byte) (string, error) {
	if a.Acp == nil || a.AcpAgents == nil {
		return "", fmt.Errorf("ACP subagents are not configured")
	}
	var args struct {
		Prompt    string `json:"prompt"`
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
	count := args.Count
	if count <= 0 {
		count = 1
	}
	if count > domain.MaxAcpSpawnCount {
		return "", fmt.Errorf("count must be between 1 and %d", domain.MaxAcpSpawnCount)
	}
	var agent *domain.AcpAgent
	var err error
	if strings.TrimSpace(args.AgentID) != "" {
		agent, err = a.AcpAgents.Get(args.AgentID)
		if err != nil {
			return "", fmt.Errorf("unknown ACP agent %q", args.AgentID)
		}
	} else {
		list := a.enabledAcpAgents()
		if len(list) == 0 {
			return "", fmt.Errorf("no enabled ACP agents; add one in Providers")
		}
		agent = list[0]
	}
	if !agent.Enabled {
		return "", fmt.Errorf("ACP agent %q is disabled", agent.Name)
	}
	workspace := strings.TrimSpace(args.Workspace)
	if workspace == "" {
		if cid := ConversationIDFromContext(ctx); cid != "" && a.Conversations != nil {
			if conv, err := a.Conversations.Get(cid); err == nil {
				workspace = conv.Workspace
			}
		}
	}
	if workspace == "" {
		workspace = agent.DefaultWorkspace
	}
	if workspace != "" && !domain.PathRooted(workspace) {
		return "", fmt.Errorf("workspace must be an absolute path")
	}

	// Parent plan handoff: ACP subagents do not receive the parent
	// conversation, so a running task's plan must travel explicitly. When
	// the parent conversation has a todo brief, point the subagent at the
	// mirrored plan file (read-first); when the file is missing or the
	// subagent runs in a different workspace (sandbox may refuse paths
	// outside it), inline a compact Objective + Done when summary instead
	// of relying on the subagent's memory of a plan it never saw.
	prompt := args.Prompt
	if a.Todos != nil {
		if cid := ConversationIDFromContext(ctx); cid != "" {
			if brief := a.Todos.GetBrief(cid); strings.TrimSpace(brief) != "" {
				prompt = withParentPlan(prompt, a.Todos.PlanPath(cid), brief, workspace)
			}
		}
	}

	results := make([]domain.AcpSpawned, count)
	for i := 0; i < count; i++ {
		run, err := a.Acp.Spawn(ctx, AcpSpawnRequest{
			Agent:            agent,
			ConversationID:   ConversationIDFromContext(ctx),
			ParentToolCallID: ToolCallIDFromContext(ctx),
			Prompt:           prompt,
			Workspace:        workspace,
			ModeID:           args.ModeID,
			ModelID:          args.ModelID,
		})
		results[i] = domain.AcpSpawned{Run: run, Err: err}
		if err == nil && run != nil {
			a.emitAcpRun(contracts.EventAcpRunStarted, run)
			a.trackPendingRun(run.ConversationID, run.ID)
		}
	}
	// Always async: the parent agent gets "starting" immediately and is
	// free to continue other work. When the subagent finishes, the OnDone
	// callback updates the original tool call with the summary and
	// triggers a new turn (tool injection) so the parent agent processes
	// the result without blocking.
	return domain.FormatSpawnResult(results), nil
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

func (a *App) SteerAcpRun(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	res, rpcErr := a.handleAcpRunsSteer(contracts.AcpRunSteerRequest{ID: args.ID, Text: args.Text})
	if rpcErr != nil {
		return "", fmt.Errorf("%s", rpcErr.Message)
	}
	b, _ := yaml.Marshal(res)
	return "---\n" + strings.TrimRight(string(b), "\n") + "\n---", nil
}

func (a *App) StopAcpRun(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	res, rpcErr := a.handleAcpRunsStop(contracts.AcpRunIDRequest{ID: args.ID})
	if rpcErr != nil {
		return "", fmt.Errorf("%s", rpcErr.Message)
	}
	b, _ := yaml.Marshal(res)
	return "---\n" + strings.TrimRight(string(b), "\n") + "\n---", nil
}

func (a *App) WaitAcpRun(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID        string `json:"id"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	run, rpcErr := a.waitAcpRun(ctx, contracts.AcpRunWaitRequest{ID: args.ID, TimeoutMS: args.TimeoutMS})
	if rpcErr != nil {
		return "", fmt.Errorf("%s", rpcErr.Message)
	}
	outputPath := a.persistAcpRun(run)
	return domain.SubagentCompletionResult(run, outputPath), nil
}

func (a *App) EnabledAcpAgents() []*domain.AcpAgent {
	return a.enabledAcpAgents()
}
