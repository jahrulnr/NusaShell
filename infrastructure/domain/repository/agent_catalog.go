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
	Options []dservice.AgentOption `json:"options,omitempty"`
	Models  []dservice.AgentModel  `json:"models,omitempty"`
	// ModelsReported is true when the session reported a model list,
	// including a list with no models. Absent, with no models stored,
	// means the session did not report one — that is unknown, not an
	// empty catalog.
	ModelsReported bool            `json:"modelsReported,omitempty"`
	CurrentModel   string          `json:"currentModel,omitempty"`
	Caps           json.RawMessage `json:"caps,omitempty"`
	ProbedAt       time.Time       `json:"probedAt"`
}

// AgentCommandCatalog is the slash-command surface one agent session last
// reported for one conversation. The conversation key matters because the
// list may depend on that session; the agent id protects a room that later
// switches agents from inheriting the previous agent's surface.
type AgentCommandCatalog struct {
	AgentID    string                  `json:"agentId"`
	Commands   []dservice.AgentCommand `json:"commands"`
	ReportedAt time.Time               `json:"reportedAt"`
}

// AgentCatalogStore persists one catalog per agent plus one command report per conversation, written every time an
// agent's offered list is seen — session open, config_option_update, a
// set_config_option response, a probe. A missing catalog reads as the zero
// value with no error: absence is normal before the first session.
type AgentCatalogStore interface {
	// Catalog returns the agent's last seen catalog, zero when none.
	Catalog(ctx context.Context, agentID string) (AgentCatalog, error)
	// SaveCatalog upserts the agent's catalog atomically.
	SaveCatalog(ctx context.Context, agentID string, cat AgentCatalog) error
	// CommandCatalog returns the commands one conversation's session last
	// reported, zero when none. It survives process idle-stops and restarts.
	CommandCatalog(ctx context.Context, conversationID string) (AgentCommandCatalog, error)
	// SaveCommandCatalog replaces — never merges — the conversation's last
	// reported command list atomically.
	SaveCommandCatalog(ctx context.Context, conversationID string, cat AgentCommandCatalog) error
	// DeleteCommandCatalog removes the conversation's report; absence is success.
	DeleteCommandCatalog(ctx context.Context, conversationID string) error
}
