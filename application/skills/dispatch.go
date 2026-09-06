package skills

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes skills.* RPC methods to their handlers.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "skill store not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodSkillsList:     rpcdispatch.NoPayload(s.HandleList),
		contracts.MethodSkillsRead:     rpcdispatch.DecodeReq(s.HandleRead),
		contracts.MethodSkillsSave:     rpcdispatch.DecodeReq(s.HandleSave),
		contracts.MethodSkillsDelete:   rpcdispatch.DecodeReq(s.HandleDelete),
		contracts.MethodSkillsFileRead: rpcdispatch.DecodeReq(s.HandleFileRead),
		contracts.MethodSkillsInstall:  rpcdispatch.DecodeReq(s.HandleInstall),
		contracts.MethodSkillsPromote:  rpcdispatch.DecodeReq(s.HandlePromote),
		contracts.MethodSkillsRollback: rpcdispatch.DecodeReq(s.HandleRollback),
	}, "skills")(method, payload)
}
