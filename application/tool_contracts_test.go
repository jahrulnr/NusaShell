package application

import (
	"context"
	"encoding/json"
	"testing"

	"nusashell/contracts"
)

func TestToolContractsFollowExecutionRoster(t *testing.T) {
	box := &factoryStubToolbox{tools: []ToolInfo{
		{Name: "file_read", Description: "Read a file", InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		}},
		{Name: "read_media", InputSchema: map[string]any{"type": "object"}},
		{Name: "delegate", InputSchema: map[string]any{"type": "object"}},
	}}
	a := &App{Toolbox: box}

	result, rpcErr := a.handleToolContracts(contracts.ToolContractsRequest{Workspace: ""})
	if rpcErr != nil {
		t.Fatalf("handleToolContracts: %v", rpcErr)
	}
	got, ok := result.(contracts.ToolContractsResult)
	if !ok {
		t.Fatalf("result type = %T, want contracts.ToolContractsResult", result)
	}
	if got.Version != contracts.ToolContractVersion {
		t.Fatalf("catalog version = %d, want %d", got.Version, contracts.ToolContractVersion)
	}
	fileRead, ok := findToolContract(got.Tools, "file_read")
	if !ok {
		t.Fatalf("file_read missing from catalog: %+v", got.Tools)
	}
	if fileRead.ID != "tool.file_read.v1" || fileRead.CSSClass != "agent-tool-file-read" {
		t.Fatalf("file_read identity = %+v", fileRead)
	}
	if len(fileRead.Presentation.RequestFields) != 1 || fileRead.Presentation.RequestFields[0] != "path" {
		t.Fatalf("file_read request fields = %+v", fileRead.Presentation.RequestFields)
	}
	if _, ok := findToolContract(got.Tools, "memory_project"); ok {
		t.Fatal("memory_project must not be advertised without a workspace")
	}
	dispatcher, ok := findToolContract(got.Tools, "memory")
	if !ok {
		t.Fatal("memory dispatcher missing from catalog")
	}
	if len(dispatcher.Presentation.Variants) != 3 {
		t.Fatalf("memory variants = %+v", dispatcher.Presentation.Variants)
	}

	media := buildToolContract(ToolDef{Name: "generate_image"})
	if media.InputSchema == nil {
		t.Fatal("tool contracts must expose an object schema even when a definition omits one")
	}
	if !containsContractString(media.Presentation.ResultFields, "attachments") {
		t.Fatalf("media result fields = %+v, want attachments", media.Presentation.ResultFields)
	}

	dispatched, rpcErr := a.Dispatch(context.Background(), contracts.MethodToolContracts, json.RawMessage(`{"workspace":""}`))
	if rpcErr != nil {
		t.Fatalf("Dispatch(%s): %v", contracts.MethodToolContracts, rpcErr)
	}
	if _, ok := dispatched.(contracts.ToolContractsResult); !ok {
		t.Fatalf("dispatched result type = %T, want contracts.ToolContractsResult", dispatched)
	}
}

func containsContractString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func findToolContract(tools []contracts.ToolContractDTO, name string) (contracts.ToolContractDTO, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return contracts.ToolContractDTO{}, false
}
