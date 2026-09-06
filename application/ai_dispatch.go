package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchAI routes ai.* RPC methods (providers, models) to their
// handlers. Called by App.Dispatch for any method whose first segment is
// "ai".
func (a *App) dispatchAI(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return a.providerService().Dispatch(method, payload)
}
