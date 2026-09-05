package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

func TestExtractJSONFromTextPlainJSON(t *testing.T) {
	input := `[{"kind":"memory.upsert","payload":{"body":"test"}}]`
	got := extractJSONFromText(input)
	if got != input {
		t.Fatalf("plain JSON: got %q, want %q", got, input)
	}
}

func TestExtractJSONFromTextMarkdownFence(t *testing.T) {
	input := "Here is the result:\n```json\n[{\"kind\":\"memory.upsert\"}]\n```\nDone."
	got := extractJSONFromText(input)
	want := `[{"kind":"memory.upsert"}]`
	if got != want {
		t.Fatalf("fenced JSON: got %q, want %q", got, want)
	}
}

func TestExtractJSONFromTextProseBeforeArray(t *testing.T) {
	input := "I analyzed the experience.\n[{\"kind\":\"memory.upsert\"}]\nThat is all."
	got := extractJSONFromText(input)
	if !strings.HasPrefix(got, "[") {
		t.Fatalf("prose+array: got %q", got)
	}
	var arr []llmProposedOp
	if err := json.Unmarshal([]byte(got), &arr); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestParseLLMOperationsValid(t *testing.T) {
	text := `[{"kind":"memory.upsert","payload":{"body":"prefers Go","type":"preference"},"reason":"explicit teaching"},{"kind":"memory.strengthen","payload":{"id":"mem_123"}}]`
	ops := parseLLMOperations(text, "job_1", "exp_1")
	if len(ops) != 2 {
		t.Fatalf("ops=%d, want 2", len(ops))
	}
	if ops[0].Kind != domain.OpMemoryUpsert {
		t.Fatalf("op0 kind=%s, want %s", ops[0].Kind, domain.OpMemoryUpsert)
	}
	if ops[0].Actor != domain.ActorConsolidator {
		t.Fatalf("op0 actor=%s", ops[0].Actor)
	}
	if ops[1].Kind != domain.OpMemoryStrengthen {
		t.Fatalf("op1 kind=%s, want %s", ops[1].Kind, domain.OpMemoryStrengthen)
	}
}

func TestParseLLMOperationsSkipsSkillOps(t *testing.T) {
	text := `[{"kind":"skill.create","payload":{}},{"kind":"memory.upsert","payload":{"body":"test"}}]`
	ops := parseLLMOperations(text, "job_1", "exp_1")
	if len(ops) != 1 {
		t.Fatalf("ops=%d, want 1 (skill ops must be skipped by consolidator)", len(ops))
	}
	if ops[0].Kind != domain.OpMemoryUpsert {
		t.Fatalf("op0 kind=%s", ops[0].Kind)
	}
}

func TestParseLLMOperationsMalformedReturnsNil(t *testing.T) {
	ops := parseLLMOperations("not json at all", "job_1", "exp_1")
	if ops != nil {
		t.Fatalf("malformed should return nil, got %d ops", len(ops))
	}
}

func TestParseLLMOperationsEmptyArray(t *testing.T) {
	ops := parseLLMOperations("[]", "job_1", "exp_1")
	if len(ops) != 0 {
		t.Fatalf("empty array should return 0 ops, got %d", len(ops))
	}
}

func TestSkillMeetsMinimumBar(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"empty", "", false},
		{"missing purpose", "## Trigger\nx\n## Steps\ny", false},
		{"missing trigger", "## Purpose\nx\n## Steps\ny", false},
		{"missing steps", "## Purpose\nx\n## Trigger\ny", false},
		{"all present", "## Purpose\nx\n## Trigger\ny\n## Steps\nz", true},
		{"lowercase", "## purpose\ntest\n## trigger\nwhen\n## steps\n1. do", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := skillMeetsMinimumBar(tc.body); got != tc.want {
				t.Fatalf("skillMeetsMinimumBar(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestDeterministicSkillBodyMeetsMinimumBar(t *testing.T) {
	app := &App{}
	exp := &domain.Experience{
		ID:   "exp_test",
		Goal: "debug nginx upload",
		Actions: []domain.ExperienceAction{
			{Name: "file_read"}, {Name: "grep"}, {Name: "exec"},
		},
	}
	body, desc := app.deterministicSkillBody(exp)
	if !skillMeetsMinimumBar(body) {
		t.Fatalf("deterministic skill body does not meet minimum bar:\n%s", body)
	}
	if desc == "" {
		t.Fatal("description should not be empty")
	}
	if !strings.Contains(body, "## Purpose") {
		t.Fatal("missing Purpose section")
	}
	if !strings.Contains(body, "## Trigger") {
		t.Fatal("missing Trigger section")
	}
	if !strings.Contains(body, "## Steps") {
		t.Fatal("missing Steps section")
	}
	if !strings.Contains(body, "## Verification") {
		t.Fatal("missing Verification section")
	}
}

func TestParseLLMSkillProposalValid(t *testing.T) {
	text := `{"kind":"skill.create","name":"debug-nginx-upload","purpose":"Debug nginx upload failures","trigger":"upload returns 403","steps":"1. inspect nginx root\n2. check symlink","verification":"curl returns 200"}`
	prop := parseLLMSkillProposal(text)
	if prop == nil {
		t.Fatal("expected valid proposal")
	}
	if prop.Kind != "skill.create" {
		t.Fatalf("kind=%s", prop.Kind)
	}
	if prop.Purpose != "Debug nginx upload failures" {
		t.Fatalf("purpose=%s", prop.Purpose)
	}
}

func TestParseLLMSkillProposalMissingSteps(t *testing.T) {
	text := `{"kind":"skill.create","name":"test","purpose":"x","trigger":"y"}`
	prop := parseLLMSkillProposal(text)
	if prop != nil {
		t.Fatal("proposal without steps should return nil")
	}
}

func TestParseLLMSkillProposalWrongKind(t *testing.T) {
	text := `{"kind":"skill.promote","name":"test","steps":"1. do"}`
	prop := parseLLMSkillProposal(text)
	if prop != nil {
		t.Fatal("skill.promote should return nil")
	}
}

// stubCoreProvider is a minimal core.Provider that returns a fixed text response.
type stubCoreProvider struct {
	text string
}

func (s *stubCoreProvider) Name() string { return "stub" }
func (s *stubCoreProvider) Chat(_ context.Context, _ *core.Request) (*core.Response, error) {
	return &core.Response{Blocks: []core.Block{core.TextBlock{Text: s.text}}}, nil
}
func (s *stubCoreProvider) Stream(_ context.Context, _ *core.Request) (core.Stream, error) {
	return nil, nil
}

func TestConsolidateViaLLMWithProvider(t *testing.T) {
	llmResponse := `[{"kind":"memory.upsert","payload":{"body":"User prefers dark mode for IDE","type":"preference","scope":"user"},"reason":"explicit teaching","risk":"low"}]`
	records := &fakeMemoryRecordStore{}
	app := &App{
		Experiences:   &fakeExperienceStore{items: []*domain.Experience{{ID: "exp_llm", Goal: "remember I prefer dark mode", Signals: domain.ExperienceSignals{ExplicitTeaching: true}}}},
		MemoryRecords: records,
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"stub": {ID: "stub", Name: "Stub", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{{ID: "stub-model"}}},
		}},
		Credentials: &memCreds{},
		Factory: func(_ context.Context, _ *domain.Provider, _ string) (core.Provider, error) {
			return &stubCoreProvider{text: llmResponse}, nil
		},
		Settings: &fakeSettings{},
	}
	if err := app.consolidateJob(&domain.LearningJob{ID: "job_llm", ExperienceID: "exp_llm", Kind: domain.LearningJobConsolidate}); err != nil {
		t.Fatal(err)
	}
	if len(records.List()) != 1 {
		t.Fatalf("records=%d, want 1", len(records.List()))
	}
	if !strings.Contains(records.List()[0].Body, "dark mode") {
		t.Fatalf("record body=%q", records.List()[0].Body)
	}
}

func TestConsolidateViaLLMFallsBackWhenNoProvider(t *testing.T) {
	// No Providers/Factory set: should fall back to deterministic teachingOps.
	exp := &domain.Experience{
		ID:      "exp_fb",
		Goal:    "remember I prefer Go",
		Signals: domain.ExperienceSignals{ExplicitTeaching: true},
	}
	records := &fakeMemoryRecordStore{}
	app := &App{
		Experiences:   &fakeExperienceStore{items: []*domain.Experience{exp}},
		MemoryRecords: records,
	}
	if err := app.consolidateJob(&domain.LearningJob{ID: "job_fb", ExperienceID: "exp_fb"}); err != nil {
		t.Fatal(err)
	}
	if len(records.List()) != 1 {
		t.Fatalf("fallback records=%d, want 1", len(records.List()))
	}
	if !strings.Contains(records.List()[0].Body, "Go") {
		t.Fatalf("fallback body=%q", records.List()[0].Body)
	}
}

func TestEvolveSkillJobDeterministicBodyMeetsBar(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{}}
	exp := &domain.Experience{
		ID:   "exp_bar",
		Goal: "debug nginx config",
		Actions: []domain.ExperienceAction{
			{Name: "file_read"}, {Name: "grep"}, {Name: "exec"},
		},
	}
	app := &App{
		Skills:      skills,
		Experiences: &fakeExperienceStore{items: []*domain.Experience{exp}},
	}
	if err := app.evolveSkillJob(&domain.LearningJob{ExperienceID: "exp_bar", Kind: domain.LearningJobEvolveSkill}); err != nil {
		t.Fatal(err)
	}
	var got *domain.Skill
	for _, s := range skills.List() {
		got = s
	}
	if got == nil {
		t.Fatal("expected a learned skill")
	}
	if !skillMeetsMinimumBar(got.Content) {
		t.Fatalf("skill content does not meet minimum bar:\n%s", got.Content)
	}
}

func TestEvolveSkillJobSkipsBelowBarBody(t *testing.T) {
	// Experience with only 2 actions: should not generate a skill at all.
	skills := &fakeSkillStore{items: map[string]*domain.Skill{}}
	exp := &domain.Experience{
		ID:   "exp_skip",
		Goal: "quick edit",
		Actions: []domain.ExperienceAction{
			{Name: "file_patch"},
			{Name: "exec"},
		},
	}
	app := &App{
		Skills:      skills,
		Experiences: &fakeExperienceStore{items: []*domain.Experience{exp}},
	}
	if err := app.evolveSkillJob(&domain.LearningJob{ExperienceID: "exp_skip", Kind: domain.LearningJobEvolveSkill}); err != nil {
		t.Fatal(err)
	}
	if len(skills.List()) != 0 {
		t.Fatalf("expected 0 skills for 2-action experience, got %d", len(skills.List()))
	}
}
