package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchAI routes ai.* RPC methods (providers, models, codex) to their
// handlers. Called by App.Dispatch for any method whose first segment is
// "ai". Codex methods are handled here before forwarding the rest to the
// provider feature service.
func (a *App) dispatchAI(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodCodexLogin:
		var req contracts.CodexLoginRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexLogin(req)
	case contracts.MethodCodexImport:
		var req contracts.CodexImportRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexImport(req)
	case contracts.MethodCodexLogout:
		var req contracts.CodexLogoutRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexLogout(req)
	case contracts.MethodCodexAccountsList:
		var req contracts.CodexAccountsListRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexAccountsList(req)
	case contracts.MethodCodexAccountsSwitch:
		var req contracts.CodexAccountsSwitchRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexAccountsSwitch(req)
	case contracts.MethodCodexRefreshCircuits:
		return a.handleCodexRefreshCircuits()
	case contracts.MethodCodexRuntimeStatus:
		return a.handleCodexRuntimeStatus()
	case contracts.MethodCodexRuntimeDownload:
		var req contracts.CodexRuntimeDownloadRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexRuntimeDownload(req)
	case contracts.MethodCodexUsage:
		var req contracts.CodexUsageRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexUsage(req)
	default:
		return a.providerService().Dispatch(method, payload)
	}
}
