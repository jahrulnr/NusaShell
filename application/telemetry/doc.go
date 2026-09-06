// Package telemetry owns the telemetry dashboard surface:
// token usage, cost, and time-bucket aggregation exposed over RPC. It
// is read-only over conversation and provider ports; it never imports
// sibling feature packages.
package telemetry
