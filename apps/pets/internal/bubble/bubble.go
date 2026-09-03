// Package bubble lays out and paints the two-line activity bubble above the
// pet. Geometry, timing, and painting are pure Go; SDL only presents the result.
package bubble

import (
	"image"
	"image/color"
	"strings"
)

// AreaHeight is reserved above the pet without changing its artwork position.
const AreaHeight = 96

const MaxWidth = 184

const (
	padX      = 12
	padY      = 10
	textGap   = 6
	caretH    = 7
	caretHalf = 6
)

type Face interface {
	Measure(text string) (w, h int)
	Draw(dst *image.RGBA, x, y int, text string, c color.Color)
}

// Fonts keeps the header and detail metrics paired with their render faces.
type Fonts struct{ Header, Body Face }

type Box struct{ X, Y, W, H int }

type Line struct {
	Text string
	Box
}

type Layout struct {
	Bg     Box
	Header Line
	Body   []Line
	CaretX int
}

func (l Layout) Caret() Box {
	return Box{X: l.CaretX - caretHalf, Y: l.Bg.Y + l.Bg.H, W: caretHalf * 2, H: caretH}
}

// Compute uses a stable width and bottom anchor so changing copy never makes
// the bubble jump. Each text row is ellipsized, including long unbroken paths.
func Compute(canvasW int, header, body string, fonts Fonts) Layout {
	width := max(0, min(MaxWidth, canvasW-8))
	x := (canvasW - width) / 2
	_, headerH := fonts.Header.Measure("Ag")
	_, bodyH := fonts.Body.Measure("Ag")
	height := 2*padY + headerH + bodyH + textGap
	y := AreaHeight - 8 - caretH - height
	textW := max(0, width-2*padX)
	return Layout{
		Bg: Box{X: x, Y: y, W: width, H: height},
		Header: Line{Text: ellipsize(header, textW, fonts.Header), Box: Box{
			X: x + padX, Y: y + padY, W: textW, H: headerH,
		}},
		Body: []Line{{Text: ellipsize(body, textW, fonts.Body), Box: Box{
			X: x + padX, Y: y + padY + headerH + textGap, W: textW, H: bodyH,
		}}},
		CaretX: canvasW / 2,
	}
}

func ellipsize(text string, maxW int, face Face) string {
	text = strings.Join(strings.Fields(text), " ")
	if w, _ := face.Measure(text); w <= maxW {
		return text
	}
	if w, _ := face.Measure("…"); w > maxW {
		return ""
	}
	runes := []rune(text)
	// Binary search avoids repeatedly measuring a potentially long payload.
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if w, _ := face.Measure(string(runes[:mid]) + "…"); w <= maxW {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return strings.TrimSpace(string(runes[:lo])) + "…"
}
