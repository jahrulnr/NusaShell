//go:build sdl2

// Package platform contains the Linux window-system adapter for the pet.
// This file applies X11 Shape bounding/input regions and reads the root pointer;
// SDL owns the window creation, rendering, and always-on-top behavior.
package platform

// #cgo pkg-config: x11 xext
//
// #include <stdint.h>
// #include <stdlib.h>
// #include <string.h>
// #include <X11/Xlib.h>
// #include <X11/extensions/shape.h>
//
// // Keep native pointer casts in C. The Go API only passes opaque handles,
// // which avoids unsafe.Pointer conversions at the cgo boundary.
// static int hex_nibble(char value) {
//     if (value >= '0' && value <= '9') return value - '0';
//     if (value >= 'a' && value <= 'f') return value - 'a' + 10;
//     if (value >= 'A' && value <= 'F') return value - 'A' + 10;
//     return -1;
// }
//
// static int set_bounding_shape_hex_uintptr(uintptr_t display_value,
//                                            uintptr_t window_value,
//                                            const char *alpha_hex,
//                                            size_t alpha_hex_len,
//                                            unsigned int width,
//                                            unsigned int height) {
//     Display *dpy = (Display *)(uintptr_t)display_value;
//     Window win = (Window)window_value;
//     int event_base = 0;
//     int error_base = 0;
//     size_t pixels;
//     size_t bytes_per_row;
//     size_t bitmap_size;
//     unsigned char *bitmap;
//     Pixmap shapemask;
//     unsigned int y;
//     unsigned int x;
//     if (dpy == NULL || win == 0 || alpha_hex == NULL || width == 0 || height == 0) {
//         return -1;
//     }
//     if (!XShapeQueryExtension(dpy, &event_base, &error_base)) {
//         return -2;
//     }
//     pixels = (size_t)width * (size_t)height;
//     if (alpha_hex_len != pixels * 2) {
//         return -3;
//     }
//     bytes_per_row = ((size_t)width + 7) / 8;
//     bitmap_size = bytes_per_row * (size_t)height;
//     bitmap = (unsigned char *)calloc(bitmap_size, 1);
//     if (bitmap == NULL) {
//         return -4;
//     }
//     for (y = 0; y < height; ++y) {
//         for (x = 0; x < width; ++x) {
//             size_t offset = ((size_t)y * width + x) * 2;
//             int high = hex_nibble(alpha_hex[offset]);
//             int low = hex_nibble(alpha_hex[offset + 1]);
//             if (high < 0 || low < 0) {
//                 free(bitmap);
//                 return -3;
//             }
//             if ((high << 4 | low) != 0) {
//                 bitmap[(size_t)y * bytes_per_row + x / 8] |= (unsigned char)(1u << (x % 8));
//             }
//         }
//     }
//     shapemask = XCreateBitmapFromData(dpy, win, (const char *)bitmap, width, height);
//     free(bitmap);
//     if (shapemask == None) {
//         return -5;
//     }
//     XShapeCombineMask(dpy, win, ShapeBounding, 0, 0, shapemask, ShapeSet);
//     XFreePixmap(dpy, shapemask);
//     XSync(dpy, False);
//     return 0;
// }
//
// static int set_input_mode_uintptr(uintptr_t display_value, uintptr_t window_value,
//                                   int click_through) {
//     Display *dpy = (Display *)(uintptr_t)display_value;
//     Window win = (Window)window_value;
//     int event_base = 0;
//     int error_base = 0;
//     if (dpy == NULL || win == 0) {
//         return -1;
//     }
//     if (!XShapeQueryExtension(dpy, &event_base, &error_base)) {
//         return -2;
//     }
//     if (click_through) {
//         // ShapeSet with zero rectangles makes the input region empty.
//         XShapeCombineRectangles(dpy, win, ShapeInput, 0, 0, NULL, 0,
//                                 ShapeSet, Unsorted);
//     } else {
//         // Copy SDL's alpha-derived bounding shape into the input shape. The
//         // transparent canvas and holes therefore remain mouse-pass-through.
//         XShapeCombineShape(dpy, win, ShapeInput, 0, 0, win, ShapeBounding,
//                            ShapeSet);
//     }
//     XFlush(dpy);
//     return 0;
// }
//
// static int query_pointer_uintptr(uintptr_t display_value, int *root_x,
//                                  int *root_y, unsigned int *mask) {
//     Display *dpy = (Display *)(uintptr_t)display_value;
//     Window root_return;
//     Window child_return;
//     int win_x;
//     int win_y;
//     unsigned int pointer_mask;
//     if (dpy == NULL || root_x == NULL || root_y == NULL || mask == NULL) {
//         return -1;
//     }
//     if (!XQueryPointer(dpy, DefaultRootWindow(dpy), &root_return, &child_return,
//                        root_x, root_y, &win_x, &win_y, &pointer_mask)) {
//         return -2;
//     }
//     *mask = pointer_mask;
//     return 0;
// }
//
// static void free_c_string(char *value) { free(value); }
import "C"

import (
	"encoding/hex"
	"fmt"

	"nusashell-pets/internal/shape"
)

// SetBoundingShape applies a binary alpha mask to the X11 bounding region.
// Pixels outside the region are both invisible and absent from hit testing.
func SetBoundingShape(display, win uintptr, mask *shape.Mask) error {
	if display == 0 || win == 0 {
		return fmt.Errorf("platform: nil X11 display/window")
	}
	if mask == nil || mask.Width <= 0 || mask.Height <= 0 {
		return fmt.Errorf("platform: shape mask is empty")
	}
	if len(mask.Alpha) != mask.Width*mask.Height {
		return fmt.Errorf("platform: invalid shape mask length %d for %dx%d", len(mask.Alpha), mask.Width, mask.Height)
	}
	encoded := hex.EncodeToString(mask.Alpha)
	cEncoded := C.CString(encoded)
	defer C.free_c_string(cEncoded)
	result := C.set_bounding_shape_hex_uintptr(C.uintptr_t(display), C.uintptr_t(win),
		cEncoded, C.size_t(len(encoded)), C.uint(mask.Width), C.uint(mask.Height))
	switch result {
	case 0:
		return nil
	case -2:
		return fmt.Errorf("platform: X11 Shape extension is unavailable")
	case -3:
		return fmt.Errorf("platform: invalid X11 shape mask encoding")
	case -4:
		return fmt.Errorf("platform: allocate X11 shape mask failed")
	case -5:
		return fmt.Errorf("platform: create X11 shape pixmap failed")
	default:
		return fmt.Errorf("platform: set X11 bounding shape failed")
	}
}

// SetInputMode updates the X11 input region for an already-shaped window.
// When clickThrough is false, input follows the window's alpha-derived
// bounding shape; when true, the whole window is skipped by pointer hit tests.
func SetInputMode(display, win uintptr, clickThrough bool) error {
	if display == 0 || win == 0 {
		return fmt.Errorf("platform: nil X11 display/window")
	}
	result := C.set_input_mode_uintptr(C.uintptr_t(display), C.uintptr_t(win), C.int(boolInt(clickThrough)))
	switch result {
	case 0:
		return nil
	case -2:
		return fmt.Errorf("platform: X11 Shape extension is unavailable")
	default:
		return fmt.Errorf("platform: set X11 input region failed")
	}
}

// PointerState is the desktop pointer state reported by X11.
type PointerState struct {
	X              int32
	Y              int32
	LeftButtonHeld bool
}

// QueryPointer reads the root-window pointer position and button state. Unlike
// SDL mouse events, this stays available while a shaped dock window is not
// focused and the pointer is outside its input region.
func QueryPointer(display uintptr) (PointerState, error) {
	if display == 0 {
		return PointerState{}, fmt.Errorf("platform: nil X11 display")
	}
	var x, y C.int
	var mask C.uint
	result := C.query_pointer_uintptr(C.uintptr_t(display), &x, &y, &mask)
	switch result {
	case 0:
		return PointerState{
			X:              int32(x),
			Y:              int32(y),
			LeftButtonHeld: uint32(mask)&(1<<8) != 0, // X11 Button1Mask
		}, nil
	case -2:
		return PointerState{}, fmt.Errorf("platform: pointer is not on the X11 root window")
	default:
		return PointerState{}, fmt.Errorf("platform: query X11 pointer failed")
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
