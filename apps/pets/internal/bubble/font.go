package bubble

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// systemFonts are tried in order when no font is configured.
var systemFonts = []string{
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
	"/usr/share/fonts/truetype/liberation2/LiberationSans-Regular.ttf",
	"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
	"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
	"/usr/share/fonts/opentype/noto/NotoSans-Regular.ttf",
	"/usr/share/fonts/TTF/DejaVuSans.ttf",
}

// FindFont returns the first existing font path. A non-empty configured path
// wins; otherwise the candidates (defaulting to systemFonts) are probed.
func FindFont(configured string, candidates ...[]string) string {
	if configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured
		}
		return ""
	}
	list := systemFonts
	if len(candidates) > 0 {
		list = candidates[0]
	}
	for _, p := range list {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ttfFace adapts an opentype face to the bubble Face interface.
type ttfFace struct {
	face font.Face
}

const (
	HeaderSize = 14
	BodySize   = 12
	fontScale  = 3
)

// LoadFonts uses a real bold face for the title. A sibling Bold TTF wins;
// otherwise the existing system font search supplies a bold family.
func LoadFonts(path string) (Fonts, error) {
	stem := strings.TrimSuffix(path, filepath.Ext(path))
	stem = strings.TrimSuffix(stem, "-Regular")
	candidates := []string{stem + "-Bold.ttf"}
	for _, p := range systemFonts {
		if !strings.Contains(p, "-Bold") {
			candidates = append(candidates, strings.TrimSuffix(strings.TrimSuffix(p, ".ttf"), "-Regular")+"-Bold.ttf")
		}
	}
	header, err := LoadFace(FindFont("", candidates), HeaderSize)
	if err != nil {
		return Fonts{}, err
	}
	body, err := LoadFace(path, BodySize)
	if err != nil {
		return Fonts{}, err
	}
	return Fonts{Header: header, Body: body}, nil
}

// LoadFace loads a TrueType font file at the given pixel size.
func LoadFace(path string, size int) (Face, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bubble: read font %s: %w", path, err)
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("bubble: parse font %s: %w", path, err)
	}
	oc := &opentype.FaceOptions{Size: float64(size * fontScale), DPI: 72, Hinting: font.HintingNone}
	ff, err := opentype.NewFace(f, oc)
	if err != nil {
		return nil, fmt.Errorf("bubble: create face %s: %w", path, err)
	}
	return ttfFace{face: ff}, nil
}

// Measure returns the advance width and line height in pixels.
func (f ttfFace) Measure(text string) (int, int) {
	m := f.face.Metrics()
	h := (m.Height.Ceil() + fontScale - 1) / fontScale
	w := (font.MeasureString(f.face, text).Ceil() + fontScale - 1) / fontScale
	return w, h
}

// Draw paints text with top-left at (x, y), baseline offset by the ascent.
func (f ttfFace) Draw(dst *image.RGBA, x, y int, text string, c color.Color) {
	w, h := f.Measure(text)
	if w == 0 || h == 0 {
		return
	}
	// Supersample glyphs separately so the 12px detail keeps smooth strokes
	// without scaling the pet artwork or changing the window size.
	hi := image.NewRGBA(image.Rect(0, 0, w*fontScale, h*fontScale))
	m := f.face.Metrics()
	d := &font.Drawer{
		Dst:  hi,
		Src:  image.NewUniform(c),
		Face: f.face,
		Dot:  fixed.P(0, m.Ascent.Ceil()),
	}
	d.DrawString(text)
	xdraw.CatmullRom.Scale(dst, image.Rect(x, y, x+w, y+h), hi, hi.Bounds(), xdraw.Over, nil)
}
