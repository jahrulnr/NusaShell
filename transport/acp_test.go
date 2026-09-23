package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"

	"gopkg.in/yaml.v3"
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
	out, err := h.app.Subagent(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Runs []struct {
			ID     string `yaml:"id"`
			Status string `yaml:"status"`
		} `yaml:"runs"`
	}
	yamlOut := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(out), "---\n"), "\n---")
	if err := yaml.Unmarshal([]byte(yamlOut), &parsed); err != nil {
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
	slow, err := h.app.Subagent(ctx, slowPayload)
	if err != nil {
		t.Fatal(err)
	}
	var slowParsed struct {
		Runs []struct {
			ID string `yaml:"id"`
		} `yaml:"runs"`
	}
	yamlSlow := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(slow), "---\n"), "\n---")
	if err := yaml.Unmarshal([]byte(yamlSlow), &slowParsed); err != nil || len(slowParsed.Runs) != 1 {
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

func readSSEMessage(t *testing.T, reader *bufio.Reader) (string, string, map[string]any) {
	t.Helper()
	var event, id string
	var data strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE: %v", err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case line == "":
			if event == "" && data.Len() == 0 {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(data.String()), &payload); err != nil {
				t.Fatalf("decode SSE payload %q: %v", data.String(), err)
			}
			return event, id, payload
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
}

func TestAcpRunStreamSendsSnapshotAndUpdates(t *testing.T) {
	h := newHarness(t, nil)
	started := contracts.AcpRunDTO{
		ID: "run_1", AgentName: "Fake ACP", ConversationID: "conv_1", Status: "running",
	}
	h.app.AcpRunStreams.Publish(contracts.EventAcpRunStarted, started)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.server.URL+"/stream/acp?conversation_id=conv_1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("ACP stream response = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(resp.Body)
	event, id, snapshot := readSSEMessage(t, reader)
	if event != contracts.EventAcpRunStream || snapshot["type"] != contracts.EventAcpRunSnapshot || id != "1" || snapshot["seq"] != float64(1) {
		t.Fatalf("initial ACP stream frame = %q id=%q payload=%v", event, id, snapshot)
	}
	runs, ok := snapshot["runs"].([]any)
	if !ok || len(runs) != 1 || runs[0].(map[string]any)["id"] != started.ID {
		t.Fatalf("initial ACP run snapshot = %#v", snapshot["runs"])
	}

	updated := started
	updated.Transcript = []contracts.AcpTranscriptChunkDTO{{Kind: "text", Text: "live result"}}
	h.app.AcpRunStreams.Publish(contracts.EventAcpRunUpdated, updated)
	event, id, frame := readSSEMessage(t, reader)
	if event != contracts.EventAcpRunStream || frame["type"] != contracts.EventAcpRunUpdated || id != "2" || frame["seq"] != float64(2) {
		t.Fatalf("ACP update frame = %q id=%q payload=%v", event, id, frame)
	}
	gotRun := frame["run"].(map[string]any)
	if gotRun["id"] != started.ID || gotRun["transcript"].([]any)[0].(map[string]any)["text"] != "live result" {
		t.Fatalf("ACP update run = %#v", gotRun)
	}

	reconnectReq, err := http.NewRequestWithContext(ctx, http.MethodGet, h.server.URL+"/stream/acp?conversation_id=conv_1", nil)
	if err != nil {
		t.Fatal(err)
	}
	reconnectResp, err := h.server.Client().Do(reconnectReq)
	if err != nil {
		t.Fatal(err)
	}
	defer reconnectResp.Body.Close()
	reconnectEvent, reconnectID, reconnectSnapshot := readSSEMessage(t, bufio.NewReader(reconnectResp.Body))
	if reconnectEvent != contracts.EventAcpRunStream || reconnectSnapshot["type"] != contracts.EventAcpRunSnapshot || reconnectID != "2" || reconnectSnapshot["seq"] != float64(2) {
		t.Fatalf("reconnect snapshot = %q id=%q payload=%v", reconnectEvent, reconnectID, reconnectSnapshot)
	}
}

func TestAcpRunStreamRequiresConversationID(t *testing.T) {
	h := newHarness(t, nil)
	resp, err := h.server.Client().Get(h.server.URL + "/stream/acp")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("stream status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

type acpStreamRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func (r *acpStreamRecorder) Flush() {
	r.ResponseRecorder.Flush()
	select {
	case r.flushed <- struct{}{}:
	default:
	}
}

func TestAcpRunStreamReturnsUnavailableWhenSubscriberLimitIsReached(t *testing.T) {
	h := newHarness(t, nil)
	subs := make([]interface{ Close() }, 0, 32)
	defer func() {
		for _, sub := range subs {
			sub.Close()
		}
	}()
	for i := 0; i < cap(subs); i++ {
		sub := h.app.AcpRunStreams.Subscribe("conv_" + strconv.Itoa(i))
		if sub == nil {
			t.Fatalf("subscriber %d was rejected below the limit", i)
		}
		subs = append(subs, sub)
	}
	resp, err := h.server.Client().Get(h.server.URL + "/stream/acp?conversation_id=conv_overflow")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stream status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestAcpRunStreamStopsAfterRequestCancellation(t *testing.T) {
	h := newHarness(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/stream/acp?conversation_id=conv_1", nil)
	req.Host = "127.0.0.1"
	req.RemoteAddr = "127.0.0.1:1234"
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	defer cancel()
	writer := &acpStreamRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{}, 1)}
	finished := make(chan struct{})
	go func() {
		(&Server{App: h.app}).handleAcpRunStream(writer, req)
		close(finished)
	}()
	select {
	case <-writer.flushed:
	case <-time.After(time.Second):
		t.Fatal("ACP SSE did not send its initial snapshot")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("ACP SSE handler did not stop after request cancellation")
	}
	if writer.Code != http.StatusOK || writer.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("ACP stream response = %d %q", writer.Code, writer.Header().Get("Content-Type"))
	}
}
