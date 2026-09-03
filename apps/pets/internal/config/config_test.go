package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		json    string
		wantErr string
		want    *Config
	}{
		{
			name: "full config",
			json: `{
				"name": "pet",
				"max_width": 220,
				"max_height": 440,
				"ws_url": "ws://host:1/ws/pet",
				"electron_path": "/usr/bin/electron",
				"image": "robot.png",
				"click_through": true,
				"shape_alpha_cutoff": 12,
				"states": {
					"idle": {"gif": "idle.gif", "every": [25, 60]},
					"error": {"gif": "error.gif", "every": [0, 0]}
				}
			}`,
			want: &Config{
				Name: "pet", MaxWidth: 220, MaxHeight: 440,
				WSURL: "ws://host:1/ws/pet", ElectronPath: "/usr/bin/electron",
				Image: "robot.png", ClickThrough: true, ShapeAlphaCutoff: 12,
				States: map[string]StateConfig{
					"idle":  {GIF: "idle.gif", Every: [2]float64{25, 60}},
					"error": {GIF: "error.gif", Every: [2]float64{0, 0}},
				},
			},
		},
		{
			name: "defaults applied",
			json: `{"states": {"idle": {"gif": "idle.gif"}}}`,
			want: &Config{
				Name: "nusa-shell-pet", MaxWidth: DefaultMaxWidth, MaxHeight: DefaultMaxHeight,
				WSURL: DefaultWSURL, ShapeAlphaCutoff: DefaultShapeAlphaCutoff,
				States: map[string]StateConfig{"idle": {GIF: "idle.gif"}},
			},
		},
		{
			name: "static image without states",
			json: `{"image": "robot.png"}`,
			want: &Config{
				Name: "nusa-shell-pet", MaxWidth: DefaultMaxWidth, MaxHeight: DefaultMaxHeight,
				WSURL: DefaultWSURL, Image: "robot.png", ShapeAlphaCutoff: DefaultShapeAlphaCutoff,
			},
		},
		{
			name: "hatch pet spritesheet",
			json: `{"spritesheet": "spritesheet.webp"}`,
			want: &Config{
				Name: "nusa-shell-pet", MaxWidth: DefaultMaxWidth, MaxHeight: DefaultMaxHeight,
				WSURL: DefaultWSURL, SpriteSheet: "spritesheet.webp", ShapeAlphaCutoff: DefaultShapeAlphaCutoff,
			},
		},
		{name: "empty assets", json: `{}`, wantErr: "spritesheet, image, or states required"},
		{name: "missing gif", json: `{"states": {"idle": {}}}`, wantErr: "gif is empty"},
		{name: "inverted every", json: `{"states": {"idle": {"gif": "i.gif", "every": [10, 5]}}}`, wantErr: "invalid every"},
		{name: "negative every", json: `{"states": {"idle": {"gif": "i.gif", "every": [-1, 5]}}}`, wantErr: "invalid every"},
		{name: "bad json", json: `{not json`, wantErr: "unmarshal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.json))
			switch {
			case tc.wantErr != "":
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
				}
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			default:
				if got.Name != tc.want.Name || got.MaxWidth != tc.want.MaxWidth ||
					got.MaxHeight != tc.want.MaxHeight || got.WSURL != tc.want.WSURL ||
					got.ElectronPath != tc.want.ElectronPath || got.SpriteSheet != tc.want.SpriteSheet || got.Image != tc.want.Image ||
					got.ClickThrough != tc.want.ClickThrough || got.ShapeAlphaCutoff != tc.want.ShapeAlphaCutoff {
					t.Fatalf("got %+v, want %+v", got, tc.want)
				}
				if len(got.States) != len(tc.want.States) {
					t.Fatalf("states: got %d, want %d", len(got.States), len(tc.want.States))
				}
				for k, w := range tc.want.States {
					g := got.States[k]
					if g.GIF != w.GIF || g.Every != w.Every {
						t.Fatalf("state %s: got %+v, want %+v", k, g, w)
					}
				}
			}
		})
	}
}

func TestBubbleDefaultsAndOverrides(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"spritesheet": "s.webp"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Bubbles() {
		t.Fatal("bubble should default to enabled")
	}
	cfg, err = Parse([]byte(`{"spritesheet": "s.webp", "bubble_enabled": false, "bubble_font": "x.ttf"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bubbles() {
		t.Fatal("bubble_enabled=false must disable")
	}
	if cfg.BubbleFont != "x.ttf" {
		t.Fatalf("bubble_font = %q, want x.ttf", cfg.BubbleFont)
	}
}

func TestLoadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"states": {"idle": {"gif": "idle.gif"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.States["idle"].GIF != "idle.gif" {
		t.Fatalf("got %v, want idle.gif", cfg.States["idle"].GIF)
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()
	if _, err := Load("/no/such/config.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}
