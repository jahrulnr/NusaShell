package char

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"os"
	"strings"

	"golang.org/x/image/webp"
)

// AtlasRow describes one animation row in a hatch-pet v2 atlas.
type AtlasRow struct {
	Row    int
	Frames int
	Delays []int
	Loop   bool
}

// Drag-run state names shared between the atlas layout and the renderer's
// hold-and-drag overlay. Rows 1 and 2 are the hatch-pet v2 directional run
// contract; they are not driven by backend events, only by pointer drags.
const (
	StateRunningRight = "running-right"
	StateRunningLeft  = "running-left"
)

// AtlasLayout describes the fixed grid and the rows consumed by the runtime.
// The layout is intentionally explicit even though hatch-pet v2 has a fixed
// geometry: validation then catches a mismatched or truncated asset early.
type AtlasLayout struct {
	CellWidth  int
	CellHeight int
	Columns    int
	Rows       int
	StateRows  map[string]AtlasRow
	LookRows   [2]int
}

// DefaultAtlasLayout is the NusaShell mapping for a Codex-compatible v2 pet.
// Rows 0-8 follow the hatch-pet contract; NusaShell maps its logical states to
// the closest semantic rows and reserves the look rows for idle hover gaze.
func DefaultAtlasLayout() AtlasLayout {
	return AtlasLayout{
		CellWidth:  192,
		CellHeight: 208,
		Columns:    8,
		Rows:       11,
		StateRows: map[string]AtlasRow{
			"idle": {
				Row: 0, Frames: 6,
				Delays: []int{280, 110, 110, 140, 140, 320}, Loop: true,
			},
			StateRunningRight: {
				Row: 1, Frames: 8,
				Delays: []int{100, 100, 100, 100, 100, 100, 100, 160}, Loop: true,
			},
			StateRunningLeft: {
				Row: 2, Frames: 8,
				Delays: []int{100, 100, 100, 100, 100, 100, 100, 160}, Loop: true,
			},
			"thinking": {
				Row: 7, Frames: 6,
				Delays: []int{120, 120, 120, 120, 120, 220}, Loop: true,
			},
			"reasoning": {
				Row: 8, Frames: 6,
				Delays: []int{150, 150, 150, 150, 150, 280}, Loop: true,
			},
			"done": {
				Row: 3, Frames: 4,
				Delays: []int{140, 140, 140, 280}, Loop: false,
			},
			"error": {
				Row: 5, Frames: 8,
				Delays: []int{140, 140, 140, 140, 140, 140, 140, 240}, Loop: false,
			},
			"waiting": {
				Row: 6, Frames: 6,
				Delays: []int{150, 150, 150, 150, 150, 260}, Loop: true,
			},
		},
		LookRows: [2]int{9, 10},
	}
}

// LoadAtlas decodes a hatch-pet v2 WebP atlas and extracts the configured
// animation cells. WebP is the storage format; the runtime keeps each cell as
// an RGBA image so SDL can upload and shape it independently.
func LoadAtlas(path, name string, maxWidth, maxHeight int, layout AtlasLayout) (*CharPack, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("char: atlas path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("char: read atlas %s: %w", path, err)
	}
	src, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("char: decode WebP atlas %s: %w", path, err)
	}
	return DecodeAtlas(src, name, maxWidth, maxHeight, layout)
}

// DecodeAtlas extracts an atlas from an already decoded image. Keeping this
// function independent from WebP makes geometry and frame-order tests small
// and lets callers use a different decoder without changing the pack model.
func DecodeAtlas(src image.Image, name string, maxWidth, maxHeight int, layout AtlasLayout) (*CharPack, error) {
	if err := layout.validate(); err != nil {
		return nil, err
	}
	if src == nil || src.Bounds().Dx() <= 0 || src.Bounds().Dy() <= 0 {
		return nil, fmt.Errorf("char: atlas image is empty")
	}
	wantW := layout.CellWidth * layout.Columns
	wantH := layout.CellHeight * layout.Rows
	if src.Bounds().Dx() != wantW || src.Bounds().Dy() != wantH {
		return nil, fmt.Errorf("char: atlas geometry is %dx%d, want %dx%d", src.Bounds().Dx(), src.Bounds().Dy(), wantW, wantH)
	}
	if maxWidth <= 0 {
		maxWidth = wantW
	}
	if maxHeight <= 0 {
		maxHeight = wantH
	}
	if strings.TrimSpace(name) == "" {
		name = "nusa-shell-pet"
	}

	scale := fitScale(layout.CellWidth, layout.CellHeight, maxWidth, maxHeight)
	pack := &CharPack{
		Name:        name,
		MaxWidth:    maxWidth,
		MaxHeight:   maxHeight,
		Scale:       scale,
		FrameWidth:  scaledDimension(layout.CellWidth, scale),
		FrameHeight: scaledDimension(layout.CellHeight, scale),
		States:      make(map[string]*Anim, len(layout.StateRows)),
	}
	for name, row := range layout.StateRows {
		anim := &Anim{
			Frames: make([]image.Image, 0, row.Frames),
			Delays: append([]int(nil), row.Delays...),
			Loop:   row.Loop,
		}
		for col := 0; col < row.Frames; col++ {
			frame := atlasCell(src, layout, row.Row, col)
			if scale != 1.0 {
				frame = scaleImage(frame, scale)
			}
			anim.Frames = append(anim.Frames, frame)
		}
		pack.States[name] = anim
	}
	for _, row := range layout.LookRows {
		for col := 0; col < layout.Columns; col++ {
			frame := atlasCell(src, layout, row, col)
			if scale != 1.0 {
				frame = scaleImage(frame, scale)
			}
			pack.LookFrames = append(pack.LookFrames, frame)
		}
	}
	return pack, nil
}

func (l AtlasLayout) validate() error {
	if l.CellWidth <= 0 || l.CellHeight <= 0 || l.Columns <= 0 || l.Rows <= 0 {
		return fmt.Errorf("char: atlas layout has invalid geometry %dx%d %dx%d", l.CellWidth, l.CellHeight, l.Columns, l.Rows)
	}
	if len(l.StateRows) == 0 {
		return fmt.Errorf("char: atlas has no state rows")
	}
	for name, row := range l.StateRows {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("char: atlas state name is empty")
		}
		if row.Row < 0 || row.Row >= l.Rows {
			return fmt.Errorf("char: atlas state %s row %d is outside 0..%d", name, row.Row, l.Rows-1)
		}
		if row.Frames <= 0 || row.Frames > l.Columns {
			return fmt.Errorf("char: atlas state %s has %d frames, want 1..%d", name, row.Frames, l.Columns)
		}
		if len(row.Delays) != row.Frames {
			return fmt.Errorf("char: atlas state %s has %d delays for %d frames", name, len(row.Delays), row.Frames)
		}
		for i, delay := range row.Delays {
			if delay <= 0 {
				return fmt.Errorf("char: atlas state %s delay %d is %d, want positive", name, i, delay)
			}
		}
	}
	for i, row := range l.LookRows {
		if row < 0 || row >= l.Rows {
			return fmt.Errorf("char: atlas look row %d (%d) is outside 0..%d", i, row, l.Rows-1)
		}
	}
	return nil
}

func atlasCell(src image.Image, layout AtlasLayout, row, col int) image.Image {
	minX := src.Bounds().Min.X + col*layout.CellWidth
	minY := src.Bounds().Min.Y + row*layout.CellHeight
	dst := image.NewRGBA(image.Rect(0, 0, layout.CellWidth, layout.CellHeight))
	for y := 0; y < layout.CellHeight; y++ {
		for x := 0; x < layout.CellWidth; x++ {
			dst.Set(x, y, src.At(minX+x, minY+y))
		}
	}
	return dst
}

func scaledDimension(value int, scale float64) int {
	return max(1, int(math.Round(float64(value)*scale)))
}
