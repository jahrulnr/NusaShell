package wire

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noisyPNG is a picture that does not compress, so a modest size is
// already past the threshold.
func noisyPNG(t *testing.T, w, h int, alpha bool) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			a := uint8(255)
			if alpha {
				a = uint8(rng.Intn(256))
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), a})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPrepareImageKeepsSmallPNGAndRejectsCorruptImages(t *testing.T) {
	small := noisyPNG(t, 40, 40, false)
	out, mt, err := prepareImage(small)
	if err != nil || !bytes.Equal(out, small) || mt != "image/png" {
		t.Fatalf("a small PNG must go out as it is: %v", err)
	}
	corrupt := small[:len(small)/2]
	if _, _, err := image.DecodeConfig(bytes.NewReader(corrupt)); err != nil {
		t.Fatalf("fixture should have a valid header: %v", err)
	}
	if _, _, err := prepareImage(corrupt); err == nil {
		t.Fatal("bytes that do not decode must be rejected")
	}
	if _, _, err := prepareImage([]byte("RIFF\x00\x00\x00\x00WEBPbad")); err == nil {
		t.Fatal("a WebP that does not decode must be rejected")
	}
}

func TestPrepareImageMakesALargeOpaqueImageSmallerAndFitsIt(t *testing.T) {
	raw := noisyPNG(t, 2600, 700, false)
	out, mt, err := prepareImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if mt != "image/jpeg" || len(out) >= len(raw)/4 {
		t.Fatalf("media type %q, %d bytes from %d", mt, len(out), len(raw))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != maxImageSide || cfg.Height != 700*maxImageSide/2600 {
		t.Fatalf("size = %dx%d", cfg.Width, cfg.Height)
	}
}

func TestPrepareImageKeepsTransparencyAsPNG(t *testing.T) {
	raw := noisyPNG(t, 700, 700, true)
	out, mt, err := prepareImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if mt != "image/png" {
		t.Fatalf("a transparent image became %q", mt)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatal(err)
	}
}

// A provider's prompt cache matches on the prefix, so a message must encode
// to the same bytes on every request: the same file in, the same payload out.
func TestPrepareImageIsDeterministic(t *testing.T) {
	raw := noisyPNG(t, 2300, 500, false)
	first, _, err := prepareImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Bypass the cache: the result must not depend on having been seen.
	shrunk = &shrinkCache{items: map[[32]byte]shrunkImage{}}
	again, _, err := prepareImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, again) {
		t.Fatal("the same image encoded differently the second time")
	}
}

func TestResolveURISendsALocalImageSmallAndKeepsItsPinned(t *testing.T) {
	root, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw := noisyPNG(t, 900, 900, false)
	path := filepath.Join(root, "frame.png")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	att := mustAttachment(t, "file://"+path, "image/png")

	u, err := ResolveURI(att, localMediaRoot(root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, "data:image/jpeg;base64,") {
		t.Fatalf("a large image went out as %.40s", u)
	}
	if len(u) >= len(raw) {
		t.Fatalf("the payload (%d) is not smaller than the file (%d)", len(u), len(raw))
	}

	data, mediaType, err := ResolveBase64(att, localMediaRoot(root))
	if err != nil || mediaType != "image/jpeg" {
		t.Fatalf("ResolveBase64 = %q, %v", mediaType, err)
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		t.Fatal(err)
	}

	// The pin still binds the file on disk, not what was sent: a swapped
	// file fails closed.
	pinned := mustAttachment(t, "file://"+path, "image/png")
	pinned.SetSHA256(strings.Repeat("0", 64))
	if _, err := ResolveURI(pinned, localMediaRoot(root)); err == nil {
		t.Fatal("a changed file must not resolve")
	}
}

// WebP reaches the wire as PNG (transparency) or JPEG (opaque) — the payload
// is decided by the decoded image, never the declared type.
func TestResolveWebPAsPortableImage(t *testing.T) {
	transparent, err := base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAkAAEA8Q8x/zH4wJiOh/BAA=")
	if err != nil {
		t.Fatal(err)
	}
	opaque, err := base64.StdEncoding.DecodeString("UklGRhwAAABXRUJQVlA4TA8AAAAvAkAAAAcQ/Y/+ByKi/wEA")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		raw  []byte
		want string
	}{
		{"transparent", transparent, "image/png"},
		{"opaque", opaque, "image/jpeg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sum := sha256.Sum256(tc.raw)
			pin := hex.EncodeToString(sum[:])

			root := t.TempDir()
			rootPath, err := filepath.Abs(root)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "shot.webp")
			if err := os.WriteFile(path, tc.raw, 0o600); err != nil {
				t.Fatal(err)
			}
			att := mustAttachment(t, "file://"+path, "image/webp")
			u, err := ResolveURI(att, localMediaRoot(rootPath))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(u, "data:"+tc.want+";base64,") {
				t.Fatalf("file:// resolved as %.40s", u)
			}
			att.SetSHA256(pin)
			if _, err := ResolveURI(att, localMediaRoot(rootPath)); err != nil {
				t.Fatalf("the pin binds the file, not the payload: %v", err)
			}
			bad := sha256.Sum256([]byte("bad"))
			att.SetSHA256(hex.EncodeToString(bad[:]))
			if _, err := ResolveURI(att, localMediaRoot(rootPath)); err == nil {
				t.Fatal("a wrong pin must fail")
			}

			dataURI := "data:image/webp;base64," + base64.StdEncoding.EncodeToString(tc.raw)
			att = mustAttachment(t, dataURI, "image/webp")
			u, err = ResolveURI(att, MediaRoot{})
			if err != nil {
				t.Fatal(err)
			}
			payload, ok := strings.CutPrefix(u, "data:"+tc.want+";base64,")
			if !ok {
				t.Fatalf("data: resolved as %.40s", u)
			}
			encoded, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				t.Fatal(err)
			}
			img, _, err := image.Decode(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			if b := img.Bounds(); b.Dx() != 3 || b.Dy() != 2 {
				t.Fatalf("decoded %dx%d, want 3x2", b.Dx(), b.Dy())
			}
			if tc.name == "transparent" {
				if _, _, _, alpha := img.At(1, 0).RGBA(); alpha != 0 {
					t.Fatalf("transparent pixel has alpha %d", alpha)
				}
			}

			// The declared type never decides: a WebP mislabeled PNG still
			// resolves by its bytes.
			att = mustAttachment(t, "file://"+path, "image/png")
			u, err = ResolveURI(att, localMediaRoot(rootPath))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(u, "data:"+tc.want+";base64,") {
				t.Fatalf("mislabeled file resolved as %.40s", u)
			}
		})
	}
}
