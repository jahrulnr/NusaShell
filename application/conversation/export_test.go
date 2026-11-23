package appconversation

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// AgentFixtureForTest exposes the scripted-agent fixture to external test
// packages that need other application packages alongside it.
func AgentFixtureForTest(t *testing.T, opts ...Option) (*ConversationApp, interface{ Prompts() []dservice.AgentPrompt }) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll, opts...)
	return app, rt
}

// Prompts returns the prompts the scripted agent received.
func (a *scriptedAgent) Prompts() []dservice.AgentPrompt {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]dservice.AgentPrompt(nil), a.prompts...)
}
