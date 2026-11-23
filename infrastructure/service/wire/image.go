package wire

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"sync"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	_ "golang.org/x/image/webp"
)

// Images a model is shown are sent no larger than it can use, and in a
// format the wire adapters can carry: PNG or JPEG. Vision models downscale
// what they receive to about this many pixels on the long side, so a bigger
// picture only costs upload time and, on a slow link or a proxy with a
// header timeout, the request itself. A conversation that loads a few dozen
// screenshots would otherwise carry tens of megabytes on every step.
//
// The change is made here, at the wire boundary where inline data and local
// files become sendable payloads, and is a pure function of the bytes: the
// same input always yields the same payload, so a message encodes
// identically on every request. A provider's prompt cache matches on the
// prefix, and a picture that came out different from one request to the
// next would break it for everything after it.
const (
	shrinkAbove   = 512 << 10 // try to shrink larger JPEGs and PNGs
	maxImageSide  = 2048      // longest side, in pixels
	maxImagePixel = 100 << 20 // a larger picture is not decoded (decompression bombs)
	jpegQuality   = 85
	shrinkCacheMB = 64 << 20 // finished payloads kept so a repeat costs no decode
)

// prepareImage validates the actual encoding and uses PNG/JPEG on the wire.
// Converting at this boundary keeps the original and its content pin intact.
func prepareImage(raw []byte) ([]byte, string, error) {
	key := sha256.Sum256(raw)
	if out, ok := shrunk.get(key); ok {
		return out.data, out.mediaType, nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("cannot identify a supported image: %w", err)
	}
	var mediaType string
	switch format {
	case "png", "jpeg":
		mediaType = "image/" + format
	case "gif", "webp":
		// These formats do not travel consistently across vision routes.
	default:
		return nil, "", fmt.Errorf("unsupported image format %q", format)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImagePixel/cfg.Height {
		return nil, "", fmt.Errorf("image dimensions exceed media limit")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("cannot decode %s image: %w", format, err)
	}
	out := shrunkImage{data: raw, mediaType: mediaType}
	if mediaType == "" || len(raw) > shrinkAbove || max(cfg.Width, cfg.Height) > maxImageSide {
		data, mt, err := reencode(img)
		if err != nil {
			return nil, "", err
		}
		if mediaType == "" || len(data) < len(raw) || max(cfg.Width, cfg.Height) > maxImageSide {
			out = shrunkImage{data: data, mediaType: mt}
		}
	}
	if int64(len(out.data)) > MaxAttachmentBytes(enum.ModalityImage) {
		return nil, "", fmt.Errorf("prepared image exceeds media limit")
	}
	shrunk.put(key, out)
	return out.data, out.mediaType, nil
}

func reencode(img image.Image) ([]byte, string, error) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if long := max(w, h); long > maxImageSide {
		img = boxScale(img, int(int64(maxImageSide)*int64(w)/int64(long)), int(int64(maxImageSide)*int64(h)/int64(long)))
	}
	var buf bytes.Buffer
	if opaque, ok := img.(interface{ Opaque() bool }); ok && !opaque.Opaque() {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/png", nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/jpeg", nil
}

// boxScale shrinks src to w by h, each new pixel the average of the source
// pixels it covers.
func boxScale(src image.Image, w, h int) image.Image {
	w, h = max(w, 1), max(h, 1)
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r, g, bl, a, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa), n+1
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), uint8(a / n >> 8)})
		}
	}
	return dst
}

type shrunkImage struct {
	data      []byte
	mediaType string
}

// shrunk keeps finished payloads by the digest of the file they came from,
// oldest out first, so a conversation re-sending the same screenshots on each
// step decodes each only once.
var shrunk = &shrinkCache{items: map[[32]byte]shrunkImage{}}

type shrinkCache struct {
	mu    sync.Mutex
	items map[[32]byte]shrunkImage
	order [][32]byte
	size  int
}

func (c *shrinkCache) get(key [32]byte) (shrunkImage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[key]
	return v, ok
}

func (c *shrinkCache) put(key [32]byte, v shrunkImage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; ok {
		return
	}
	c.items[key] = v
	c.order = append(c.order, key)
	c.size += len(v.data)
	for c.size > shrinkCacheMB && len(c.order) > 1 {
		old := c.order[0]
		c.order = c.order[1:]
		c.size -= len(c.items[old].data)
		delete(c.items, old)
	}
}
