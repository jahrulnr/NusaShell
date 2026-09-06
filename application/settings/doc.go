// Package settings owns the settings surface: dispatch,
// handlers, and the live settings watcher. Settings mutations emit Bus events
// and AppliedHook callbacks so subsystems react without importing this package.
package settings
