package transport

import (
	"encoding/json"
	"testing"

	"nusashell/infrastructure/ci"
)

func TestRPCAutomationListAndSave(t *testing.T) {
	h := newHarness(t, nil)
	svc, store, err := ci.BuildAutomation(h.app.DataDir, h.app.Bus, h.app.Plugins, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	h.app.Automation = svc

	listed := h.rpcOK(t, "automation.list", map[string]any{})
	var list struct {
		Workflows []struct {
			ID string `json:"id"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal(listed.Result, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Workflows) != 0 {
		t.Fatalf("want empty list, got %+v", list.Workflows)
	}

	saved := h.rpcOK(t, "automation.save", map[string]any{
		"yaml": "name: ping\ntriggers:\n  manual: true\njobs:\n  hi:\n    steps:\n      - run: true\n",
	})
	var dto struct {
		Workflow struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(saved.Result, &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Workflow.Name != "ping" || dto.Workflow.ID == "" {
		t.Fatalf("saved = %+v", dto)
	}

	info := h.rpcOK(t, "app.info", map[string]any{})
	var appInfo struct {
		Features struct {
			Automation bool `json:"automation"`
		} `json:"features"`
	}
	if err := json.Unmarshal(info.Result, &appInfo); err != nil {
		t.Fatal(err)
	}
	if !appInfo.Features.Automation {
		t.Fatal("app.info features.automation should be true when wired")
	}
}
