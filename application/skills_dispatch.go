package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchSkills routes skills.* RPC methods to their handlers. Called by
// App.Dispatch for any method whose first segment is "skills".
func (a *App) dispatchSkills(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodSkillsList:     noPayload(a.handleSkillsList),
		contracts.MethodSkillsRead:     decodeReq(a.handleSkillsRead),
		contracts.MethodSkillsSave:     decodeReq(a.handleSkillsSave),
		contracts.MethodSkillsDelete:   decodeReq(a.handleSkillsDelete),
		contracts.MethodSkillsFileRead: decodeReq(a.handleSkillsFileRead),
		contracts.MethodSkillsInstall:  decodeReq(a.handleSkillsInstall),
		contracts.MethodSkillsPromote:  decodeReq(a.handleSkillsPromote),
		contracts.MethodSkillsRollback: decodeReq(a.handleSkillsRollback),
	}, "skills")(method, payload)
}
