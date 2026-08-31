package contracts

import (
	"encoding/json"
	"testing"
)

func TestToolContractWireShape(t *testing.T) {
	ref := ToolContractRefDTO{
		ID: "tool.file_read.v1", Version: ToolContractVersion, CSSClass: "agent-tool-file-read",
	}
	in := ToolPresentationDTO{
		Variant:  "file-content",
		Action:   "File read",
		Request:  `file_read({"path":"README.md"})`,
		Contract: &ref,
		Result: ToolPresentationResultDTO{
			Format:      "code",
			Summary:     "Read 12 bytes",
			Text:        "hello",
			Language:    "markdown",
			Attachments: []AttachmentDTO{{Type: "file", Name: "README.md", MediaType: "text/markdown", FilePath: "/workspace/README.md"}},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out ToolPresentationDTO
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Contract == nil || out.Contract.ID != ref.ID || out.Contract.CSSClass != ref.CSSClass {
		t.Fatalf("contract ref was not round-tripped: %+v", out.Contract)
	}
	if len(out.Result.Attachments) != 1 || out.Result.Attachments[0].FilePath != "/workspace/README.md" {
		t.Fatalf("result attachments were not round-tripped: %+v", out.Result.Attachments)
	}
}

func TestToolContractsResultWireShape(t *testing.T) {
	in := ToolContractsResult{
		Version: ToolContractVersion,
		Tools: []ToolContractDTO{{
			Name:        "file_read",
			Description: "Read a file",
			ID:          "tool.file_read.v1",
			Version:     ToolContractVersion,
			CSSClass:    "agent-tool-file-read",
			InputSchema: map[string]any{"type": "object"},
			Presentation: ToolContractPresentationDTO{
				Variants:        []string{"file-content"},
				Formats:         []string{"code"},
				RequestFields:   []string{"path"},
				ResultFields:    []string{"summary", "text", "language", "attachments"},
				AttachmentTypes: []string{"file"},
			},
		}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out ToolContractsResult
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Version != ToolContractVersion || len(out.Tools) != 1 || out.Tools[0].Presentation.Formats[0] != "code" {
		t.Fatalf("catalog was not round-tripped: %+v", out)
	}
	assertGolden(t, "tool-contract.json", in)
}
