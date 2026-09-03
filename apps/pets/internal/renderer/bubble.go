//go:build sdl2

package renderer

import (
	"image"

	xdraw "golang.org/x/image/draw"

	"nusashell-pets/internal/bubble"
)

// LoadBubbleFonts loads the 14px bold header and 12px detail faces. The
// caller decides what to do on failure (usually: bubble stays disabled).
func (re *Renderer) LoadBubbleFonts(path string) error {
	fonts, err := bubble.LoadFonts(path)
	if err != nil {
		return err
	}
	re.mu.Lock()
	defer re.mu.Unlock()
	re.bubbleFonts = fonts
	re.bubbleLayout = nil
	re.bubbleImage = nil
	re.bubbleVersion++
	return nil
}

// SetBubble shows (or clears with an empty header) the head speech bubble.
// The header carries the state caption, the body the latest event message.
func (re *Renderer) SetBubble(header, body string) {
	re.mu.Lock()
	defer re.mu.Unlock()
	if header == re.bubbleHeader && body == re.bubbleBody {
		return
	}
	re.bubbleHeader = header
	re.bubbleBody = body
	re.bubbleLayout = nil
	re.bubbleImage = nil
	re.bubbleVersion++
}

// BubbleVersion increments whenever the bubble content changes. The event
// loop folds it into the X11 shape cache key.
func (re *Renderer) BubbleVersion() int {
	re.mu.Lock()
	defer re.mu.Unlock()
	return re.bubbleVersion
}

// WindowImage returns the full-window RGBA the native X11 shape should follow
// for the last rendered frame: the pet cell at its offset plus any active
// bubble. It is called by the event loop only when the shape key changes.
func (re *Renderer) WindowImage(rendered image.Image) image.Image {
	re.mu.Lock()
	defer re.mu.Unlock()
	return re.windowImageLocked(rendered)
}

// windowImageLocked builds the window-sized composite. Callers hold re.mu.
func (re *Renderer) windowImageLocked(rendered image.Image) image.Image {
	ww := re.pack.FrameWidth
	wh := re.pack.FrameHeight + bubble.AreaHeight
	full := image.NewRGBA(image.Rect(0, 0, ww, wh))
	if rendered != nil {
		b := rendered.Bounds()
		xdraw.Draw(full, image.Rect(0, bubble.AreaHeight, ww, bubble.AreaHeight+b.Dy()),
			rendered, b.Min, xdraw.Over)
	}
	if re.bubbleFonts.Header != nil && re.bubbleHeader != "" {
		if re.bubbleImage == nil {
			l := bubble.Compute(ww, re.bubbleHeader, re.bubbleBody, re.bubbleFonts)
			re.bubbleLayout = &l
			re.bubbleImage = image.NewRGBA(image.Rect(0, 0, ww, bubble.AreaHeight))
			bubble.Draw(re.bubbleImage, l, bubble.DefaultStyle(), re.bubbleFonts)
		}
		xdraw.Draw(full, re.bubbleImage.Bounds(), re.bubbleImage, image.Point{}, xdraw.Over)
	}
	return full
}
