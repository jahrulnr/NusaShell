package shape

import (
	"image"
	"image/color"
	"testing"
)

func TestMaskFromImageNormalizesBoundsAndKeepsOpaquePixels(t *testing.T) {
	img := image.NewRGBA(image.Rect(5, 7, 8, 10))
	img.SetRGBA(5, 7, color.RGBA{A: 255})
	img.SetRGBA(7, 9, color.RGBA{A: 255})

	mask, err := FromImage(img, 1)
	if err != nil {
		t.Fatalf("FromImage: %v", err)
	}
	if mask.Width != 3 || mask.Height != 3 {
		t.Fatalf("mask size = %dx%d, want 3x3", mask.Width, mask.Height)
	}
	if got := mask.AlphaAt(0, 0); got != 255 {
		t.Fatalf("top-left alpha = %d, want 255", got)
	}
	if got := mask.AlphaAt(2, 2); got != 255 {
		t.Fatalf("bottom-right alpha = %d, want 255", got)
	}
	if got := mask.AlphaAt(1, 1); got != 0 {
		t.Fatalf("middle alpha = %d, want 0", got)
	}
}

func TestMaskFromImageAppliesAlphaCutoff(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{A: 7})
	img.SetRGBA(1, 0, color.RGBA{A: 8})

	mask, err := FromImage(img, 8)
	if err != nil {
		t.Fatalf("FromImage: %v", err)
	}
	if got := mask.AlphaAt(0, 0); got != 0 {
		t.Fatalf("below-cutoff alpha = %d, want 0", got)
	}
	if got := mask.AlphaAt(1, 0); got != 255 {
		t.Fatalf("at-cutoff alpha = %d, want 255", got)
	}
}

func TestMaskFromImageRejectsEmptyOrFullyTransparentImages(t *testing.T) {
	if _, err := FromImage(nil, 1); err == nil {
		t.Fatal("nil image should fail")
	}
	if _, err := FromImage(image.NewRGBA(image.Rect(0, 0, 0, 0)), 1); err == nil {
		t.Fatal("empty image should fail")
	}
	if _, err := FromImage(image.NewRGBA(image.Rect(0, 0, 1, 1)), 1); err == nil {
		t.Fatal("fully transparent image should fail")
	}
}

func TestMaskFromImageRejectsInvalidCutoff(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, color.RGBA{A: 255})
	for _, cutoff := range []uint8{0} {
		if _, err := FromImage(img, cutoff); err == nil {
			t.Fatalf("cutoff %d should fail", cutoff)
		}
	}
}
