package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchAI routes ai.* RPC methods (providers, models) to their
// handlers. Called by App.Dispatch for any method whose first segment is
// "ai".
func (a *App) dispatchAI(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodProvidersList:
		return a.handleProvidersList()
	case contracts.MethodProvidersSave:
		var req contracts.ProviderSaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleProvidersSave(req)
	case contracts.MethodProvidersDelete:
		var req contracts.ProviderIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleProvidersDelete(req)
	case contracts.MethodProvidersTest:
		var req contracts.ProviderIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleProvidersTest(req)
	case contracts.MethodProvidersImport:
		var req contracts.ProviderIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleProvidersImport(req)
	case contracts.MethodModelsList:
		return a.handleModelsList()
	case contracts.MethodModelsEndpoints:
		var req contracts.ModelEndpointsRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleModelEndpoints(req)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown ai method: " + method}
	}
}
