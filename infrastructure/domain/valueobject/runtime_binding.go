package valueobject

import (
	"errors"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// RuntimeBinding is one picker target: an agent, or a provider route.
// It is not room state. The zero value is absence, not a kind. An empty,
// partial, or mixed target cannot be built.
type RuntimeBinding struct {
	kind       enum.Runtime
	agentID    string
	agentModel string
	providerID string
	modelID    string
}

// NewAgentBinding builds an agent target. The agent id is required. The
// model the agent reports is optional.
func NewAgentBinding(agentID, agentModel string) (RuntimeBinding, error) {
	agentID = strings.TrimSpace(agentID)
	agentModel = strings.TrimSpace(agentModel)
	if agentID == "" {
		return RuntimeBinding{}, errors.New("an agent binding needs an agent id")
	}
	if err := ValidID(agentID); err != nil {
		return RuntimeBinding{}, errors.New("invalid agent id")
	}
	return RuntimeBinding{kind: enum.RuntimeAgent, agentID: agentID, agentModel: agentModel}, nil
}

// NewHarnessBinding builds a provider route. Both ids are required.
func NewHarnessBinding(providerID, modelID string) (RuntimeBinding, error) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return RuntimeBinding{}, errors.New("a provider binding needs both providerId and modelId")
	}
	return RuntimeBinding{kind: enum.RuntimeHarness, providerID: providerID, modelID: modelID}, nil
}

// IsZero reports absence. A zero binding is not a harness and not an agent.
func (b RuntimeBinding) IsZero() bool { return b.kind == "" }

func (b RuntimeBinding) Kind() enum.Runtime { return b.kind }

func (b RuntimeBinding) AgentID() string { return b.agentID }

func (b RuntimeBinding) AgentModel() string { return b.agentModel }

func (b RuntimeBinding) ProviderID() string { return b.providerID }

func (b RuntimeBinding) ModelID() string { return b.modelID }
