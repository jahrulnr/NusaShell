//go:build sdl2

package platform

import "testing"

func TestGuardsRejectNilHandles(t *testing.T) {
	if err := EnableAutomaticCompositing(0); err == nil {
		t.Fatal("nil display did not fail")
	}
	if FocusElectronWindow(0) {
		t.Fatal("nil display accepted focus request")
	}
	DisableAutomaticCompositing(0)
}
