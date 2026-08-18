package domain

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestInferRiskTierExplicitMappingWins(t *testing.T) {
	mappings := []ModeRiskMapping{
		{ModeID: "code", Tier: RiskReadOnly},
		{ModeID: "weird", Tier: RiskEditConfirmed},
	}
	if got := InferRiskTier("code", mappings); got != RiskReadOnly {
		t.Fatalf("explicit mapping: got %s", got)
	}
	if got := InferRiskTier("weird", mappings); got != RiskEditConfirmed {
		t.Fatalf("unknown-looking id with mapping: got %s", got)
	}
}

func TestInferRiskTierUnknownIsReadOnly(t *testing.T) {
	if got := InferRiskTier("totally-new-mode", nil); got != RiskReadOnly {
		t.Fatalf("unknown mode must be read_only, got %s", got)
	}
	if got := InferRiskTier("", nil); got != RiskReadOnly {
		t.Fatalf("empty mode must be read_only, got %s", got)
	}
}

func TestInferRiskTierHeuristics(t *testing.T) {
	cases := map[string]RiskTier{
		"plan":              RiskReadOnly,
		"architect":         RiskReadOnly,
		"ask":               RiskReadOnly,
		"acceptEdits":       RiskEditConfirmed,
		"code":              RiskEditConfirmed,
		"default":           RiskEditConfirmed,
		"bypassPermissions": RiskBypass,
		"yolo":              RiskBypass,
	}
	for id, want := range cases {
		if got := InferRiskTier(id, nil); got != want {
			t.Errorf("%s: got %s want %s", id, got, want)
		}
	}
}

func TestStrictestAvailableMode(t *testing.T) {
	modes := []AcpMode{
		{ID: "bypassPermissions", Name: "Bypass"},
		{ID: "code", Name: "Code"},
		{ID: "plan", Name: "Plan"},
	}
	got := StrictestAvailableMode(modes, nil)
	if got != "plan" {
		t.Fatalf("got %q want plan", got)
	}
	if StrictestAvailableMode(nil, nil) != "" {
		t.Fatal("empty modes should return empty")
	}
}

func TestWrapSessionAuthError(t *testing.T) {
	agent := &AcpAgent{
		Name: "Cursor",
		CachedAuthMethods: []AcpAuthMethod{
			{ID: "cursor_login", Name: "Cursor Login"},
		},
	}
	upstream := fmt.Errorf("acp Authentication required")
	got := WrapSessionAuthError(agent, upstream)
	if got == upstream {
		t.Fatal("expected wrapped error")
	}
	if !strings.Contains(got.Error(), "not authenticated") {
		t.Fatalf("got %q", got.Error())
	}
	if !strings.Contains(got.Error(), "cursor_login") {
		t.Fatalf("got %q", got.Error())
	}
	agent.AuthMethodID = "cursor_login"
	if WrapSessionAuthError(agent, upstream) != upstream {
		t.Fatal("should not wrap when auth method already set")
	}
	if WrapSessionAuthError(agent, nil) != nil {
		t.Fatal("nil err")
	}
}

func TestSeedModeRiskMappingsPreservesOverrides(t *testing.T) {
	modes := []AcpMode{{ID: "plan"}, {ID: "code"}}
	existing := []ModeRiskMapping{{ModeID: "code", Tier: RiskBypass}}
	got := SeedModeRiskMappings(modes, existing)
	if len(got) != 2 {
		t.Fatalf("len %d", len(got))
	}
	byID := map[string]RiskTier{}
	for _, m := range got {
		byID[m.ModeID] = m.Tier
	}
	if byID["plan"] != RiskReadOnly {
		t.Errorf("plan seeded %s", byID["plan"])
	}
	if byID["code"] != RiskBypass {
		t.Errorf("code override lost: %s", byID["code"])
	}
}

func TestDecideAcpPermission(t *testing.T) {
	ws := filepath.Join(string(filepath.Separator), "proj")
	in := filepath.Join(ws, "main.go")
	out := filepath.Join(string(filepath.Separator), "etc", "passwd")

	t.Run("bypass auto-allows exec", func(t *testing.T) {
		d := DecideAcpPermission(RiskBypass, "execute", []string{out}, ws)
		if !d.Auto || d.Outcome != PermissionAllowOnce {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("read_only auto-allows read", func(t *testing.T) {
		d := DecideAcpPermission(RiskReadOnly, "read", []string{in}, ws)
		if !d.Auto {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("read_only prompts on edit", func(t *testing.T) {
		d := DecideAcpPermission(RiskReadOnly, "edit", []string{in}, ws)
		if d.Auto {
			t.Fatalf("should prompt: %+v", d)
		}
	})
	t.Run("edit_confirmed auto-allows workspace edit", func(t *testing.T) {
		d := DecideAcpPermission(RiskEditConfirmed, "edit", []string{in}, ws)
		if !d.Auto {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("edit_confirmed prompts outside workspace", func(t *testing.T) {
		d := DecideAcpPermission(RiskEditConfirmed, "edit", []string{out}, ws)
		if d.Auto {
			t.Fatalf("should prompt: %+v", d)
		}
	})
	t.Run("edit_confirmed prompts on exec", func(t *testing.T) {
		d := DecideAcpPermission(RiskEditConfirmed, "execute", []string{in}, ws)
		if d.Auto {
			t.Fatalf("should prompt: %+v", d)
		}
	})
	t.Run("invalid tier treated as read_only", func(t *testing.T) {
		d := DecideAcpPermission(RiskTier("nope"), "execute", nil, ws)
		if d.Auto {
			t.Fatalf("should prompt: %+v", d)
		}
	})
}

func TestPathsWithinWorkspaceSlashRootedOutside(t *testing.T) {
	ws := filepath.Join(string(filepath.Separator), "proj")
	out := filepath.Join(string(filepath.Separator), "etc", "passwd")
	if pathsWithinWorkspace([]string{out}, ws) {
		t.Fatalf("slash-rooted %q must be outside %q", out, ws)
	}
	if pathsWithinWorkspace([]string{"/etc/passwd"}, ws) {
		t.Fatal("unix-style absolute path must be outside workspace")
	}
	in := filepath.Join(ws, "main.go")
	if !pathsWithinWorkspace([]string{in}, ws) {
		t.Fatalf("inside %q should be within %q", in, ws)
	}
	if !pathsWithinWorkspace([]string{"main.go"}, ws) {
		t.Fatal("relative in-workspace path should join and pass")
	}
	if pathsWithinWorkspace([]string{"../secret"}, ws) {
		t.Fatal("relative escape must fail")
	}
	dots := filepath.Join(ws, "...hidden")
	if !pathsWithinWorkspace([]string{dots}, ws) {
		t.Fatal("...hidden inside workspace is not an escape")
	}
	if pathsWithinWorkspace([]string{""}, ws) {
		t.Fatal("empty path is not within workspace")
	}
}

func TestHostRootedPathPortable(t *testing.T) {
	if hostRootedPath("ui/index.html") || hostRootedPath("dist/app.bin") {
		t.Fatal("relative paths must not be host-rooted")
	}
	for _, p := range []string{"/etc/passwd", `\Windows\System32`, `C:\secrets\out.bin`, `c:\Temp\x`} {
		if !hostRootedPath(p) {
			t.Fatalf("%q must be host-rooted on every GOOS", p)
		}
	}
}

func TestSamplePermissionPaths(t *testing.T) {
	paths := make([]string, MaxAcpPermissionPaths+3)
	for i := range paths {
		paths[i] = "p"
	}
	got := SamplePermissionPaths(paths)
	if len(got) != MaxAcpPermissionPaths+1 || got[len(got)-1] != "…" {
		t.Fatalf("got %#v", got)
	}
	small := SamplePermissionPaths([]string{"a", "b"})
	if len(small) != 2 {
		t.Fatalf("small: %#v", small)
	}
}

func TestClassifyModelTierUnclassifiedByDefault(t *testing.T) {
	if got := ClassifyModelTier("mystery-model", ""); got != ModelTierUnclassified {
		t.Fatalf("got %s", got)
	}
	if got := ClassifyModelTier("claude-opus-4.6", ""); got != ModelTierFrontier {
		t.Fatalf("opus: %s", got)
	}
	if got := ClassifyModelTier("claude-haiku", ""); got != ModelTierEconomy {
		t.Fatalf("haiku: %s", got)
	}
}

func TestAcpRunLiveAndTranscriptCap(t *testing.T) {
	r := &AcpRun{Status: AcpRunRunning}
	if !r.Live() {
		t.Fatal("running should be live")
	}
	r.Status = AcpRunCompleted
	if r.Live() {
		t.Fatal("completed should not be live")
	}
	r.Status = AcpRunRunning
	chunk := AcpTranscriptChunk{Text: strings.Repeat("x", 1024)}
	for i := 0; i < (MaxAcpTranscriptBytes/1024)+8; i++ {
		r.AppendTranscript(chunk)
	}
	total := 0
	for _, c := range r.Transcript {
		total += len(c.Text)
	}
	if total > MaxAcpTranscriptBytes+1024 {
		t.Fatalf("transcript not capped: %d", total)
	}
}

// TestAppendTranscriptMergesConsecutiveTextChunks verifies that streaming
// agent_message_chunk updates (often one char/token each) are merged into a
// single text chunk so the UI does not render one line per delta.
func TestAppendTranscriptMergesConsecutiveTextChunks(t *testing.T) {
	r := &AcpRun{}
	r.AppendTranscript(AcpTranscriptChunk{Kind: "text", Text: "H"})
	r.AppendTranscript(AcpTranscriptChunk{Kind: "text", Text: "i"})
	r.AppendTranscript(AcpTranscriptChunk{Kind: "text", Text: "!"})
	r.AppendTranscript(AcpTranscriptChunk{Kind: "tool", ToolID: "t1", ToolTitle: "ls"})
	r.AppendTranscript(AcpTranscriptChunk{Kind: "text", Text: "done"})
	r.AppendTranscript(AcpTranscriptChunk{Kind: "thought", Text: "think"})
	r.AppendTranscript(AcpTranscriptChunk{Kind: "thought", Text: " more"})

	want := []struct {
		kind string
		text string
	}{
		{"text", "Hi!"},
		{"tool", ""},
		{"text", "done"},
		{"thought", "think more"},
	}
	if len(r.Transcript) != len(want) {
		t.Fatalf("transcript len = %d, want %d: %+v", len(r.Transcript), len(want), r.Transcript)
	}
	for i, w := range want {
		if r.Transcript[i].Kind != w.kind {
			t.Fatalf("chunk %d kind = %q, want %q", i, r.Transcript[i].Kind, w.kind)
		}
		if w.text != "" && r.Transcript[i].Text != w.text {
			t.Fatalf("chunk %d text = %q, want %q", i, r.Transcript[i].Text, w.text)
		}
	}
}

func TestValidateAcpAgentSave(t *testing.T) {
	if ValidateAcpAgentSave("", "cursor") == "" {
		t.Fatal("empty name")
	}
	if ValidateAcpAgentSave("Cursor", "") == "" {
		t.Fatal("empty command")
	}
	if ValidateAcpAgentSave("Cursor", "cursor") != "" {
		t.Fatal("valid")
	}
}

func TestRedactEnvKeys(t *testing.T) {
	keys := RedactEnvKeys(map[string]string{"TOKEN": "secret", "HOME": "/tmp"})
	if len(keys) != 2 {
		t.Fatalf("%v", keys)
	}
	joined := strings.Join(keys, ",")
	if strings.Contains(joined, "secret") {
		t.Fatalf("leaked value: %v", keys)
	}
}

func TestIsSubagentResultCallID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"subagent-result-acprun_abc", true},
		{"subagent-result-", true},
		{"subagent-result", false},
		{"hydrate-runtime_context", false},
		{"call_abc", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsSubagentResultCallID(c.id); got != c.want {
			t.Errorf("IsSubagentResultCallID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}
