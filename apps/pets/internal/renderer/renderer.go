//go:build sdl2

// Package renderer converts decoded atlas cells (image.Image) into SDL
// textures and renders the current animation frame to the overlay. It is the
// SDL2 delivery adapter for the pure-Go char package.
package renderer

import (
	"fmt"
	"image"
	"log/slog"
	"sort"
	"sync"
	"time"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"nusashell-pets/internal/bubble"
	"nusashell-pets/internal/char"
)

// Renderer owns the texture cache for one CharPack and renders the active
// state's animation to the given SDL renderer.
type Renderer struct {
	r    *sdl.Renderer
	log  *slog.Logger
	mu   sync.Mutex
	pack *char.CharPack
	// textures[state] -> decoded frames as SDL textures
	textures map[string][]*sdl.Texture
	// lookTextures contains the 16 v2 direction cells in clockwise order.
	lookTextures []*sdl.Texture
	// current playback state
	state     string
	frame     int
	lookIndex int
	holdLast  bool
	// runDir is the hold-and-drag overlay: -1 = running-left, 0 = none,
	// +1 = running-right. While non-zero it wins over the current state and
	// look cells so the pet visibly jogs in the drag direction.
	runDir int
	// Head speech bubble. Text is rasterized with golang.org/x/image/font
	// (pure Go) into the window composite; no SDL_ttf dependency.
	bubbleFonts   bubble.Fonts
	bubbleImage   *image.RGBA // antialiased text/panel cached until copy changes
	bubbleHeader  string
	bubbleBody    string
	bubbleVersion int
	bubbleLayout  *bubble.Layout
}

// FrameResult describes the frame rendered by Render. Image is the decoded
// cell used to derive the native X11 shape for that frame.
type FrameResult struct {
	Delay     time.Duration
	Image     image.Image
	State     string
	Frame     int
	LookIndex int
	Finished  bool
}

// New creates a Renderer bound to an SDL renderer. Call Load before Render.
func New(r *sdl.Renderer, log *slog.Logger) *Renderer {
	if log == nil {
		log = slog.Default()
	}
	return &Renderer{
		r:        r,
		log:      log,
		textures: make(map[string][]*sdl.Texture),
	}
}

// Load uploads a CharPack's frames into SDL textures. It must be called once
// before Render. Reloading frees the previous textures.
func (re *Renderer) Load(pack *char.CharPack) error {
	if pack == nil {
		return fmt.Errorf("renderer: pack is nil")
	}
	re.mu.Lock()
	defer re.mu.Unlock()
	nextTextures := make(map[string][]*sdl.Texture, len(pack.States))
	for state, anim := range pack.States {
		if anim == nil {
			freeTextures(nextTextures)
			return fmt.Errorf("renderer: state %s is nil", state)
		}
		frames := make([]*sdl.Texture, 0, len(anim.Frames))
		for _, img := range anim.Frames {
			t, err := re.imageToTexture(img)
			if err != nil {
				freeTextures(nextTextures)
				return fmt.Errorf("renderer: state %s: %w", state, err)
			}
			frames = append(frames, t)
		}
		nextTextures[state] = frames
	}
	nextLookTextures := make([]*sdl.Texture, 0, len(pack.LookFrames))
	for _, img := range pack.LookFrames {
		t, err := re.imageToTexture(img)
		if err != nil {
			freeTextures(nextTextures)
			freeTextureSlice(nextLookTextures)
			return fmt.Errorf("renderer: look direction: %w", err)
		}
		nextLookTextures = append(nextLookTextures, t)
	}
	re.freeLocked()
	re.pack = pack
	re.bubbleLayout = nil
	re.bubbleImage = nil
	re.bubbleVersion++
	re.textures = nextTextures
	re.lookTextures = nextLookTextures
	re.state = ""
	if _, ok := re.textures["idle"]; ok {
		re.state = "idle"
	} else {
		states := make([]string, 0, len(re.textures))
		for state := range re.textures {
			states = append(states, state)
		}
		sort.Strings(states)
		if len(states) > 0 {
			re.state = states[0]
		}
	}
	re.frame = 0
	re.lookIndex = -1
	re.holdLast = false
	re.runDir = 0
	return nil
}

// SetState switches the active animation. Unknown states are ignored.
func (re *Renderer) SetState(state string) {
	re.mu.Lock()
	defer re.mu.Unlock()
	if _, ok := re.textures[state]; !ok {
		return
	}
	if re.state != state {
		re.state = state
		re.frame = 0
		re.holdLast = false
	}
}

// SetLookDirection selects one of the 16 v2 direction cells while idle. -1
// means neutral/front and uses the idle animation. Out-of-range values are
// normalized to neutral.
func (re *Renderer) SetLookDirection(index int) {
	re.mu.Lock()
	defer re.mu.Unlock()
	if index < 0 || index >= len(re.lookTextures) {
		index = -1
	}
	if re.lookIndex != index {
		re.lookIndex = index
		re.frame = 0
		re.holdLast = false
	}
}

// SetRunDirection toggles the hold-and-drag running overlay. dir is +1 for
// running-right, -1 for running-left, and 0 to clear the overlay and resume
// the current state animation. States missing from the loaded pack (for
// example a legacy still-image pack) are ignored and playback continues as if
// the overlay were not active.
func (re *Renderer) SetRunDirection(dir int) {
	re.mu.Lock()
	defer re.mu.Unlock()
	if dir < 0 {
		dir = -1
	} else if dir > 0 {
		dir = 1
	}
	if re.runDir == dir {
		return
	}
	re.runDir = dir
	re.frame = 0
	re.holdLast = false
}

// Render draws the current frame and advances the animation clock. Callers
// should call Render at roughly the returned delay. The active decoded image
// is returned so the native window shape can follow silhouette changes.
func (re *Renderer) Render() FrameResult {
	re.mu.Lock()
	defer re.mu.Unlock()
	frames, images, stateName, look := re.activeFramesLocked()
	if len(frames) == 0 {
		// fallback: any available state
		for st, fs := range re.textures {
			if len(fs) > 0 {
				re.state = st
				re.frame = 0
				re.holdLast = false
				frames = fs
				images = re.pack.States[st].Frames
				stateName = st
				look = false
				break
			}
		}
		if len(frames) == 0 {
			return FrameResult{Delay: 100 * time.Millisecond}
		}
	}
	if re.frame < 0 || re.frame >= len(frames) {
		re.frame = 0
	}
	renderedFrame := re.frame
	renderedImage := imageAt(images, renderedFrame)
	tex := frames[renderedFrame]
	_, _, w, h, err := tex.Query()
	if err != nil {
		re.log.Warn("renderer: query texture", "err", err)
		return FrameResult{
			Delay: slowPlaybackDelay(100 * time.Millisecond), Image: renderedImage,
			State: stateName, Frame: renderedFrame, LookIndex: activeLookIndex(re.lookIndex, look),
		}
	}
	_ = re.r.SetDrawColor(0, 0, 0, 0)
	_ = re.r.Clear()
	if re.bubbleFonts.Header != nil && re.bubbleHeader != "" {
		full := re.windowImageLocked(renderedImage)
		ft, terr := re.imageToTexture(full)
		if terr != nil {
			re.log.Warn("renderer: bubble texture", "err", terr)
			if err := re.r.Copy(tex, nil, &sdl.Rect{X: 0, Y: int32(bubble.AreaHeight), W: w, H: h}); err != nil {
				re.log.Warn("renderer: copy", "err", err)
			}
		} else {
			fb := full.Bounds()
			if err := re.r.Copy(ft, nil, &sdl.Rect{X: 0, Y: 0, W: int32(fb.Dx()), H: int32(fb.Dy())}); err != nil {
				re.log.Warn("renderer: bubble copy", "err", err)
			}
			ft.Destroy()
		}
	} else {
		dst := &sdl.Rect{X: 0, Y: int32(bubble.AreaHeight), W: w, H: h}
		if err := re.r.Copy(tex, nil, dst); err != nil {
			re.log.Warn("renderer: copy", "err", err)
		}
	}
	re.r.Present()

	// Look cells are still frames in the atlas, but direction selection is a
	// stable one-cell pose rather than a looping animation.
	if look {
		return FrameResult{
			Delay: slowPlaybackDelay(100 * time.Millisecond), Image: renderedImage,
			State: stateName, Frame: renderedFrame, LookIndex: activeLookIndex(re.lookIndex, look),
		}
	}

	anim := re.pack.States[stateName]
	delayMs := 100
	if anim != nil && renderedFrame < len(anim.Delays) {
		delayMs = anim.Delays[renderedFrame]
	}
	finished := false
	if re.holdLast {
		finished = true
	} else {
		next, atEnd := nextFrame(re.frame, len(frames), anim == nil || anim.Loop)
		if atEnd {
			re.holdLast = true
		} else {
			re.frame = next
		}
	}
	return FrameResult{
		Delay:     slowPlaybackDelay(time.Duration(delayMs) * time.Millisecond),
		Image:     renderedImage,
		State:     stateName,
		Frame:     renderedFrame,
		LookIndex: activeLookIndex(re.lookIndex, look),
		Finished:  finished,
	}
}

// Destroy frees all cached textures.
func (re *Renderer) Destroy() {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.freeLocked()
}

func (re *Renderer) freeLocked() {
	freeTextures(re.textures)
	freeTextureSlice(re.lookTextures)
	re.textures = make(map[string][]*sdl.Texture)
	re.lookTextures = nil
}

func freeTextures(textures map[string][]*sdl.Texture) {
	for _, frames := range textures {
		for _, t := range frames {
			if t != nil {
				t.Destroy()
			}
		}
	}
}

func freeTextureSlice(textures []*sdl.Texture) {
	for _, t := range textures {
		if t != nil {
			t.Destroy()
		}
	}
}

func (re *Renderer) activeFramesLocked() ([]*sdl.Texture, []image.Image, string, bool) {
	// The drag overlay wins over the idle look cells and the current state:
	// while the pet is held and moving it jogs toward the drag direction.
	if re.runDir != 0 && re.pack != nil {
		name := char.StateRunningRight
		if re.runDir < 0 {
			name = char.StateRunningLeft
		}
		if anim := re.pack.States[name]; anim != nil && len(anim.Frames) > 0 && len(re.textures[name]) > 0 {
			return re.textures[name], anim.Frames, name, false
		}
	}
	if re.state == "idle" && re.lookIndex >= 0 && re.lookIndex < len(re.lookTextures) && re.pack != nil {
		return re.lookTextures[re.lookIndex : re.lookIndex+1], re.pack.LookFrames[re.lookIndex : re.lookIndex+1], "idle", true
	}
	if re.pack == nil {
		return nil, nil, re.state, false
	}
	anim := re.pack.States[re.state]
	if anim == nil {
		return nil, nil, re.state, false
	}
	return re.textures[re.state], anim.Frames, re.state, false
}

func imageAt(images []image.Image, index int) image.Image {
	if index < 0 || index >= len(images) {
		return nil
	}
	return images[index]
}

func activeLookIndex(index int, active bool) int {
	if !active {
		return -1
	}
	return index
}

// imageToTexture uploads an image.Image as an RGBA SDL texture.
func (re *Renderer) imageToTexture(img image.Image) (*sdl.Texture, error) {
	if img == nil {
		return nil, fmt.Errorf("image is nil")
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("image has empty bounds %v", b)
	}
	// SDL's BLENDMODE_BLEND expects straight alpha. Go's image.RGBA is
	// premultiplied; uploading it directly darkens antialiased edges twice.
	rgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			rgba.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	pixels := rgba.Pix
	tex, err := re.r.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888),
		sdl.TEXTUREACCESS_STATIC, int32(w), int32(h))
	if err != nil {
		return nil, fmt.Errorf("create texture: %w", err)
	}
	if err := tex.Update(nil, unsafe.Pointer(&pixels[0]), w*4); err != nil {
		tex.Destroy()
		return nil, fmt.Errorf("update texture: %w", err)
	}
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	return tex, nil
}
