package appchannel

import (
	"context"
	"errors"

	appagent "github.com/jahrulnr/nusashell/application/agent"
	appprovider "github.com/jahrulnr/nusashell/application/provider"
)

// Agents lists configured agents. *appagent.AgentApp satisfies it.
type Agents interface {
	List(ctx context.Context) ([]appagent.View, error)
}

// Providers lists configured providers. *appprovider.ProviderApp satisfies it.
type Providers interface {
	List(ctx context.Context) ([]appprovider.ProviderInfo, error)
}

// ConfiguredCatalog is the Catalog over the configured agents and
// providers. Preferred names the runtime new chat threads start on, read
// on each new thread so a change applies at once; when it is nil or
// answers an empty runtime the first agent wins, then the first usable
// provider's first listed model.
type ConfiguredCatalog struct {
	Agents    Agents
	Providers Providers
	Preferred func() Runtime
}

var _ Catalog = ConfiguredCatalog{}

// Default resolves the runtime for a new chat thread.
func (c ConfiguredCatalog) Default(ctx context.Context) (Runtime, error) {
	if c.Preferred != nil {
		if r := c.Preferred(); !r.empty() {
			return r, nil
		}
	}
	choices, err := c.Choices(ctx)
	if err != nil {
		return Runtime{}, err
	}
	if len(choices) == 0 {
		return Runtime{}, errors.New("no agent or provider is configured")
	}
	return choices[0].Runtime, nil
}

// Choices lists agents first, then each usable provider's listed models.
func (c ConfiguredCatalog) Choices(ctx context.Context) ([]Choice, error) {
	var out []Choice
	if c.Agents != nil {
		agents, err := c.Agents.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range agents {
			out = append(out, Choice{Runtime: Runtime{AgentID: a.ID}, Name: a.Name + " (ACP)"})
		}
	}
	if c.Providers != nil {
		providers, err := c.Providers.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range providers {
			if p.Broken {
				continue
			}
			for _, m := range p.ListedModels() {
				label := m.Name()
				if label == "" {
					label = m.ID()
				}
				out = append(out, Choice{Runtime: Runtime{ProviderID: p.ID, ModelID: m.ID()}, Name: label + " (API)"})
			}
		}
	}
	return out, nil
}
