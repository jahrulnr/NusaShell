package repository

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
)

// AgentSecretKey names the vault entry for one launch environment value of
// an agent. Management and persistence share it when writing a "secret:"
// reference.
func AgentSecretKey(agentID, envName string) string {
	return "agent/" + agentID + "/env/" + envName
}

// Agent is the store of configured external agents. A missing id is not
// found, reported with a stable error identity.
//
// Environment values are secret material by default: a literal passed to
// Save is moved into the SecretStore and the record keeps a reference.
type Agent interface {
	// Save upserts the agent record.
	Save(ctx context.Context, a *aggregate.Agent) error
	// Load returns one agent with its environment resolved for launch.
	Load(ctx context.Context, id string) (*aggregate.Agent, error)
	// Inspect returns one agent with environment refs left unresolved —
	// the management read that never exposes secret values.
	Inspect(ctx context.Context, id string) (*aggregate.Agent, error)
	// List returns every configured agent, refs unresolved.
	List(ctx context.Context) ([]aggregate.Agent, error)
	// Delete removes the record and the secrets it owns.
	Delete(ctx context.Context, id string) error
}
