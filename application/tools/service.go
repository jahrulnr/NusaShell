package tools

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Docs DocsSource
}

// Service owns docs.* RPC handlers and the tool-surface helpers that do
// not need the agent turn loop.
type Service struct {
	deps Deps
}

// New builds a tools Service from Deps.
func New(d Deps) *Service {
	return &Service{deps: d}
}

// Dispatch routes docs.* RPC methods to their handlers.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "docs store not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodDocsList:   rpcdispatch.NoPayload(s.HandleList),
		contracts.MethodDocsSearch: rpcdispatch.DecodeReq(s.HandleSearch),
		contracts.MethodDocsRead:   rpcdispatch.DecodeReq(s.HandleRead),
	}, "docs")(method, payload)
}
