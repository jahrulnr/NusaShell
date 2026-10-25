/* Real X server regression: shrinking shapes must reveal current client
 * pixels without changing another client's background/ClearArea behavior. */
#include <stdio.h>
#include <stdlib.h>
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <X11/extensions/shape.h>
#include <X11/extensions/Xcomposite.h>

static int failed;
static void pixel(Display *d, Window root, int x, int y, unsigned long expected,
                  const char *name) {
    XSync(d, False);
    XImage *im = XGetImage(d, root, x, y, 1, 1, AllPlanes, ZPixmap);
    unsigned long actual = XGetPixel(im, 0, 0);
    XDestroyImage(im);
    printf("%s: actual=%06lx expected=%06lx\n", name, actual, expected);
    if (actual != expected) failed = 1;
}
static int xerror;
static int error_handler(Display *d, XErrorEvent *e) { xerror = e->error_code; return 0; }

int main(int argc, char **argv) {
    int automatic = argc > 1 && atoi(argv[1]);
    Display *d = XOpenDisplay(NULL);
    if (!d) return 2;
    Window root = DefaultRootWindow(d);
    Window w = XCreateSimpleWindow(d, root, 0, 0, 120, 120, 0, 0, 0xffffff);
    XSetWindowAttributes unmanaged = {.override_redirect = True};
    XChangeWindowAttributes(d, w, CWOverrideRedirect, &unmanaged);
    XMapWindow(d, w);
    XSync(d, False);
    GC gc = XCreateGC(d, w, 0, NULL);
    XSetForeground(d, gc, 0x009933);
    XFillRectangle(d, w, gc, 0, 0, 120, 120);
    Window pet = XCreateSimpleWindow(d, root, 0, 0, 80, 80, 0, 0, 0xff0000);
    XChangeWindowAttributes(d, pet, CWOverrideRedirect, &unmanaged);
    XMapWindow(d, pet);
    XSync(d, False);
    /* Draw a NEW underlying frame while hidden: backing-store snapshots
     * must not masquerade as a correct retained-pixel implementation. */
    XSetForeground(d, gc, 0x3366cc);
    XFillRectangle(d, w, gc, 0, 0, 120, 120);
    XRectangle r = {0, 0, 30, 80};
    XShapeCombineRectangles(d, pet, ShapeBounding, 0, 0, &r, 1, ShapeSet, Unsorted);
    pixel(d, root, 50, 50, automatic ? 0x3366cc : 0xffffff, "uncover current pixels");
    XClearArea(d, w, 90, 90, 20, 20, False);
    pixel(d, root, 100, 100, 0xffffff, "client pixel background clear");
    Pixmap bg = XCreatePixmap(d, w, 1, 1, DefaultDepth(d, DefaultScreen(d)));
    XSetForeground(d, gc, 0x123456);
    XFillRectangle(d, bg, gc, 0, 0, 1, 1);
    XSetWindowBackgroundPixmap(d, w, bg);
    XClearWindow(d, w);
    pixel(d, root, 100, 100, 0x123456, "client pixmap background clear");
    /* XComposite state belongs to the Pet connection, not this client. */
    int major, minor;
    XCompositeQueryVersion(d, &major, &minor);
    XSetErrorHandler(error_handler);
    Pixmap named = XCompositeNameWindowPixmap(d, w);
    XSync(d, False);
    if (automatic ? xerror != 0 : xerror != BadMatch) failed = 1;
    printf("redirect ownership: error=%d expected=%d\n", xerror, automatic ? 0 : BadMatch);
    if (!xerror) XFreePixmap(d, named);
    XFreePixmap(d, bg);
    XFreeGC(d, gc);
    XCloseDisplay(d);
    return failed;
}
