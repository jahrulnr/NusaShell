package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"
)

// ---- fixtures ----

// contractTestPlugin builds an in-memory plugin whose install dir contains
// an optional contract file. No manifest.json is needed: the toolbox reads
// the contract straight from the declared entry under InstallPath.
func contractTestPlugin(t *testing.T, id, entry, body string) *domain.Plugin {
	t.Helper()
	dir := t.TempDir()
	if entry != "" && body != "" {
		if err := os.WriteFile(filepath.Join(dir, entry), []byte(body), 0o644); err != nil {
			t.Fatalf("write contract: %v", err)
		}
	}
	var cfg *domain.PluginContractConfig
	if entry != "" {
		cfg = &domain.PluginContractConfig{Entry: entry}
	}
	return &domain.Plugin{
		InstallPath: dir,
		Manifest: domain.PluginManifest{
			ID:       id,
			Name:     id,
			MCP:      domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "srv"},
			Contract: cfg,
		},
	}
}

// stubContractSettings pins the gate mode for a test.
type stubContractSettings struct{ mode string }

func (s *stubContractSettings) Get() domain.Settings {
	ds := domain.DefaultSettings()
	ds.PluginContractMode = s.mode
	return ds
}
func (s *stubContractSettings) Set(_ domain.Settings) error { return nil }

func connectedStub(id, tool string) *stubMCP {
	serverID := "plugin:" + id
	return &stubMCP{
		connected: map[string]bool{serverID: true},
		tools:     map[string][]contracts.MCPToolDTO{serverID: {{Name: tool}}},
	}
}

func callRef(tb *Toolbox, convID string, ref string) (string, error) {
	ctx := context.Background()
	if convID != "" {
		ctx = application.WithConversationID(ctx, convID)
	}
	return tb.Execute(ctx, "mcp_call", []byte(`{"ref":"`+ref+`","arguments_json":"{}"}`))
}

// ---- contract_read ----

func TestContractReadReturnsBodyAndMarksRead(t *testing.T) {
	p := contractTestPlugin(t, "p1", "CONTRACT.md", "# rules\nbe careful")
	tb := testToolbox(nil, []*domain.Plugin{p}, connectedStub("p1", "ping"))
	tb.Settings = &stubContractSettings{mode: domain.PluginContractRequire}

	out, err := tb.Execute(application.WithConversationID(context.Background(), "c1"), "contract_read", []byte(`{"id":"p1"}`))
	if err != nil {
		t.Fatalf("contract_read: %v", err)
	}
	if !strings.Contains(out, "be careful") {
		t.Fatalf("contract body missing:\n%s", out)
	}
	if !strings.Contains(out, "ids:") || !strings.Contains(out, "p1") {
		t.Fatalf("meta ids missing:\n%s", out)
	}
	// The read must unlock mcp_call under require.
	if _, err := callRef(tb, "c1", "p1:ping"); err != nil {
		t.Fatalf("mcp_call after contract_read: %v", err)
	}
}

func TestContractReadIsPerConversation(t *testing.T) {
	p := contractTestPlugin(t, "p1", "CONTRACT.md", "rules")
	tb := testToolbox(nil, []*domain.Plugin{p}, connectedStub("p1", "ping"))
	tb.Settings = &stubContractSettings{mode: domain.PluginContractRequire}

	if _, err := tb.Execute(application.WithConversationID(context.Background(), "c1"), "contract_read", []byte(`{"id":"p1"}`)); err != nil {
		t.Fatalf("contract_read: %v", err)
	}
	_, err := callRef(tb, "c2", "p1:ping")
	if err == nil || !strings.Contains(err.Error(), "CONTRACT_REQUIRED") {
		t.Fatalf("other conversation must stay blocked, got: %v", err)
	}
}

func TestContractReadAllMarksEveryContractPlugin(t *testing.T) {
	p1 := contractTestPlugin(t, "p1", "CONTRACT.md", "rules one")
	p2 := contractTestPlugin(t, "p2", "GUIDE.md", "rules two")
	plain := contractTestPlugin(t, "p3", "", "")
	mcp := connectedStub("p1", "ping")
	tb := testToolbox(nil, []*domain.Plugin{p1, p2, plain}, mcp)
	tb.Settings = &stubContractSettings{mode: domain.PluginContractRequire}
	// register p2 as connected too
	mcp.connected["plugin:p2"] = true
	mcp.tools["plugin:p2"] = []contracts.MCPToolDTO{{Name: "pong"}}

	out, err := tb.Execute(application.WithConversationID(context.Background(), "c1"), "contract_read", []byte(`{"id":"all"}`))
	if err != nil {
		t.Fatalf("contract_read all: %v", err)
	}
	for _, want := range []string{"rules one", "rules two", "# p1", "# p2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if _, err := callRef(tb, "c1", "p1:ping"); err != nil {
		t.Fatalf("p1 unlocked: %v", err)
	}
	if _, err := callRef(tb, "c1", "p2:pong"); err != nil {
		t.Fatalf("p2 unlocked: %v", err)
	}
}

func TestContractReadErrors(t *testing.T) {
	plain := contractTestPlugin(t, "plain", "", "")
	tb := testToolbox(nil, []*domain.Plugin{plain}, connectedStub("plain", "ping"))

	if _, err := tb.Execute(context.Background(), "contract_read", []byte(`{"id":"nope"}`)); err == nil {
		t.Fatal("unknown id must error")
	}
	if _, err := tb.Execute(context.Background(), "contract_read", []byte(`{"id":"plain"}`)); err == nil {
		t.Fatal("plugin without contract must error")
	}
	if _, err := tb.Execute(context.Background(), "contract_read", []byte(`{}`)); err == nil {
		t.Fatal("missing id must error")
	}
}

func TestContractReadMissingFileErrors(t *testing.T) {
	p := contractTestPlugin(t, "ghost", "CONTRACT.md", "") // declared but never written
	tb := testToolbox(nil, []*domain.Plugin{p}, connectedStub("ghost", "ping"))
	if _, err := tb.Execute(context.Background(), "contract_read", []byte(`{"id":"ghost"}`)); err == nil {
		t.Fatal("missing contract file must error")
	}
}

func TestContractReadTruncatesWithMarker(t *testing.T) {
	big := strings.Repeat("rule line\n", 2000) // ~18k chars > cap
	p := contractTestPlugin(t, "big", "CONTRACT.md", big)
	tb := testToolbox(nil, []*domain.Plugin{p}, connectedStub("big", "ping"))
	out, err := tb.Execute(context.Background(), "contract_read", []byte(`{"id":"big"}`))
	if err != nil {
		t.Fatalf("contract_read: %v", err)
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("truncation marker missing:\n%s", out)
	}
	if len(out) > 12000 {
		t.Fatalf("output not bounded: %d chars", len(out))
	}
}

// ---- mcp_list flag ----

func TestMcpListFlagsContractPlugins(t *testing.T) {
	p1 := contractTestPlugin(t, "with", "CONTRACT.md", "r")
	p2 := contractTestPlugin(t, "without", "", "")
	tb := testToolbox(nil, []*domain.Plugin{p1, p2}, &stubMCP{})
	out, err := tb.Execute(context.Background(), "mcp_list", []byte(`{}`))
	if err != nil {
		t.Fatalf("mcp_list: %v", err)
	}
	if !strings.Contains(out, `"contract":true`) {
		t.Fatalf("contract flag missing:\n%s", out)
	}
	if strings.Contains(out, "contract\":false") {
		t.Fatalf("contract-less plugins must omit the key:\n%s", out)
	}
}

// ---- gate modes ----

func TestGateRequireBlocksBeforeRead(t *testing.T) {
	p := contractTestPlugin(t, "p1", "CONTRACT.md", "rules")
	tb := testToolbox(nil, []*domain.Plugin{p}, connectedStub("p1", "ping"))
	tb.Settings = &stubContractSettings{mode: domain.PluginContractRequire} // explicit

	_, err := callRef(tb, "c1", "p1:ping")
	if err == nil || !strings.Contains(err.Error(), "CONTRACT_REQUIRED") {
		t.Fatalf("want CONTRACT_REQUIRED, got: %v", err)
	}
	if !strings.Contains(err.Error(), "contract_read") {
		t.Fatalf("error must point at contract_read: %v", err)
	}
}

func TestGateHintAppendsNoticeUntilRead(t *testing.T) {
	p := contractTestPlugin(t, "p1", "CONTRACT.md", "rules")
	tb := testToolbox(nil, []*domain.Plugin{p}, connectedStub("p1", "ping"))
	tb.Settings = &stubContractSettings{mode: domain.PluginContractHint}

	out, err := callRef(tb, "c1", "p1:ping")
	if err != nil {
		t.Fatalf("hint mode must not block: %v", err)
	}
	if !strings.Contains(out, "[contract notice]") || !strings.Contains(out, "ok") {
		t.Fatalf("notice must ride along the result:\n%s", out)
	}
	if _, err := tb.Execute(application.WithConversationID(context.Background(), "c1"), "contract_read", []byte(`{"id":"p1"}`)); err != nil {
		t.Fatalf("contract_read: %v", err)
	}
	out, err = callRef(tb, "c1", "p1:ping")
	if err != nil {
		t.Fatalf("post-read call: %v", err)
	}
	if strings.Contains(out, "[contract notice]") {
		t.Fatalf("notice must disappear after read:\n%s", out)
	}
}

func TestGateOffStaysSilent(t *testing.T) {
	p := contractTestPlugin(t, "p1", "CONTRACT.md", "rules")
	tb := testToolbox(nil, []*domain.Plugin{p}, connectedStub("p1", "ping"))
	tb.Settings = &stubContractSettings{mode: domain.PluginContractOff}
	out, err := callRef(tb, "c1", "p1:ping")
	if err != nil {
		t.Fatalf("off must not block: %v", err)
	}
	if strings.Contains(out, "[contract notice]") {
		t.Fatalf("off must not notice:\n%s", out)
	}
}

func TestGateIgnoresContractlessPluginsEvenInRequire(t *testing.T) {
	p := contractTestPlugin(t, "plain", "", "")
	tb := testToolbox(nil, []*domain.Plugin{p}, connectedStub("plain", "ping"))
	tb.Settings = &stubContractSettings{mode: domain.PluginContractRequire}
	if _, err := callRef(tb, "c1", "plain:ping"); err != nil {
		t.Fatalf("contract-less plugin must pass: %v", err)
	}
}

func TestGateAdHocContextUsesSharedBucket(t *testing.T) {
	p := contractTestPlugin(t, "p1", "CONTRACT.md", "rules")
	tb := testToolbox(nil, []*domain.Plugin{p}, connectedStub("p1", "ping"))
	tb.Settings = &stubContractSettings{mode: domain.PluginContractRequire}
	// Ad-hoc (no conversation id): blocked, then unlocked process-wide.
	if _, err := callRef(tb, "", "p1:ping"); err == nil {
		t.Fatal("ad-hoc call must be blocked initially")
	}
	if _, err := tb.Execute(context.Background(), "contract_read", []byte(`{"id":"p1"}`)); err != nil {
		t.Fatalf("contract_read: %v", err)
	}
	if _, err := callRef(tb, "", "p1:ping"); err != nil {
		t.Fatalf("ad-hoc call after read must pass: %v", err)
	}
}

func TestGateDefaultsToHintWithoutSettings(t *testing.T) {
	p := contractTestPlugin(t, "p1", "CONTRACT.md", "rules")
	tb := testToolbox(nil, []*domain.Plugin{p}, connectedStub("p1", "ping"))
	if tb.contractMode() != domain.PluginContractHint {
		t.Fatal("nil settings must default to hint")
	}
	if _, err := callRef(tb, "c1", "p1:ping"); err != nil {
		t.Fatalf("default mode must not gate: %v", err)
	}
}

func TestContractReadAdvertisedInListTools(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	found := false
	for _, ti := range tb.ListTools() {
		if ti.Name == "contract_read" {
			found = true
		}
	}
	if !found {
		t.Fatal("contract_read must be advertised")
	}
}
