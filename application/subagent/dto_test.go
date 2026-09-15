package subagent

import (
	"encoding/json"
	"strings"
	"testing"

	"nusashell/domain"
)

// runDTO must carry the agent-provided tool input/output to the UI drawer;
// the wire keeps its snake_case field names.
func TestRunDTOCarriesToolInputAndOutput(t *testing.T) {
	run := &domain.AcpRun{Transcript: []domain.AcpTranscriptChunk{{
		Kind:       "tool",
		ToolID:     "call_1",
		ToolTitle:  "Read SKILL.md",
		ToolKind:   "read",
		ToolStatus: "completed",
		ToolInput:  "{\n  \"path\": \"SKILL.md\"\n}",
		ToolOutput: "file body",
	}}}

	dto := runDTO(run)
	if len(dto.Transcript) != 1 {
		t.Fatalf("transcript = %+v", dto.Transcript)
	}
	chunk := dto.Transcript[0]
	if chunk.ToolInput == "" || chunk.ToolOutput != "file body" {
		t.Fatalf("tool io missing from DTO: %+v", chunk)
	}
	encoded, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"tool_input"`, `"tool_output"`} {
		if !strings.Contains(string(encoded), key) {
			t.Fatalf("wire JSON missing %s: %s", key, encoded)
		}
	}
}
