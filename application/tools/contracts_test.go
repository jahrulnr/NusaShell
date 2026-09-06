package tools

import (
	"testing"

	"nusashell/contracts"
)

func TestBuildContractMediaAttachmentFields(t *testing.T) {
	media := BuildContract(ToolInfo{Name: "generate_image"})
	if media.InputSchema == nil {
		t.Fatal("tool contracts must expose an object schema even when a definition omits one")
	}
	if !containsContractString(media.Presentation.ResultFields, "attachments") {
		t.Fatalf("media result fields = %+v, want attachments", media.Presentation.ResultFields)
	}
}

func TestBuildContractsSkipsEmptyNames(t *testing.T) {
	got := BuildContracts([]ToolInfo{
		{Name: ""},
		{Name: "file_read", Description: "Read a file", InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		}},
	})
	if got.Version != contracts.ToolContractVersion {
		t.Fatalf("catalog version = %d, want %d", got.Version, contracts.ToolContractVersion)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "file_read" {
		t.Fatalf("tools = %+v", got.Tools)
	}
	if got.Tools[0].ID != "tool.file_read.v1" || got.Tools[0].CSSClass != "agent-tool-file-read" {
		t.Fatalf("file_read identity = %+v", got.Tools[0])
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
