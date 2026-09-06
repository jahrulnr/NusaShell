// Package conversation owns persisted conversations: transcript repository
// (append-only), room RPC, messaging, todos, workspace selection,
// instruction-file discovery, and journal housekeeping. It never imports
// sibling feature packages; cross-feature reactions subscribe to Bus events.
//
// Agent turn-loop policy (conversation_agent_rules.go) stays on the root
// application package until the agent/ move: those rules hold *App and
// would cycle if imported from here.
package conversation
