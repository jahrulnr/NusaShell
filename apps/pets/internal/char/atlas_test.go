package char

import (
	"image"
	"image/color"
	"testing"
)

func TestDecodeAtlasExtractsConfiguredRowsAndLookDirections(t *testing.T) {
	t.Parallel()
	layout := AtlasLayout{
		CellWidth:  4,
		CellHeight: 3,
		Columns:    3,
		Rows:       4,
		StateRows: map[string]AtlasRow{
			"idle":     {Row: 0, Frames: 2, Delays: []int{100, 200}, Loop: true},
			"thinking": {Row: 1, Frames: 1, Delays: []int{150}, Loop: true},
		},
		LookRows: [2]int{2, 3},
	}

	src := image.NewRGBA(image.Rect(0, 0, 12, 12))
	for row := 0; row < layout.Rows; row++ {
		for col := 0; col < layout.Columns; col++ {
			want := color.RGBA{uint8(row + 1), uint8(col + 1), 0, 255}
			for y := 0; y < layout.CellHeight; y++ {
				for x := 0; x < layout.CellWidth; x++ {
					src.Set(col*layout.CellWidth+x, row*layout.CellHeight+y, want)
				}
			}
		}
	}

	pack, err := DecodeAtlas(src, "test", 12, 12, layout)
	if err != nil {
		t.Fatalf("DecodeAtlas: %v", err)
	}
	if got := len(pack.States["idle"].Frames); got != 2 {
		t.Fatalf("idle frames = %d, want 2", got)
	}
	if got := pack.States["idle"].Delays; len(got) != 2 || got[0] != 100 || got[1] != 200 {
		t.Fatalf("idle delays = %v, want [100 200]", got)
	}
	if got := len(pack.LookFrames); got != 6 {
		t.Fatalf("look frames = %d, want 6", got)
	}
	if got := pack.FrameWidth; got != 4 {
		t.Fatalf("frame width = %d, want 4", got)
	}
	if got := pack.FrameHeight; got != 3 {
		t.Fatalf("frame height = %d, want 3", got)
	}

	assertPixel := func(label string, img image.Image, want color.Color) {
		t.Helper()
		if got := img.At(0, 0); got != want {
			t.Fatalf("%s pixel = %v, want %v", label, got, want)
		}
	}
	assertPixel("idle frame 0", pack.States["idle"].Frames[0], color.RGBA{1, 1, 0, 255})
	assertPixel("idle frame 1", pack.States["idle"].Frames[1], color.RGBA{1, 2, 0, 255})
	assertPixel("look frame 0", pack.LookFrames[0], color.RGBA{3, 1, 0, 255})
	assertPixel("look frame 5", pack.LookFrames[5], color.RGBA{4, 3, 0, 255})
}

func TestDecodeAtlasRejectsInvalidGeometryAndRows(t *testing.T) {
	t.Parallel()
	base := AtlasLayout{
		CellWidth:  4,
		CellHeight: 3,
		Columns:    3,
		Rows:       4,
		StateRows:  map[string]AtlasRow{"idle": {Row: 0, Frames: 1}},
		LookRows:   [2]int{2, 3},
	}

	tests := []struct {
		name   string
		layout AtlasLayout
		src    image.Image
	}{
		{name: "empty image", layout: base, src: image.NewRGBA(image.Rect(0, 0, 0, 0))},
		{name: "atlas not divisible", layout: base, src: image.NewRGBA(image.Rect(0, 0, 11, 12))},
		{name: "state row out of range", layout: func() AtlasLayout {
			l := base
			l.StateRows = map[string]AtlasRow{"idle": {Row: 4, Frames: 1}}
			return l
		}(), src: image.NewRGBA(image.Rect(0, 0, 12, 12))},
		{name: "state frame count out of range", layout: func() AtlasLayout {
			l := base
			l.StateRows = map[string]AtlasRow{"idle": {Row: 0, Frames: 4}}
			return l
		}(), src: image.NewRGBA(image.Rect(0, 0, 12, 12))},
		{name: "look row out of range", layout: func() AtlasLayout { l := base; l.LookRows = [2]int{2, 4}; return l }(), src: image.NewRGBA(image.Rect(0, 0, 12, 12))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeAtlas(tc.src, "test", 12, 12, tc.layout); err == nil {
				t.Fatal("DecodeAtlas unexpectedly succeeded")
			}
		})
	}
}

func TestDefaultAtlasLayoutMatchesHatchPetV2(t *testing.T) {
	t.Parallel()
	layout := DefaultAtlasLayout()
	if layout.CellWidth != 192 || layout.CellHeight != 208 || layout.Columns != 8 || layout.Rows != 11 {
		t.Fatalf("geometry = %dx%d %dx%d, want 192x208 8x11", layout.CellWidth, layout.CellHeight, layout.Columns, layout.Rows)
	}
	if layout.LookRows != [2]int{9, 10} {
		t.Fatalf("look rows = %v, want [9 10]", layout.LookRows)
	}
	for _, name := range []string{"idle", "thinking", "reasoning", "done", "error", "waiting",
		StateRunningRight, StateRunningLeft} {
		if _, ok := layout.StateRows[name]; !ok {
			t.Fatalf("default state row %q is missing", name)
		}
	}
	runRight := layout.StateRows[StateRunningRight]
	if runRight.Row != 1 || runRight.Frames != layout.Columns || !runRight.Loop {
		t.Fatalf("running-right row = %+v, want row 1 / 8 frames / loop", runRight)
	}
	runLeft := layout.StateRows[StateRunningLeft]
	if runLeft.Row != 2 || runLeft.Frames != layout.Columns || !runLeft.Loop {
		t.Fatalf("running-left row = %+v, want row 2 / 8 frames / loop", runLeft)
	}
}
