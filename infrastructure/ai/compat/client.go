package compat

import "nusashell/infrastructure/ai/core"

// NewClient builds the provider from cfg and spec and wraps it in a ready
// *core.Client. It is a convenience for custom OpenAI-compatible endpoints.
// It calls New(cfg, spec) and then core.New(provider, opts...).
func NewClient(cfg Config, spec Spec, opts ...core.ClientOption) (*core.Client, error) {
	p, err := New(cfg, spec)
	if err != nil {
		return nil, err
	}
	return core.New(p, opts...)
}
