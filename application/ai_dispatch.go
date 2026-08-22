package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchAI routes ai.* RPC methods (providers, models, codex) to their
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
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown ai method: " + method}
	}
}
