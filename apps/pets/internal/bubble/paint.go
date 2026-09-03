package bubble

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/vector"
)

// Style holds the dark panel's surface, rim light, and two text tones.
type Style struct {
	Bg, Top, Border, HeaderText, BodyText color.Color
}

func DefaultStyle() Style {
	return Style{
		Bg:         color.RGBA{30, 40, 36, 255},
		Top:        color.RGBA{44, 58, 49, 255},
		Border:     color.RGBA{86, 105, 91, 255},
		HeaderText: color.RGBA{237, 243, 233, 255},
		BodyText:   color.RGBA{207, 219, 209, 255},
	}
}

// Draw rasterizes the panel and its caret as one antialiased contour. A thin
// rim and quiet top-to-bottom lighting add depth without a bulky shadow.
func Draw(canvas *image.RGBA, l Layout, style Style, fonts Fonts) {
	panelPath(canvas, l, 0, image.NewUniform(style.Border))
	gradient := image.NewRGBA(canvas.Bounds())
	top := color.RGBAModel.Convert(style.Top).(color.RGBA)
	bottom := color.RGBAModel.Convert(style.Bg).(color.RGBA)
	for y := l.Bg.Y; y < l.Caret().Y+l.Caret().H; y++ {
		t := min(1.0, max(0.0, float64(y-l.Bg.Y)/float64(max(1, l.Bg.H-1))))
		mix := func(a, b uint8) uint8 { return uint8(float64(a)*(1-t) + float64(b)*t + 0.5) }
		c := color.RGBA{mix(top.R, bottom.R), mix(top.G, bottom.G), mix(top.B, bottom.B), 255}
		draw.Draw(gradient, image.Rect(l.Bg.X, y, l.Bg.X+l.Bg.W, y+1), image.NewUniform(c), image.Point{}, draw.Src)
	}
	panelPath(canvas, l, 1, gradient)
	fonts.Header.Draw(canvas, l.Header.X, l.Header.Y, l.Header.Text, style.HeaderText)
	for _, line := range l.Body {
		fonts.Body.Draw(canvas, line.X, line.Y, line.Text, style.BodyText)
	}
}

func panelPath(dst *image.RGBA, l Layout, inset float32, source image.Image) {
	x0, y0 := float32(l.Bg.X)+inset, float32(l.Bg.Y)+inset
	x1, y1 := float32(l.Bg.X+l.Bg.W)-inset, float32(l.Bg.Y+l.Bg.H)-inset
	r := min(float32(12)-inset, (x1-x0)/2, (y1-y0)/2)
	if r <= 0 {
		return
	}
	cx := float32(l.CaretX)
	half := float32(caretHalf) - inset
	tip := float32(l.Caret().Y+l.Caret().H) - inset
	z := vector.NewRasterizer(dst.Bounds().Dx(), dst.Bounds().Dy())
	z.MoveTo(x0+r, y0)
	z.LineTo(x1-r, y0)
	z.QuadTo(x1, y0, x1, y0+r)
	z.LineTo(x1, y1-r)
	z.QuadTo(x1, y1, x1-r, y1)
	z.LineTo(cx+half, y1)
	z.LineTo(cx+1, tip-1)
	z.QuadTo(cx, tip, cx-1, tip-1)
	z.LineTo(cx-half, y1)
	z.LineTo(x0+r, y1)
	z.QuadTo(x0, y1, x0, y1-r)
	z.LineTo(x0, y0+r)
	z.QuadTo(x0, y0, x0+r, y0)
	z.ClosePath()
	z.Draw(dst, dst.Bounds(), source, image.Point{})
}
