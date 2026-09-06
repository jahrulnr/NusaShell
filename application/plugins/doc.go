// Package plugins owns the unified plugin/MCP model: dispatch
// and handlers for install, register, enable/disable, and tool discovery
// surfaced to the agent. Concrete process and wire adapters stay in
// infrastructure. Cross-feature reactions (skill mount, automation caps)
// go through consumer-side ports on Deps, never sibling feature packages.
package plugins
