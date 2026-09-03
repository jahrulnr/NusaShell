package char

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"sort"
	"strings"
)

// LoadStaticImage is the explicit legacy fallback for a single still image.
// The normal NusaShell pet path uses LoadAtlas so state animation and look
// direction cells come from the hatch-pet v2 WebP asset.
func LoadStaticImage(path, name string, maxWidth, maxHeight int) (*CharPack, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("char: static image path is empty")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("char: open static image %s: %w", path, err)
	}
	defer file.Close()

	src, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("char: decode static image %s: %w", path, err)
	}
	if src.Bounds().Dx() <= 0 || src.Bounds().Dy() <= 0 {
		return nil, fmt.Errorf("char: static image %s has empty bounds", path)
	}
	if maxWidth <= 0 {
		maxWidth = defaultMaxWidth
	}
	if maxHeight <= 0 {
		maxHeight = defaultMaxHeight
	}
	if strings.TrimSpace(name) == "" {
		name = "nusa-shell-pet"
	}

	// Normalize before scaling so image bounds with a non-zero origin cannot
	// shift the texture or the alpha mask relative to the SDL window.
	normalized := normalizeImage(src)
	scale := fitScale(normalized.Bounds().Dx(), normalized.Bounds().Dy(), maxWidth, maxHeight)
	frame := scaleImage(normalized, scale)
	states := make(map[string]*Anim, len(staticStateNames))
	for _, state := range staticStateNames {
		states[state] = &Anim{Frames: []image.Image{frame}, Delays: []int{100}, Loop: true}
	}
	return &CharPack{
		Name:       name,
		MaxWidth:   maxWidth,
		MaxHeight:  maxHeight,
		Scale:      scale,
		FrameWidth: frame.Bounds().Dx(), FrameHeight: frame.Bounds().Dy(),
		States: states,
	}, nil
}

var staticStateNames = []string{"idle", "thinking", "reasoning", "done", "error", "waiting"}

// FirstFrame returns a deterministic frame for shaping the initial window.
// Idle is preferred; otherwise state names are sorted for stable behavior.
func (p *CharPack) FirstFrame() (image.Image, bool) {
	if p == nil {
		return nil, false
	}
	if idle, ok := p.States["idle"]; ok && idle != nil && len(idle.Frames) > 0 {
		return idle.Frames[0], true
	}
	names := make([]string, 0, len(p.States))
	for name := range p.States {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		anim := p.States[name]
		if anim != nil && len(anim.Frames) > 0 {
			return anim.Frames[0], true
		}
	}
	return nil, false
}

func normalizeImage(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
