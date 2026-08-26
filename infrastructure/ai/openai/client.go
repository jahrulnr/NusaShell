package openai

import "nusashell/infrastructure/ai/core"

// NewClient builds the provider from cfg and wraps it in a ready *core.Client.
// It is a convenience for the common single-provider case. It calls New(cfg)
// and then core.New(provider, opts...).
func NewClient(cfg Config, opts ...core.ClientOption) (*core.Client, error) {
	p, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return core.New(p, opts...)
}
