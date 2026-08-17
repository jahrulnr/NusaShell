package domain

import (
	"encoding/json"
	"testing"
)

func TestPluginManifestPreservesDescriptionForLauncherSearch(t *testing.T) {
	var manifest PluginManifest
	if err := json.Unmarshal([]byte(`{"id":"notes.app","name":"Notes","version":"1.0.0","icon":"📝","description":"Capture ideas","mcp":{"transport":"stdio","command":"node"}}`), &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if manifest.Description != "Capture ideas" {
		t.Fatalf("Description = %q, want %q", manifest.Description, "Capture ideas")
	}
}

func TestPluginManifestValidateRejectsUnsafeIDs(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want bool // true = should be accepted
	}{
		{"simple", "notes", true},
		{"dotted", "com.example.notes", true},
		{"hyphenated", "my-plugin", true},
		{"parent traversal", "..", false},
		{"embedded parent", "foo/../bar", false},
		{"absolute posix", "/etc/passwd", false},
		{"absolute windows drive", `C:\windows`, false},
		{"backslash separator", `foo\bar`, false},
		{"empty", "  ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := PluginManifest{ID: tc.id, Name: "n", Version: "1", Icon: "x", MCP: PluginMCPConfig{Transport: PluginTransportStdio, Command: "x"}}
			err := m.Validate()
			if tc.want && err != nil {
				t.Fatalf("expected ID %q to be valid, got: %v", tc.id, err)
			}
			if !tc.want && err == nil {
				t.Fatalf("expected ID %q to be rejected, but Validate returned nil", tc.id)
			}
		})
	}
}

func TestPluginManifestValidateRejectsRootedUIEntry(t *testing.T) {
	base := PluginManifest{
		ID: "notes", Name: "n", Version: "1", Icon: "x",
		MCP: PluginMCPConfig{Transport: PluginTransportStdio, Command: "x"},
	}
	ok := base
	ok.UI = &PluginUIConfig{Entry: "ui/index.html"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("relative ui.entry should be valid: %v", err)
	}
	for _, entry := range []string{"/etc/index.html", `\Windows\index.html`, `C:\plugins\index.html`} {
		bad := base
		bad.UI = &PluginUIConfig{Entry: entry}
		if err := bad.Validate(); err == nil {
			t.Fatalf("ui.entry %q must be rejected", entry)
		}
	}
}

func TestValidatePluginID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"notes", true},
		{"com.example.notes", true},
		{"my-plugin", true},
		{"", false},
		{"  ", false},
		{"..", false},
		{"foo/../bar", false},
		{"/etc/passwd", false},
		{`C:\windows`, false},
		{`foo\bar`, false},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			if got := ValidatePluginID(tc.id); got != tc.want {
				t.Fatalf("ValidatePluginID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}
