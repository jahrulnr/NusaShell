package service

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
)

// VisionReading is the owner's decision at one moment: the model a
// description would run on, and whether sending a file to it is allowed.
type VisionReading struct {
	ProviderID string
	ModelID    string
	// Consent is the separate switch: assigning the model says who would
	// read a file, consent says whether one may be sent at all.
	Consent bool
}

// VisionTarget is that reading resolved: the provider with its credentials,
// the catalog entry, and the text adapter for the model's wire style.
type VisionTarget struct {
	Provider *aggregate.Provider
	Model    entity.Model
	Service  TextModelService
}

// Vision is the bridge a tool asks before it sends a file to a model that can
// see it — read_media's description branch. Both calls are made per tool
// call, so a change in Settings applies without a restart; a tool never loads
// a provider itself.
type Vision interface {
	// ReadSettings reports the owner's vision assignment and consent.
	ReadSettings(ctx context.Context) (VisionReading, error)
	// ResolveModel loads what the assignment names and picks the text
	// adapter for the model's wire style.
	ResolveModel(ctx context.Context, providerID, modelID string) (VisionTarget, error)
}

// VisionFuncs adapts two functions to Vision — the shape the composition root
// wires, where the settings file and the provider repository live.
type VisionFuncs struct {
	Settings func(ctx context.Context) (VisionReading, error)
	Resolve  func(ctx context.Context, providerID, modelID string) (VisionTarget, error)
}

func (v VisionFuncs) ReadSettings(ctx context.Context) (VisionReading, error) {
	return v.Settings(ctx)
}

func (v VisionFuncs) ResolveModel(ctx context.Context, providerID, modelID string) (VisionTarget, error) {
	return v.Resolve(ctx, providerID, modelID)
}
