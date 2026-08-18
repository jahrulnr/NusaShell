package domain

import "testing"

func TestAcpAgentTransportField(t *testing.T) {
	// stdio is the default and current behavior. remote is the future
	// cloud transport. Empty defaults to stdio for backward compatibility.
	a := AcpAgent{ID: "a1", Name: "Cursor", Command: "cursor-agent"}
	if got := a.EffectiveTransport(); got != AcpTransportStdio {
		t.Errorf("empty transport = %q, want %q", got, AcpTransportStdio)
	}
	a.Transport = AcpTransportRemote
	if got := a.EffectiveTransport(); got != AcpTransportRemote {
		t.Errorf("remote transport = %q, want %q", got, AcpTransportRemote)
	}
	// Unknown values fall back to stdio (fail-safe).
	a.Transport = "carrier-pigeon"
	if got := a.EffectiveTransport(); got != AcpTransportStdio {
		t.Errorf("unknown transport = %q, want %q", got, AcpTransportStdio)
	}
}
