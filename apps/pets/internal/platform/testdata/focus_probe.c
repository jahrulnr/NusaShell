#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <X11/Xatom.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static Window window(Display *d, const char *title, const char *class_name) {
    Window w = XCreateSimpleWindow(d, DefaultRootWindow(d), 0, 0, 40, 40, 0, 0, 0);
    XClassHint hint = {(char *)class_name, (char *)class_name};
    XSetClassHint(d, w, &hint);
    XChangeProperty(d, w, XInternAtom(d, "_NET_WM_NAME", False),
        XInternAtom(d, "UTF8_STRING", False), 8, PropModeReplace,
        (const unsigned char *)title, strlen(title));
    return w; // Deliberately unmapped, like a minimized client.
}
int main(int argc, char **argv) {
    int found = argc > 1 && atoi(argv[1]);
    Display *d = XOpenDisplay(NULL);
    if (!d) return 2;
    Window root = DefaultRootWindow(d);
    Window main = window(d, "NusaShell", "nusashell-desktop");
    Window popup = window(d, "MCP Kanban", "nusashell-desktop");
    Window wrong = window(d, "NusaShell", "unrelated-client");
    Window clients[] = {main, popup, wrong, 0x777777}; // stale WM entry
    XChangeProperty(d, root, XInternAtom(d, "_NET_CLIENT_LIST_STACKING", False),
        XA_WINDOW, 32, PropModeReplace, (unsigned char *)(clients + !found), 4 - !found);
    XSelectInput(d, root, SubstructureNotifyMask);
    XSync(d, False);
    puts("ready"); fflush(stdout);
    if (getchar() == EOF) return 3;
    XSync(d, False);
    XEvent e;
    int count = 0, bad = 0;
    while (XCheckTypedEvent(d, ClientMessage, &e)) {
        if (e.xclient.message_type != XInternAtom(d, "_NET_ACTIVE_WINDOW", False)) continue;
        count++;
        if (e.xclient.window != main || e.xclient.format != 32 || e.xclient.data.l[0] != 2) bad = 1;
    }
    printf("activation messages=%d expected=%d wrong-target=%d\n",count,found,bad);
    XDeleteProperty(d, root, XInternAtom(d, "_NET_CLIENT_LIST_STACKING", False));
    XCloseDisplay(d);
    return count != found || bad;
}
