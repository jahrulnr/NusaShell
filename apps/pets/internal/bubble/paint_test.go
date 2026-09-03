package bubble

import (
	"image"
	"testing"
)

func TestPanelHasAntialiasedCornersAndSubtleDepth(t *testing.T) {
	f := fakeFace{size: 6}
	l := Compute(192, "", "", Fonts{f, f})
	canvas := image.NewRGBA(image.Rect(0, 0, 192, AreaHeight))
	Draw(canvas, l, DefaultStyle(), Fonts{f, f})
	partial := 0
	for y := l.Bg.Y; y < l.Bg.Y+12; y++ {
		for x := l.Bg.X; x < l.Bg.X+12; x++ {
			a := canvas.RGBAAt(x, y).A
			if a > 0 && a < 255 {
				partial++
			}
		}
	}
	if partial < 5 {
		t.Fatalf("rounded corner has no smooth coverage: %d partial pixels", partial)
	}
	top := canvas.RGBAAt(l.CaretX, l.Bg.Y+3)
	bottom := canvas.RGBAAt(l.CaretX, l.Bg.Y+l.Bg.H-4)
	if top.R <= bottom.R || top.G <= bottom.G || top.B <= bottom.B || top.A != 255 || bottom.A != 255 {
		t.Fatalf("want subtle opaque top light, got %v -> %v", top, bottom)
	}
}
