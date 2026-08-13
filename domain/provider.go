package domain

import "time"

// ProviderKind names the wire API shape, not a vendor: Messages (Anthropic
// Messages API), Responses (OpenAI Responses API), Chat (any OpenAI-compatible
// Chat Completions endpoint).
type ProviderKind string

const (
	ProviderMessages  ProviderKind = "messages"
	ProviderResponses ProviderKind = "responses"
	ProviderChat      ProviderKind = "chat"
)

type Model struct {
	ID          string
	Context     int
	InputCost   float64 // USD per 1M input tokens, when known
	Description string
}

type Provider struct {
	ID        string
	Kind      ProviderKind
	Name      string
	BaseURL   string
	Enabled   bool
	HasAPIKey bool
	Models    []Model
	UpdatedAt time.Time
}

func (p *Provider) HasModel(id string) bool {
	for _, m := range p.Models {
		if m.ID == id {
			return true
		}
	}
	return false
}
