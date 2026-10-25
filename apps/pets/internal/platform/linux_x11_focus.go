//go:build sdl2

package platform

// #cgo pkg-config: x11
// #include <stdint.h>
// #include <string.h>
// #include <strings.h>
// #include <X11/Xlib.h>
// #include <X11/Xutil.h>
// #include <X11/Xatom.h>
//
// // SDL owns its connection. Use a short-lived connection for this request,
// // so only this operation's stale-window errors are consumed. The main SDL
// // thread owns all calls; do not enter this process-global handler twice.
// static Display *focus_display;
// static int (*focus_previous_handler)(Display *, XErrorEvent *);
// static int focus_error(Display *d, XErrorEvent *e) {
//     if (d == focus_display && e->error_code == BadWindow) return 0;
//     return focus_previous_handler ? focus_previous_handler(d, e) : 0;
// }
// static Window electron_main_window(Display *d) {
//     Atom actual; int format; unsigned long n, remaining; unsigned char *value = NULL;
//     Atom list = XInternAtom(d, "_NET_CLIENT_LIST_STACKING", False);
//     if (XGetWindowProperty(d, DefaultRootWindow(d), list, 0, 4096, False,
//         XA_WINDOW, &actual, &format, &n, &remaining, &value) != Success || !value) return None;
//     Window found = None;
//     if (actual == XA_WINDOW && format == 32) {
//         Window *windows = (Window *)value;
//         Atom name = XInternAtom(d, "_NET_WM_NAME", False);
//         for (unsigned long i = n; i > 0 && !found; --i) {
//             XClassHint hint = {0};
//             if (!XGetClassHint(d, windows[i-1], &hint)) continue;
//             int matches = hint.res_name && !strcasecmp(hint.res_name, "nusashell-desktop");
//             if (hint.res_name) XFree(hint.res_name);
//             if (hint.res_class) XFree(hint.res_class);
//             if (!matches) continue;
//             unsigned char *title = NULL; unsigned long length;
//             if (XGetWindowProperty(d, windows[i-1], name, 0, 64, False,
//                 AnyPropertyType, &actual, &format, &length, &remaining, &title) == Success &&
//                 title && format == 8 && length == 9 && !memcmp(title, "NusaShell", 9)) found = windows[i-1];
//             if (title) XFree(title);
//         }
//     }
//     XFree(value);
//     return found;
// }
// static int focus_electron_uintptr(uintptr_t display_value) {
//     if (!display_value || focus_display) return 0;
//     Display *d = XOpenDisplay(DisplayString((Display *)display_value));
//     if (!d) return 0;
//     focus_display = d;
//     focus_previous_handler = XSetErrorHandler(focus_error);
//     Window w = electron_main_window(d);
//     int sent = 0;
//     if (w) {
//         XEvent e = {0};
//         e.xclient.type = ClientMessage;
//         e.xclient.window = w;
//         e.xclient.message_type = XInternAtom(d, "_NET_ACTIVE_WINDOW", False);
//         e.xclient.format = 32;
//         e.xclient.data.l[0] = 2; // Explicit user click, pager source.
//         e.xclient.data.l[1] = CurrentTime;
//         sent = XSendEvent(d, DefaultRootWindow(d), False,
//             SubstructureRedirectMask | SubstructureNotifyMask, &e);
//     }
//     XSync(d, False);
//     XCloseDisplay(d);
//     XSetErrorHandler(focus_previous_handler);
//     focus_previous_handler = NULL;
//     focus_display = NULL;
//     return sent;
// }
import "C"

// FocusElectronWindow asks the EWMH window manager to activate/restore the
// existing NusaShell main window. It does not start another Electron process.
// The return value means a request was sent, not an acknowledgement of focus.
// Call on the SDL thread. False leaves the normal launch path available when
// no matching window exists (for example while Electron is still starting).
func FocusElectronWindow(display uintptr) bool {
	return display != 0 && C.focus_electron_uintptr(C.uintptr_t(display)) != 0
}
