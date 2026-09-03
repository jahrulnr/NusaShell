// Package char loads a CharPack — the atlas or legacy animated GIF assets and
// config that drive the desktop pet. The hatch-pet v2 path decodes one fixed
// 8x11 WebP atlas; the older folder/.zip GIF loader remains available for
// explicit compatibility use.
//
// This package is pure Go (image/gif + golang.org/x/image/draw) so it is fully
// unit-testable without SDL2. The SDL2 renderer converts the decoded frames
// ([]image.Image) into textures.
package char

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/gif"
	_ "image/png"
	"io"
	"math"
	"path"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// Frame is one decoded legacy GIF frame with its display delay in milliseconds.
type Frame struct {
	Image image.Image
	Delay int // milliseconds
}

// Anim is a playable animation: frames, per-frame delays (ms), and a random
// replay gap Every [minSeconds, maxSeconds]. [0,0] means play once and hold.
type Anim struct {
	Frames []image.Image
	Delays []int // ms per frame
	Every  [2]float64
	Loop   bool // when false, hold the last frame until the caller changes state
}

// CharPack holds the decoded state animations for a pet.
type CharPack struct {
	Name      string
	MaxWidth  int
	MaxHeight int
	Scale     float64
	States    map[string]*Anim
	// FrameWidth and FrameHeight are the fixed display canvas dimensions. Atlas
	// cells share these dimensions even when their visible silhouettes differ.
	FrameWidth  int
	FrameHeight int
	LookFrames  []image.Image // 16 clockwise hatch-pet v2 direction cells
}

// packConfig is the on-disk config.json shape (a subset of myCat's, focused on
// the state -> GIF map used by the NusaShell pet overlay).
type packConfig struct {
	Name      string                     `json:"name"`
	MaxWidth  int                        `json:"max_width"`
	MaxHeight int                        `json:"max_height"`
	States    map[string]packStateConfig `json:"states"`
}

type packStateConfig struct {
	GIF   string     `json:"gif"`
	Every [2]float64 `json:"every"`
}

const (
	defaultMaxWidth  = 200
	defaultMaxHeight = 400
)

// DecodeGIF decodes an animated GIF into frames (RGBA) with millisecond delays.
// A single-frame GIF yields one frame. Delays default to 100ms when the GIF
// omits them.
func DecodeGIF(data []byte) ([]image.Image, []int, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("decode gif: %w", err)
	}
	n := len(g.Image)
	if n == 0 {
		return nil, nil, fmt.Errorf("decode gif: no frames")
	}
	frames := make([]image.Image, 0, n)
	delays := make([]int, 0, n)
	for i, src := range g.Image {
		bounds := src.Bounds()
		rgba := image.NewRGBA(bounds)
		// Composite onto transparent RGBA so disposal is handled simply.
		drawCopy(rgba, bounds.Min, src, bounds)
		frames = append(frames, rgba)
		d := 100
		if i < len(g.Delay) {
			d = g.Delay[i] // hundredths of a second
			if d <= 0 {
				d = 100
			}
		}
		delays = append(delays, d*10) // -> ms
	}
	return frames, delays, nil
}

// drawCopy copies src into dst at dstMin. Standard image/draw.Src would drop
// alpha; we use a manual copy to preserve per-frame transparency for the
// paletted GIF frames.
func drawCopy(dst *image.RGBA, dstMin image.Point, src image.Image, srcBounds image.Rectangle) {
	for y := srcBounds.Min.Y; y < srcBounds.Max.Y; y++ {
		for x := srcBounds.Min.X; x < srcBounds.Max.X; x++ {
			dst.Set(dstMin.X+(x-srcBounds.Min.X), dstMin.Y+(y-srcBounds.Min.Y), src.At(x, y))
		}
	}
}

// fitScale returns the shrink-to-fit scale for nativeW x nativeH within
// maxW x maxH. It never enlarges (caps at 1.0).
func fitScale(nativeW, nativeH, maxW, maxH int) float64 {
	if nativeW <= 0 || nativeH <= 0 {
		return 1.0
	}
	s := math.Min(float64(maxW)/float64(nativeW), float64(maxH)/float64(nativeH))
	if s > 1.0 {
		s = 1.0
	}
	return s
}

// scaleImage returns a new image scaled by s using bilinear interpolation.
func scaleImage(src image.Image, s float64) image.Image {
	if s == 1.0 {
		return src
	}
	b := src.Bounds()
	w := max(1, int(math.Round(float64(b.Dx())*s)))
	h := max(1, int(math.Round(float64(b.Dy())*s)))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	return dst
}

// LoadPack reads a char (folder or .zip) and decodes its state GIFs, scaled
// shrink-to-fit to the config's max box. Only states whose GIF file is present
// are decoded; missing files are silently skipped (the state is absent from the
// returned pack).
func LoadPack(srcPath string) (*CharPack, error) {
	src, err := OpenSource(srcPath)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return loadChar(src.Has, src.Read, srcPath)
}

// LoadPackFromReader loads a char from an in-memory zip (no filesystem needed).
func LoadPackFromReader(r io.ReaderAt, size int64) (*CharPack, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("char: open zip: %w", err)
	}
	names := make(map[string]bool, len(zr.File))
	for _, f := range zr.File {
		names[f.Name] = true
	}
	has := func(name string) bool { return names[name] }
	read := func(name string) ([]byte, error) {
		if !names[name] {
			return nil, fmt.Errorf("read %s: not found", name)
		}
		rc, err := zr.Open(name)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return loadChar(has, read, "(zip)")
}

// loadChar is the shared loader: has reports file presence, read returns bytes.
func loadChar(has func(string) bool, read func(string) ([]byte, error), label string) (*CharPack, error) {
	if !has("config.json") {
		return nil, fmt.Errorf("char: %s missing config.json", label)
	}
	cfgData, err := read("config.json")
	if err != nil {
		return nil, err
	}
	var pc packConfig
	if err := json.Unmarshal(cfgData, &pc); err != nil {
		return nil, fmt.Errorf("char: parse config.json: %w", err)
	}
	if pc.Name == "" {
		base := strings.TrimSuffix(path.Base(label), ".zip")
		if base == "" || base == "." {
			base = "pet"
		}
		pc.Name = base
	}
	if pc.MaxWidth <= 0 {
		pc.MaxWidth = defaultMaxWidth
	}
	if pc.MaxHeight <= 0 {
		pc.MaxHeight = defaultMaxHeight
	}

	// Determine the scale from the first available state GIF's native size, so
	// every state shares one scale (matches myCat fitting the still's height).
	scale := 1.0
	for _, st := range pc.States {
		if st.GIF == "" || !has(st.GIF) {
			continue
		}
		data, derr := read(st.GIF)
		if derr != nil {
			continue
		}
		if w, h, derr := gifDimensions(data); derr == nil && w > 0 && h > 0 {
			scale = fitScale(w, h, pc.MaxWidth, pc.MaxHeight)
		}
		break
	}

	states := make(map[string]*Anim, len(pc.States))
	for name, st := range pc.States {
		if st.GIF == "" || !has(st.GIF) {
			continue
		}
		data, derr := read(st.GIF)
		if derr != nil {
			return nil, fmt.Errorf("char: read %s: %w", st.GIF, derr)
		}
		frames, delays, derr := DecodeGIF(data)
		if derr != nil {
			return nil, fmt.Errorf("char: state %s: %w", name, derr)
		}
		if scale != 1.0 {
			scaled := make([]image.Image, len(frames))
			for i, f := range frames {
				scaled[i] = scaleImage(f, scale)
			}
			frames = scaled
		}
		states[name] = &Anim{Frames: frames, Delays: delays, Every: st.Every, Loop: true}
	}

	return &CharPack{
		Name:      pc.Name,
		MaxWidth:  pc.MaxWidth,
		MaxHeight: pc.MaxHeight,
		Scale:     scale,
		States:    states,
	}, nil
}

// gifDimensions returns the GIF's first-frame canvas size.
func gifDimensions(data []byte) (int, int, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	if len(g.Image) == 0 {
		return 0, 0, fmt.Errorf("no frames")
	}
	b := g.Image[0].Bounds()
	return b.Dx(), b.Dy(), nil
}
