// Package shape converts image alpha into a binary mask suitable for an SDL2
// shaped window. It has no SDL dependency so the geometry can be tested in
// ordinary Go unit tests.
package shape

import (
	"fmt"
	"image"
)

// Mask is a row-major alpha mask. Alpha is either 0 or 255 so it can be used
// consistently both as an SDL window-shape surface and as an input silhouette.
type Mask struct {
	Width  int
	Height int
	Alpha  []byte
}

// FromImage builds a binary mask from img. Pixels with alpha >= cutoff are
// included. Image bounds are normalized to (0,0), which matters for decoded
// image frames whose bounds may have a non-zero origin.
func FromImage(img image.Image, cutoff uint8) (*Mask, error) {
	if img == nil {
		return nil, fmt.Errorf("shape: image is nil")
	}
	if cutoff == 0 {
		return nil, fmt.Errorf("shape: alpha cutoff must be greater than zero")
	}
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("shape: image has empty bounds %v", b)
	}

	mask := &Mask{Width: width, Height: height, Alpha: make([]byte, width*height)}
	opaque := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			_, _, _, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			if uint8(a>>8) >= cutoff {
				mask.Alpha[y*width+x] = 255
				opaque++
			}
		}
	}
	if opaque == 0 {
		return nil, fmt.Errorf("shape: image has no pixels above alpha cutoff %d", cutoff)
	}
	return mask, nil
}

// AlphaAt returns the binary alpha at x,y. Out-of-bounds coordinates are
// treated as transparent, which is useful when hit-testing a pointer.
func (m *Mask) AlphaAt(x, y int) byte {
	if m == nil || x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return 0
	}
	return m.Alpha[y*m.Width+x]
}
