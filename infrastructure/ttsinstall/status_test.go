package ttsinstall

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Status.ready uses the same voice listing the piper engine uses: any
// *.onnx under the voices directory. The catalog lists id_ID + en_US, but
// a manually-placed third language (or any user-managed voice) must also
// make speech available.
func TestStatusReadyThroughAnyVoiceFile(t *testing.T) {
	dataDir := t.TempDir()
	withFakeBin(t, dataDir)
	inst := New(dataDir, fakeBinaryServer(t, dataDir).URL)

	// Fresh data dir: nothing installed, not ready.
	if st := inst.status(); st.Ready {
		t.Error("fresh data dir must not report ready")
	}

	// Drop a voice outside the catalog, as a manual install would.
	dir := inst.voicesDirectory()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(dir, "pl_PL-example.onnx")
	if err := os.WriteFile(extra, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := inst.status()
	if !st.Ready {
		t.Error("status.ready must be true when any voice file is on disk")
	}
	if st.VoicesOnDisk != 1 {
		t.Errorf("VoicesOnDisk = %d, want 1", st.VoicesOnDisk)
	}
	if got := inst.Status().Ready; !got {
		t.Error("adapter must surface ready=true too")
	}
}

// The install flow leaves a fully-resolvable engine + at least one catalog
// voice behind by the time install() returns.
func TestInstallLeavesReadyState(t *testing.T) {
	dataDir := t.TempDir()
	withFakeBin(t, dataDir)
	inst := New(dataDir, fakeBinaryServer(t, dataDir).URL)
	if err := inst.install(context.Background(), "id_ID-news_tts-medium", nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	st := inst.status()
	if !st.BinaryInstalled || !st.Ready {
		t.Errorf("after install: binary_installed=%v ready=%v voices_on_disk=%d", st.BinaryInstalled, st.Ready, st.VoicesOnDisk)
	}
	if st.Voices[0].Installed {
		t.Logf("voice %s installed", st.Voices[0].ID)
	} else {
		t.Error("catalog voice not marked installed after install()")
	}
}
