// Package subagent owns ACP subagent delegation: spawn-only
// runtimes, risk tiers, permission policy, completion handling, and the
// delegate agent. It depends on agent turn execution through an injected
// runner interface, not on the agent package internals.
package subagent
