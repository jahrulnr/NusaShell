package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewPairingWiresStore is the regression guard for the "pairing not
// configured" bug: pairing persistence is bound to the data directory, not
// the listen host, so every normal startup — including loopback binds — gets
// a working pairing service. A loopback-bound core may sit behind a
// remote-aware loopback reverse proxy or tunnel whose forwarded external
// clients must pair, and the Settings UI issues pairing links via the
// loopback-only pairing.challenge.create RPC.
func TestNewPairingWiresStore(t *testing.T) {
	dataDir := t.TempDir()
	svc, store, err := newPairing(dataDir)
	if err != nil {
		t.Fatalf("newPairing: %v", err)
	}
	if svc == nil {
		t.Fatal("newPairing: nil pairing service")
	}
	if store == nil {
		t.Fatal("newPairing: nil pairing store")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "pairing.db")); err != nil {
		t.Fatalf("newPairing: pairing.db not created: %v", err)
	}
	// The wired service must actually create challenges — the failure mode
	// this guards against is App.Pairing being nil and the RPC returning
	// "pairing not configured".
	res, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatalf("newPairing: CreateChallenge: %s: %s", rpcErr.Code, rpcErr.Message)
	}
	if res == nil || res.ChallengeID == "" || res.Code == "" {
		t.Fatal("newPairing: CreateChallenge returned empty result")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("newPairing: store close: %v", err)
	}
}

func TestNewPairingForSettingsDefaultsOff(t *testing.T) {
	dataDir := t.TempDir()
	svc, store, err := newPairingForSettings(dataDir, false)
	if err != nil {
		t.Fatalf("newPairingForSettings: %v", err)
	}
	if svc != nil || store != nil {
		t.Fatalf("disabled pairing = service %v/store %v, want both nil", svc, store)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "pairing.db")); !os.IsNotExist(err) {
		t.Fatalf("disabled pairing database stat error = %v, want not exist", err)
	}
}

func TestResolveListenHostForRemoteAccess(t *testing.T) {
	tests := []struct {
		name           string
		configuredHost string
		remoteAccess   bool
		want           string
	}{
		{name: "default remote access binds all interfaces", configuredHost: "", remoteAccess: true, want: "0.0.0.0"},
		{name: "default local access stays loopback", configuredHost: "", remoteAccess: false, want: "127.0.0.1"},
		{name: "explicit loopback is promoted for remote access", configuredHost: "127.0.0.1", remoteAccess: true, want: "0.0.0.0"},
		{name: "explicit LAN bind is preserved", configuredHost: "192.168.18.81", remoteAccess: true, want: "192.168.18.81"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveListenHost(tt.configuredHost, tt.remoteAccess); got != tt.want {
				t.Fatalf("resolveListenHost(%q, %v) = %q, want %q", tt.configuredHost, tt.remoteAccess, got, tt.want)
			}
		})
	}
}
