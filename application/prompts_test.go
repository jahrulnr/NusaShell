package application

import (
	"strings"
	"testing"

	"nusashell/domain"
)

func TestBuildSystemPromptWithoutUserPrompt(t *testing.T) {
	c := &domain.Conversation{ID: "c1"}
	prompt := buildSystemPrompt(c, "")
	if strings.Contains(prompt, "<user_instructions>") {
		t.Error("system prompt should not contain user_instructions tags when userPrompt is empty")
	}
}

func TestBuildSystemPromptWithUserPrompt(t *testing.T) {
	c := &domain.Conversation{ID: "c1"}
	prompt := buildSystemPrompt(c, "Always respond in Indonesian.")
	if !strings.Contains(prompt, "<user_instructions>") {
		t.Error("system prompt should contain <user_instructions> open tag")
	}
	if !strings.Contains(prompt, "</user_instructions>") {
		t.Error("system prompt should contain </user_instructions> close tag")
	}
	if !strings.Contains(prompt, "Always respond in Indonesian.") {
		t.Error("system prompt should contain the user prompt text")
	}
}

func TestBuildSystemPromptUserPromptPlacement(t *testing.T) {
	c := &domain.Conversation{
		ID:        "c1",
		Workspace: "/tmp/project",
		Messages: []domain.Message{
			{Role: domain.RoleSystem, Content: "SYSTEM_MSG_MARKER"},
		},
	}
	prompt := buildSystemPrompt(c, "USER_PROMPT_MARKER")
	userPromptIdx := strings.Index(prompt, "USER_PROMPT_MARKER")
	systemMsgIdx := strings.Index(prompt, "SYSTEM_MSG_MARKER")
	workspaceIdx := strings.Index(prompt, "/tmp/project")
	if userPromptIdx == -1 || systemMsgIdx == -1 || workspaceIdx == -1 {
		t.Fatal("markers not found in prompt")
	}
	if userPromptIdx > systemMsgIdx {
		t.Error("user prompt should appear before system messages")
	}
	if systemMsgIdx > workspaceIdx {
		t.Error("system messages should appear before workspace")
	}
}

func TestBuildSystemPromptWhitespaceUserPromptIgnored(t *testing.T) {
	c := &domain.Conversation{ID: "c1"}
	prompt := buildSystemPrompt(c, "   \n\t  ")
	if strings.Contains(prompt, "<user_instructions>") {
		t.Error("whitespace-only user prompt should not be injected")
	}
}

func TestAgentPromptsUseCanonicalDocIDs(t *testing.T) {
	prompt := strings.Join([]string{systemPrompt, toolsPrompt, subagentDelegationPrompt}, "\n")
	for _, stale := range []string{"`mcp.md`", "`agent-attachments.md`", "`automation.md`", "`agent-subagents.md`"} {
		if strings.Contains(prompt, stale) {
			t.Errorf("prompt contains non-canonical documentation id %s", stale)
		}
	}
	for _, canonical := range []string{"`mcp`", "`agent-attachments`", "`automation`", "`agent-subagents`"} {
		if !strings.Contains(prompt, canonical) {
			t.Errorf("prompt missing canonical documentation id %s", canonical)
		}
	}
}

func TestAgentPromptRoutesCommonToolWorkflows(t *testing.T) {
	prompt := strings.Join(strings.Fields(systemPrompt+"\n"+toolsPrompt), " ")
	for _, expected := range []string{
		"Skip TODOs for one-step answers or lookups",
		"docs_read",
		"docs_search",
		"skill_read",
		"web_search",
		"web_fetch",
		"ask_question",
		"memory_search",
		"subagent",
	} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("agent prompt missing workflow routing guidance for %q", expected)
		}
	}
}

func TestSystemPromptRoutesArtifactGuidance(t *testing.T) {
	prompt := strings.Join(strings.Fields(systemPrompt), " ")
	for _, expected := range []string{
		"artifact_create",
		"artifact_update",
		"prototypes, minigames, dashboards, simulations",
		"sandboxed iframe",
		"prefer reusing CDNs",
		"64k token budget",
		"width and height are required",
		"640x480",
	} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("system prompt missing artifact guidance for %q", expected)
		}
	}
}

func TestSystemPromptMemorySelectivity(t *testing.T) {
	prompt := strings.Join(strings.Fields(systemPrompt), " ")
	for _, expected := range []string{
		"deliberate commit, not a default",
		"would look up and not find in docs, skills, code, or recent conversation",
		"memory_search",
		"memory_replace",
		"redundant fragments are noise",
	} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("system prompt missing memory selectivity guidance for %q", expected)
		}
	}
}

func TestSystemPromptRoutesIntentEvidenceAndRisk(t *testing.T) {
	prompt := strings.Join(strings.Fields(systemPrompt), " ")
	for _, expected := range []string{
		"discussion or an execution task",
		"fictional or factual",
		"Treat user-provided factual claims as claims, not verified truth",
		"act as the relevant professional",
		"For software discussions, act as an expert developer",
		"predictable or unpredictable",
		"happy path",
		"edge cases",
		"worst case",
		"cost",
		"latency",
		"reversibility",
		"observed facts, sourced facts, assumptions, and inferences",
		"For troubleshooting, reproduce or inspect",
		"For comparisons, define",
		"For forecasts, use ranges",
	} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("system prompt missing intent/evidence guidance for %q", expected)
		}
	}
	searchIndex := strings.Index(prompt, "web_search")
	fetchIndex := strings.Index(prompt, "web_fetch")
	answerIndex := strings.Index(prompt, "web_answer")
	if searchIndex < 0 || fetchIndex < searchIndex || answerIndex < fetchIndex {
		t.Fatalf("research tools must be ordered web_search -> web_fetch -> web_answer: %d, %d, %d", searchIndex, fetchIndex, answerIndex)
	}
}
