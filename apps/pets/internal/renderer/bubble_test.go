//go:build sdl2

package renderer

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"nusashell-pets/internal/bubble"
	"nusashell-pets/internal/char"
)

// Exercise the actual SDL texture/render path on a software surface, without
// requiring a display server or touching the user's desktop.
func TestBubbleSDLRenderAndShapeComposite(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const width, height = 192, 208 + bubble.AreaHeight
	surface, err := sdl.CreateRGBSurfaceWithFormat(0, width, height, 32, sdl.PIXELFORMAT_ABGR8888)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Free()
	sdlRenderer, err := sdl.CreateSoftwareRenderer(surface)
	if err != nil {
		t.Fatal(err)
	}
	defer sdlRenderer.Destroy()
	re := New(sdlRenderer, nil)
	defer re.Destroy()
	pack, err := char.LoadAtlas("../../assets/pets/spritesheet.webp", "preview", width, 208, char.DefaultAtlasLayout())
	if err != nil {
		t.Fatal(err)
	}
	if err := re.Load(pack); err != nil {
		t.Fatal(err)
	}
	fontPath := bubble.FindFont("")
	if fontPath == "" {
		t.Fatal("install a system TTF font for SDL bubble verification")
	}
	if err := re.LoadBubbleFonts(fontPath); err != nil {
		t.Fatal(err)
	}
	previews := []struct{ state, header, body string }{
		{"thinking", "Thinking…", "Thinking it through…"},
		{"reasoning", "Executing…", "read_file(...)"},
		{"waiting", "Waiting…", "Choose how to continue."},
	}
	contact := image.NewRGBA(image.Rect(0, 0, width*len(previews), height))
	draw.Draw(contact, contact.Bounds(), image.NewUniform(color.RGBA{12, 24, 30, 255}), image.Point{}, draw.Src)
	for i, preview := range previews {
		re.SetState(preview.state)
		re.SetBubble(preview.header, preview.body)
		version := re.BubbleVersion()
		re.SetBubble(preview.header, preview.body)
		if re.BubbleVersion() != version {
			t.Fatal("unchanged copy invalidated the shape cache")
		}
		frame := re.Render()
		composite := re.WindowImage(frame.Image).(*image.RGBA)
		cached := re.bubbleImage
		re.WindowImage(frame.Image)
		if re.bubbleImage != cached {
			t.Fatal("unchanged copy rasterized the bubble again")
		}
		pixels := image.NewRGBA(composite.Bounds())
		if err := sdlRenderer.ReadPixels(nil, sdl.PIXELFORMAT_ABGR8888, unsafe.Pointer(&pixels.Pix[0]), pixels.Stride); err != nil {
			t.Fatal(err)
		}
		l := re.bubbleLayout
		if l == nil || len(l.Body) != 1 {
			t.Fatal("renderer did not compose two-line bubble")
		}
		for y := l.Bg.Y; y < l.Bg.Y+l.Bg.H; y++ {
			for x := l.Bg.X; x < l.Bg.X+l.Bg.W; x++ {
				got, want := pixels.RGBAAt(x, y), composite.RGBAAt(x, y)
				// Unpremultiplication and SDL's 8-bit blend can round RGB by
				// one level; alpha coverage must still match exactly.
				delta := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
				if got.A != want.A || delta(got.R, want.R) > 1 || delta(got.G, want.G) > 1 || delta(got.B, want.B) > 1 {
					t.Fatalf("%s SDL pixel (%d,%d)=%v, shape composite=%v", preview.state, x, y, got, want)
				}
			}
		}
		if composite.RGBAAt(0, 0).A != 0 {
			t.Fatal("outside bubble must stay transparent")
		}
		draw.Draw(contact, image.Rect(i*width, 0, (i+1)*width, height), composite, image.Point{}, draw.Over)
	}
	re.SetBubble("", "")
	frame := re.Render()
	if re.WindowImage(frame.Image).(*image.RGBA).RGBAAt(width/2, 40).A != 0 {
		t.Fatal("cleared bubble still occupies window shape")
	}
	if output := os.Getenv("PETS_BUBBLE_PREVIEW_DIR"); output != "" {
		file, err := os.Create(filepath.Join(output, "bubble-preview.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := png.Encode(file, contact); err != nil {
			t.Fatal(err)
		}
	}
}
