package repository

import (
	"context"
	"encoding/json"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// AgentCatalog is the offered surface an agent last reported: its config
// options and model list in the picker's mapped form (composite model ids
// kept verbatim — they are wire syntax, not display names), the agent's
// capabilities, and when that surface was last seen. The picker reads it
// when no live session or probe is fresher: it is what survives a restart,
// and what makes a stored pick's id "known" rather than refused.
type AgentCatalog struct {
	Options      []dservice.AgentOption `json:"options,omitempty"`
	Models       []dservice.AgentModel  `json:"models,omitempty"`
	CurrentModel string                 `json:"currentModel,omitempty"`
	Caps         json.RawMessage        `json:"caps,omitempty"`
	ProbedAt     time.Time              `json:"probedAt"`
}

// AgentCatalogStore persists one catalog per agent, written every time an
// agent's offered list is seen — session open, config_option_update, a
// set_config_option response, a probe. A missing catalog reads as the zero
// value with no error: absence is normal before the first session.
type AgentCatalogStore interface {
	// Catalog returns the agent's last seen catalog, zero when none.
	Catalog(ctx context.Context, agentID string) (AgentCatalog, error)
	// SaveCatalog upserts the agent's catalog atomically.
	SaveCatalog(ctx context.Context, agentID string, cat AgentCatalog) error
}
