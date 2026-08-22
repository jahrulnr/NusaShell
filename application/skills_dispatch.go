package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchSkills routes skills.* RPC methods to their handlers. Called by
// App.Dispatch for any method whose first segment is "skills".
func (a *App) dispatchSkills(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodSkillsList:
		return a.handleSkillsList()
	case contracts.MethodSkillsRead:
		var req contracts.SkillIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSkillsRead(req)
	case contracts.MethodSkillsSave:
		var req contracts.SkillSaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSkillsSave(req)
	case contracts.MethodSkillsDelete:
		var req contracts.SkillIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSkillsDelete(req)
	case contracts.MethodSkillsFileRead:
		var req contracts.SkillFileReadRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSkillsFileRead(req)
	case contracts.MethodSkillsInstall:
		var req contracts.SkillInstallRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSkillsInstall(req)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown skills method: " + method}
	}
}
