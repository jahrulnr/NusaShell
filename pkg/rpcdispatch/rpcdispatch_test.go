package rpcdispatch

import (
	"encoding/json"
	"errors"
	"testing"

	"nusashell/contracts"
)

func TestNoPayloadIgnoresBody(t *testing.T) {
	h := NoPayload(func() (any, *contracts.RPCError) { return "ok", nil })
	got, err := h(json.RawMessage(`{"ignored":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Fatalf("got %#v", got)
	}
}

func TestDecodeReqRejectsMalformedJSON(t *testing.T) {
	h := DecodeReq(func(req struct {
		Name string `json:"name"`
	}) (any, *contracts.RPCError) {
		return req.Name, nil
	})
	_, rpcErr := h(json.RawMessage(`not-json`))
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("got %v, want validation error", rpcErr)
	}
}

func TestTableUnknownMethod(t *testing.T) {
	d := Table(map[string]Handler{
		"plugin.list": NoPayload(func() (any, *contracts.RPCError) { return 1, nil }),
	}, "plugin")
	_, rpcErr := d("plugin.save", nil)
	if rpcErr == nil || rpcErr.Message != "unknown plugin method: plugin.save" {
		t.Fatalf("got %v", rpcErr)
	}
}

func TestInternal(t *testing.T) {
	rpcErr := Internal(errors.New("boom"))
	if rpcErr.Code != contracts.CodeInternal || rpcErr.Message != "boom" {
		t.Fatalf("got %#v", rpcErr)
	}
}
