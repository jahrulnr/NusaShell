package tools

import (
	"context"
	"encoding/json"

	"nusashell/application"
)

func (t *Toolbox) subagentToolEntry() toolEntry {
	return toolEntry{
		info: application.ToolInfo{
			Name:        "subagent",
			InputSchema: obj("object", props("op", strEnum("Action: spawn (default) starts async subagent runs; steer redirects a live run; stop cancels a live run; wait blocks this round until a run is terminal", "spawn", "steer", "stop", "wait"), "prompt", str("Self-contained task brief (spawn)"), "title", str("Optional short label shown in the Agent dock/drawer (what this run is for; spawn)"), "agent_id", str("Optional target (spawn): an ACP agent id from Providers, or \"internal\" to run the task on NusaShell's own engine headless in a hidden pipeline room (standard toolbox, no permission prompts, model from Settings → Internal delegate model; empty inherits this conversation's model). Omit to use the default enabled ACP agent, or the internal delegate when none is enabled"), "workspace", str("Optional absolute workspace path (defaults to the conversation workspace; spawn)"), "mode_id", str("Optional ACP session mode id advertised by the agent (spawn, ACP targets only)"), "model_id", str("Optional ACP model id advertised by the agent (spawn, ACP targets only)"), "count", intSchema("Number of parallel spawns of the same brief (spawn; 1-6, default 1)"), "id", str("Subagent run id (steer/stop/wait)"), "text", str("Steer instruction (steer)"), "timeout_ms", intSchema("Optional wait timeout in milliseconds (wait)")), "prompt"),
		},
		handler:  t.execSubagent,
		enabled:  subagentAdvertised,
		describe: subagentDescription,
	}
}

// subagentAdvertised reports whether the subagent tool is advertised: an
// ACP agent is enabled or the internal delegate is wired.
func subagentAdvertised(t *Toolbox) bool {
	return t.Acp != nil && (len(t.Acp.EnabledAcpAgents()) > 0 || t.Delegate != nil)
}

// subagentDescription builds the advertised description; called by
// ListTools only after subagentAdvertised passes, so t.Acp is non-nil.
func subagentDescription(t *Toolbox) string {
	desc := "Delegate subagent — one tool for the whole subagent family. op=spawn (default) starts async subagent runs and returns immediately; the result is injected later. op=steer redirects a live run (ACP: interrupt-and-replace on the same session; internal delegate: queued for the next tool-round boundary). op=stop cancels a live run. op=wait blocks this round until a run is terminal. agent_id (spawn) selects the target: an ACP agent id from Providers (acp_*), or the built-in internal delegate."
	if delegation := application.AcpDelegationDescription(t.Acp.EnabledAcpAgents()); delegation != "" {
		desc += "\n\n" + delegation
	}
	return desc
}

// legacySubagentToolEntries are the retired per-verb names kept executable
// for persisted-history compat (application/service/tooloutput and
// toolpresentation still recognise them). Never advertised.
func (t *Toolbox) legacySubagentToolEntries() []toolEntry {
	return []toolEntry{
		{info: application.ToolInfo{Name: "delegate"}, handler: t.execDelegate, enabled: neverAdvertised},
		{info: application.ToolInfo{Name: "subagent_steer"}, handler: t.execSubagentSteer, enabled: neverAdvertised},
		{info: application.ToolInfo{Name: "subagent_stop"}, handler: t.execSubagentStop, enabled: neverAdvertised},
		{info: application.ToolInfo{Name: "subagent_wait"}, handler: t.execSubagentWait, enabled: neverAdvertised},
	}
}

func (t *Toolbox) execDelegate(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Delegate == nil {
		return "", depMissing("delegation is not available in this build")
	}
	return t.Delegate.SpawnDelegate(ctx, argsJSON)
}

func (t *Toolbox) execSubagent(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Acp == nil {
		return "", depMissing("no subagent support configured")
	}
	return t.Acp.Subagent(ctx, argsJSON)
}

// Legacy per-verb names route to the same dispatcher: steer/stop/wait
// map to their op, and the old `delegate` tool is spawn with
// agent_id=internal (SpawnDelegate forces it).
func (t *Toolbox) execSubagentSteer(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Acp == nil {
		return "", depMissing("no subagent support configured")
	}
	return t.Acp.Subagent(ctx, mergeOp(argsJSON, "steer"))
}

func (t *Toolbox) execSubagentStop(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Acp == nil {
		return "", depMissing("no subagent support configured")
	}
	return t.Acp.Subagent(ctx, mergeOp(argsJSON, "stop"))
}

func (t *Toolbox) execSubagentWait(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Acp == nil {
		return "", depMissing("no subagent support configured")
	}
	return t.Acp.Subagent(ctx, mergeOp(argsJSON, "wait"))
}

// mergeOp injects an op into a tool args payload for legacy per-verb
// subagent names (subagent_steer/stop/wait) so they route through the
// dispatcher. An existing op in the args wins; malformed args pass through.
func mergeOp(argsJSON []byte, op string) []byte {
	var args map[string]any
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return argsJSON
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	if _, ok := args["op"]; !ok {
		args["op"] = op
	}
	merged, err := json.Marshal(args)
	if err != nil {
		return argsJSON
	}
	return merged
}
