// Package ai is the composition root for AI provider adapters. It wires
// the provider subpackages (messages, responses, chatcompletion) into the
// application.AIProvider contract via Factory.
//
// The cross-provider contract lives in application.AIProvider; this package
// re-exports it as Provider for ergonomic imports from the infrastructure
// layer.
package ai

import "nusashell/application"

// Provider is the contract implemented by all AI provider adapters.
// It is an alias for application.AIProvider so consumers can depend on
// the infrastructure layer without importing application directly.
type Provider = application.AIProvider

// ProviderFactory is the contract for building a provider adapter from
// a stored config and API key.
type ProviderFactory = application.ProviderFactory
