package transport

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

func TestAcpAgentsCRUDAndProbe(t *testing.T) {
	h := newHarness(t, nil)
	list := h.rpcOK(t, "acp.agents.list", map[string]any{})
	var listed contracts.AcpAgentsListResult
	if err := json.Unmarshal(list.Result, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Agents) != 0 {
		t.Fatalf("expected empty ACP registry, got %+v", listed.Agents)
	}

	save := h.rpcOK(t, "acp.agents.save", map[string]any{
		"name":    "Fake ACP",
		"command": fakeacpBin,
		"enabled": true,
		"env":     map[string]string{"FAKEACP_AUTH": "1"},
	})
	var saved contracts.AcpAgentsListResult
	if err := json.Unmarshal(save.Result, &saved); err != nil || len(saved.Agents) != 1 {
		t.Fatalf("save: %v %#v", err, saved)
	}
	agent := saved.Agents[0]
	if agent.Command != fakeacpBin {
		t.Fatalf("command = %q", agent.Command)
	}
	if len(agent.EnvKeys) != 1 || agent.EnvKeys[0] != "FAKEACP_AUTH" {
		t.Fatalf("env keys = %#v (values must not appear on the wire)", agent.EnvKeys)
	}

	locked := h.rpc(t, "acp.agents.save", map[string]any{
		"id": agent.ID, "name": "Fake ACP", "command": "/bin/false", "enabled": true,
	})
	if locked.OK || locked.Error == nil || !strings.Contains(locked.Error.Message, "immutable") {
		t.Fatalf("want immutable command error, got %+v", locked)
	}

	probe := h.rpcOK(t, "acp.agents.probe", map[string]any{"id": agent.ID})
	var probed contracts.AcpProbeResult
	if err := json.Unmarshal(probe.Result, &probed); err != nil {
		t.Fatal(err)
	}
	if !probed.OK || len(probed.Agent.CachedAuthMethods) == 0 {
		t.Fatalf("probe did not cache auth methods: %+v", probed)
	}

	del := h.rpcOK(t, "acp.agents.delete", map[string]any{"id": agent.ID})
	_ = del
	empty := h.rpcOK(t, "acp.agents.list", map[string]any{})
	if err := json.Unmarshal(empty.Result, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Agents) != 0 {
		t.Fatalf("delete leftover: %+v", listed.Agents)
	}
}

func TestAcpSpawnSteerStopViaTools(t *testing.T) {
	h := newHarness(t, nil)
	save := h.rpcOK(t, "acp.agents.save", map[string]any{
		"name": "Fake ACP", "command": fakeacpBin, "enabled": true,
	})
	var saved contracts.AcpAgentsListResult
	if err := json.Unmarshal(save.Result, &saved); err != nil || len(saved.Agents) != 1 {
		t.Fatalf("save: %v %#v", err, saved)
	}
	id := saved.Agents[0].ID
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	payload, _ := json.Marshal(map[string]any{
		"prompt": "hello nest", "async": true, "count": 2, "workspace": ws, "agent_id": id,
	})
	out, err := h.app.SpawnSubagents(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Runs []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Runs) != 2 {
		t.Fatalf("spawned %d runs, want 2: %s", len(parsed.Runs), out)
	}

	list := h.rpcOK(t, "acp.runs.list", map[string]any{})
	var runs contracts.AcpRunsListResult
	if err := json.Unmarshal(list.Result, &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs.Runs) < 2 {
		t.Fatalf("runs.list = %+v", runs)
	}

	slowPayload, _ := json.Marshal(map[string]any{
		"prompt": "SLOW nest", "async": true, "workspace": ws, "agent_id": id,
	})
	slow, err := h.app.SpawnSubagents(ctx, slowPayload)
	if err != nil {
		t.Fatal(err)
	}
	var slowParsed struct {
		Runs []struct {
			ID string `json:"id"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(slow), &slowParsed); err != nil || len(slowParsed.Runs) != 1 {
		t.Fatalf("slow spawn: %v %s", err, slow)
	}
	stopped := h.rpcOK(t, "acp.runs.stop", map[string]any{"id": slowParsed.Runs[0].ID})
	var run contracts.AcpRunDTO
	if err := json.Unmarshal(stopped.Result, &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != string(domain.AcpRunCancelled) {
		t.Fatalf("stop status = %#v", run)
	}
}
