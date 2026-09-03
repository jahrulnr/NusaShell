# Framework Comparison — Desktop Pet Notification Gateway

Research for `apps/pets/`: a floating desktop pet overlay that receives AI
notifications from the Go backend via WebSocket, renders a state-based WebP
sprite atlas (idle, thinking, reasoning, waiting, done, error plus 16 look
directions), stays always-on-top with a
transparent, chromeless window, launches Electron or browser on click, and
ships via `curl | sh`.

Researched 2026-09-03 against official docs, GitHub issues, and 2025–2026
benchmarks. All claims are cited inline.

## Current implementation decision

The current Linux-first phase intentionally stays with the existing Go + SDL2
runtime instead of introducing Tauri: it keeps the pet beside NusaShell's Go
code, proves transparency and pointer behavior first, and defers the
cross-platform framework decision until animation and packaging requirements
are known. The runtime uses SDL for the window/render loop and the X11 Shape
extension for the alpha-derived visible/input region.

---

## TL;DR — Top recommendation

**Tauri v2** is the right choice for this specific use case, with
**Wails v3 (beta)** as the Go-native alternative if the team refuses to
introduce Rust.

Reasoning, in order of weight for *this* overlay:

1. **Transparent + undecorated + click-through is a solved, documented
   pattern in Tauri.** `transparent: true` + `decorations: false` are
   first-class config, and `WebviewWindow.setIgnoreCursorEvents()` is a
   real cross-platform API with a published *desktop-pet* click-through
   demo (irregular hitbox, no per-OS hacks). No other candidate has an
   equivalent off-the-shelf story.
2. **Smallest realistic bundle** for a web-rendered overlay (~3–8 MB
   compressed) and ~30–50 MB idle RAM — the overlay should be invisible
   to the user's machine while it waits for notifications.
3. **Mobile is real (iOS + Android stable)**, so the same pet can later
   follow the user onto mobile without a rewrite — relevant future-proofing
   for a notification companion.
4. **Active Wayland investment.** The `tao` windowing crate merged
   multiple Wayland CSD/decoration fixes in 2025 (PR #1218) and the
   decoration plugin explicitly targets GNOME Wayland/Mutter.

The one hard caveat applies to **every** framework equally (see
[Wayland reality check](#wayland-reality-check-applies-to-all)): Wayland
has no "always-on-top" protocol concept, so the pet's z-order can only be
enforced on X11/XWayland or via user-set compositor window rules (KDE).
This is a platform limitation, not a Tauri limitation.

---

## Comparison table

| Dimension | zserge/webview (webview_go) | Tauri v2 | Wails v2 / v3-beta | Neutralinojs | Flutter Desktop |
|---|---|---|---|---|---|
| **Language / backend** | Go + C lib (CGO) | Rust + JS frontend | Go + JS frontend | C++ + JS frontend | Dart + Skia engine |
| **Renderer** | OS WebView (WebKitGTK / WKWebView / WebView2) | OS WebView (WebKitGTK 4.1 / WKWebView / WebView2) | OS WebView (WebKitGTK / WKWebView / WebView2) | OS WebView (WebKit2GTK / WKWebView / WebView2) | Skia (own engine, not WebView) |
| **Bundle size (hello world)** | ~2–5 MB (Go binary + webview C lib; no engine bundled) | ~3–8 MB compressed, ~14 MB installer | v2: ~8–15 MB stripped (~50–60 MB debug); v3: ~15 MB | ~0.5 MB compressed, ~2 MB uncompressed (framework binary; app resources separate) | ~18–20 MB Linux bundle; ~98–138 MB with engine on Win/macOS |
| **Idle RAM (simple app)** | Low (~20–40 MB; WebView + Go runtime) | ~30–50 MB | ~10–30 MB baseline (Go runtime + WebView) | Low (~20–40 MB) | **High: ~96–180 MB** (Dart VM + Skia + GTK) |
| **Transparent window** | Not built-in. `webview_set_color()` was **removed**; must grab native window via `webview_get_window()` and call GTK/Cocoa/Win32 yourself | **Native**: `transparent: true` in `tauri.conf.json` | v2: Windows yes (`WebviewIsTransparent`/`WindowIsTranslucent`); Linux translucency via PR #1926; macOS limited. v3: improved | **Native**: `modes.window.transparent` flag (Windows borderless; others alpha layer) | Via `window_manager` plugin: `backgroundColor: Colors.transparent` + `titleBarStyle: hidden` |
| **Always-on-top** | Not built-in; native window handle only | **Native** config `alwaysOnTop: true` (broken on Wayland — see caveat) | Not a first-class v2 option; needs native handle | **Native** `window.setAlwaysOnTop()` (`gtk_window_set_keep_above`) | Via plugins; `setAlwaysOnTop` uses `gtk_window_set_keep_above`, compositor-dependent |
| **Click-through** | Manual: GTK `gdk_window_input_shape_combine_region` via native handle | **Native** `setIgnoreCursorEvents()` + published desktop-pet demo | Not built-in | Not built-in | Not native; needs platform channel |
| **Undecorated / frameless** | Native handle only | **Native** `decorations: false` | Native option | Native `borderless` mode + draggable region API | Native via plugin `titleBarStyle: hidden` |
| **Build toolchain** | Go + CGO + `libwebkit2gtk-4.0-dev` + C compiler | Rust (rustup/cargo) + Node + `libwebkit2gtk-4.1-dev` | Go + CGO + `libgtk-3-dev`/`libwebkit2gtk` + npm; v3 adds Taskfile | C++ toolchain (g++/make) + `neu` CLI + npm | Flutter SDK + Dart + GTK dev libs; AppImage bundling is painful |
| **Plugin / API ecosystem** | Minimal — you write native glue | **Mature**: official plugins (updater, notification, dialog, FS, shell, decoration, etc.) + cargo ecosystem | Good (v2 services/bindings); v3 expanding | Small but focused (window, os, filesystem, storage, computer) | **Huge** (pub.dev) but desktop-windowing plugins are third-party and fragmented |
| **Linux / Wayland quality** | GTK/WebKitGTK; works, Wayland limits apply; no upstream Wayland work | **Active**: `tao` Wayland CSD fixes merged 2025 (PR #1218); decoration plugin targets GNOME Wayland | v2 uses XWayland fallback; native Wayland is open issue #1420 with size/maximize bugs #2431. **v3 moves to GTK4/WebKitGTK 6.0** | GTK3/WebKit2GTK; Wayland throws errors for mouse/keyboard simulation (v6.7.0 changelog) | GTK embedder (Canonical-maintained); Wayland works but AppImage graphics-stack conflicts are a known pain |
| **Mobile (future-proofing)** | None | **iOS + Android stable** (cargo-mobile2; AAB/APK/IPA) | v2: none. **v3: experimental iOS + Android** (same Go main + frontend) | **Explicitly desktop-only** (maintainer stated no mobile, #384/#522) | **Best-in-class** (mobile-first, native iOS/Android) |
| **Community / docs** | webview/webview ~12k★, webview_go ~450★; minimal docs | **~90k★**, excellent docs, active releases (v2.11.x in 2026) | ~26k★, good docs; v2 EOL, v3 beta | ~13k★, decent docs, small team | Massive (Flutter + Google + Canonical); excellent docs |
| **`curl \| sh` deploy fit** | Excellent (single static-ish binary) | Excellent (small installer/binary) | Excellent (single Go binary) | Excellent (tiny binary + resources.neu) | Poor (large bundle + lib deps + engine) |

### Scores (1–5, 5 = best for this use case)

| Criterion | webview_go | Tauri v2 | Wails v2/v3 | Neutralinojs | Flutter |
|---|:-:|:-:|:-:|:-:|:-:|
| Simplicity (toolchain + setup) | 3 | 3 | 4 (Go-native) | 4 | 2 |
| Bundle size | 5 | 5 | 4 | 5 | 2 |
| Linux compatibility (incl. Wayland effort) | 3 | 4 | 3 | 3 | 3 |
| Ecosystem / plugins / docs | 2 | 5 | 4 | 3 | 4 |
| Future-proofing (mobile + momentum) | 1 | 5 | 3 | 1 | 5 |
| **Overlay-feature fit** (transparent + AOT + click-through) | 2 | 5 | 3 | 3 | 3 |
| **Weighted total** | **16** | **27** | **21** | **19** | **19** |

---

## Wayland reality check (applies to ALL)

A cross-cutting finding that dominates this decision: **Wayland has no
"always on top" protocol concept.** Apps cannot see or control their own
z-order on Wayland. This is confirmed across frameworks:

- **Electron** issue #50403: maintainer confirms "there is no concept of
  'always on top' in the Wayland protocol. Apps cannot see or control their
  z-order. KDE and some other environments let *users* control this with
  window rules, but it's at the compositor level and not something an app
  can access programmatically."
- **Tauri** `tao` issue #1134 and tauri #13121: `with_always_on_top` is a
  no-op on native Wayland; the documented workaround is
  `env XDG_SESSION_TYPE=x11 WAYLAND_DISPLAY=` (force XWayland).

Implications for the pet:

- The pet's always-on-top guarantee only holds on **X11 / XWayland**.
- On native Wayland (GNOME/Mutter default), the pet will behave like a
  normal window and lose foreground when another window is focused.
- **Mitigations:** (a) document X11/XWayland as the supported session and
  auto-fallback via `WAYLAND_DISPLAY=""`; (b) ship a `.desktop` file with
  `XDG_SESSION_TYPE` hints and instruct KDE users to set a compositor
  window rule; (c) keep the pet small and corner-anchored so z-order
  matters less.
- **Click-through** has a parallel Wayland wrinkle: GTK3
  `gdk_window_input_shape_combine_region` (the only way to pass events to
  *other processes'* windows) works on X11 but is unreliable on XWayland
  under GTK3; it works better on GTK4. Tauri's `setIgnoreCursorEvents`
  sidesteps the per-pixel problem by toggling the whole window's input
  region based on a hitbox, which is why the desktop-pet demo exists.

This is the single biggest platform risk for the pet and is identical
regardless of framework choice.

---

## One-paragraph summary per framework

### 1. zserge/webview (now `webview/webview_go`)
A tiny C/C++/Go library that opens an OS-native WebView window with
two-way JS bindings. The original `zserge/webview` moved to the
`webview/webview` org (tauri-apps also maintains a `zserge-webview`
mirror); `webview/webview_go` is the Go binding. It produces the smallest
possible binary (~2–5 MB) and the lowest conceptual overhead, but it is
intentionally minimal: `webview_set_color()` was **removed**, so
transparency, always-on-top, and click-through all require you to grab
the native window handle (`webview_get_window()`) and call GTK/Cocoa/Win32
yourself. It needs CGO + `libwebkit2gtk-4.0-dev`, has no plugin system, no
mobile story, and thin docs. It is the right tool if you want zero
framework and are willing to write all native window glue by hand — a
poor fit when the overlay's *entire* feature set is native-window
manipulation. Pure-Go alternatives (`glaze`, `abemedia/go-webview`) use
`purego` to avoid CGO but inherit the same "you own the native calls"
philosophy and currently report transparent/frameless windows as
unsupported on Linux.

### 2. Tauri v2
The leading Rust + OS-WebView desktop framework (~90k stars, v2.11.x
active in 2026). It ships ~3–8 MB bundles, ~30–50 MB idle RAM, and treats
transparent/undecorated/always-on-top as first-class window config.
`WebviewWindow.setIgnoreCursorEvents()` is a real cross-platform API and
there is a published desktop-pet click-through demo using a dynamic
hitbox — exactly the pet's interaction model. The official plugin set
(updater, notification, dialog, shell, decoration) is mature, iOS +
Android are stable mobile targets via `cargo-mobile2`, and the `tao`
windowing crate actively invests in Wayland (CSD/decoration fixes merged
in 2025, PR #1218). Cost: a Rust toolchain in a Go-centric monorepo, and
the universal Wayland always-on-top limitation. For an overlay whose
core requirements are transparent + always-on-top + click-through, Tauri
is the only candidate where all three are documented, supported, and
demoed.

### 3. Wails v2 / v3-beta
Go + OS-WebView, the natural "stay in Go" choice (~26k stars). v2
produces ~8–15 MB stripped binaries with ~10–30 MB baseline RAM and is
production-proven, but v2 is now end-of-life (docs flag v2.11.0 as "no
longer actively maintained") and its Wayland story is weak: it relies on
the XWayland fallback, native Wayland is an open issue (#1420), and
maximize/fullscreen/size bugs on Wayland are tracked (#2431).
Transparency exists on Windows and (via PR #1926) Linux translucency,
but always-on-top is not a first-class option and click-through is not
built in — both need native handle manipulation. **v3 (beta)** is the
strategic direction: GTK4 + WebKitGTK 6.0, a visible Taskfile build,
~15 MB binaries, ~10 MB baseline memory, and experimental iOS/Android
from the same `main.go`. v3 is the better long-term bet but is beta and
its mobile support is explicitly outside the desktop compatibility
promise. If the team insists on Go-only, target v3 and accept writing
the always-on-top/click-through native glue yourself.

### 4. Neutralinojs
A C++ + OS-WebView framework (~13k stars) explicitly positioned as a
lightweight Electron alternative: ~0.5 MB compressed binary, low RAM,
no bundled Chromium. It has a genuine `window.transparent` flag, a
native `window.setAlwaysOnTop()` (backed by `gtk_window_set_keep_above`),
and a borderless + draggable-region API — surprisingly close to the
pet's needs on paper. The catch is scope: the maintainer has stated
flatly that Neutralinojs is **desktop-only** and will never officially
support mobile (#384, #522), the plugin ecosystem is small, and v6.7.0
documents that mouse/keyboard simulation functions throw errors on
Wayland. Click-through is not exposed. It is a clean, tiny option if you
only ever care about desktop and can live without mobile future-proofing,
but it offers no advantage over Tauri for the overlay's specific feature
set and gives up mobile entirely.

### 5. Flutter Desktop
Dart + Skia (own rendering engine, not a WebView). Mobile support is
best-in-class and the ecosystem is enormous, but for a *lightweight
always-on-top overlay* it is the weakest fit: a minimal Linux app is
~18–20 MB on disk and **~96–180 MB of RAM** at idle (Flutter issue #92318
reports 96 MB for the default app; 2026 benchmarks cite 180 MB idle) —
unacceptable for a notification gateway that should sit quietly in the
corner. Transparent/always-on-top/click-through are all third-party
plugin territory (`window_manager`, `multiview_desktop`) with fragmented
support — `setAlwaysOnTop` is compositor-dependent and some plugins
silently no-op on Wayland. Linux AppImage distribution is a documented
pain (graphics-stack/driver collisions with the host). Choose Flutter
only if the pet becomes a rich animated character that justifies Skia's
rendering quality and you are willing to pay the memory cost.

---

## Sources

- webview/webview README — https://github.com/webview/webview/blob/master/README.md
- webview/webview_go — https://github.com/webview/webview_go
- `webview_set_color()` removal note — https://github.com/tauri-apps/zserge-webview (README)
- glaze (pure-Go webview, Linux transparent/frameless unsupported) — https://github.com/crgimenes/glaze ; platform matrix https://github.com/tituscheng/webviewgo/blob/main/docs/PLATFORM_MATRIX.md
- Tauri window customization (transparent, decorations) — https://v2.tauri.app/learn/window-customization/
- Tauri `setIgnoreCursorEvents` API — https://v2.tauri.app/reference/javascript/api/namespacewindow/
- Tauri desktop-pet click-through demo — https://github.com/Xinyu-Li-123/tauri-clickthrough-demo
- Tauri always-on-top broken on Wayland — https://github.com/tauri-apps/tauri/issues/13121 ; tao #1134 https://github.com/tauri-apps/tao/issues/1134
- Tauri Wayland CSD fixes — https://github.com/tauri-apps/tao/pull/1218
- Tauri mobile (Google Play AAB/APK) — https://v2.tauri.app/distribute/google-play/ ; mobile architecture https://deepwiki.com/tauri-apps/tauri/8-mobile-platform-support
- Tauri bundle/memory benchmarks 2026 — https://johal.in/tauri-20-vs-electron-30-desktop-app-bundle ; https://javascript-news.org/tauri-vs-electron-bundle-size-and-memory-footprint-in-2026
- Wails v2 Linux window source — https://github.com/wailsapp/wails/blob/4d0abeb3/v2/internal/frontend/desktop/linux/window.go
- Wails Linux transparency PR #1926 — https://github.com/wailsapp/wails/pull/1926 ; transparent background issue #1296 https://github.com/wailsapp/wails/issues/1296
- Wails Wayland support issue #1420 — https://github.com/wailsapp/wails/issues/1420 ; Wayland size bugs #2431 https://github.com/wailsapp/wails/issues/2431 ; fix PR #4047 https://github.com/wailsapp/wails/pull/4047
- Wails v3 beta (mobile, GTK4, ~15 MB / ~10 MB RAM) — https://v3.wails.io/ ; https://v3.wails.io/blog/wails-v3-beta/ ; build system https://v3.wails.io/concepts/build-system/ ; mobile https://v3.wails.io/guides/mobile/
- Wails v2 EOL note — https://wails.io/docs/v2.11.0/tutorials/helloworld/
- Neutralinojs homepage (size claims) — https://neutralino.js.org/
- Neutralinojs transparency + `setAlwaysOnTop` changelog — https://github.com/neutralinojs/neutralinojs/blob/main/CHANGELOG.md
- Neutralinojs Linux impl (GTK, `gtk_window_set_keep_above`) — https://deepwiki.com/neutralinojs/neutralinojs/9.3-linux-implementation ; window.cpp https://github.com/neutralinojs/neutralinojs/blob/main/api/window/window.cpp
- Neutralinojs no-mobile stance — https://github.com/neutralinojs/neutralinojs/issues/384 ; #522 https://github.com/neutralinojs/neutralinojs/issues/522
- Neutralinojs v6.7.0 Wayland errors — https://github.com/neutralinojs/neutralinojs/releases/tag/v6.7.0
- Flutter Linux memory 96 MB — https://github.com/flutter/flutter/issues/92318
- Flutter desktop windowing blog — https://flutter.dev/blog/desktop-windowing-apis
- Flutter `window_manager` / `multiview_desktop` (always-on-top compositor-dependent) — https://pub.dev/packages/multiview_desktop ; https://pub.dev/packages/window_manager_extra
- Flutter AppImage/Wayland distribution pain — https://www.industrialflutter.com/blogs/portability-a-case-study-in-flutter-appimage-distribution/
- Flutter vs Tauri 2026 benchmark (138 MB / 182 MB) — https://johal.in/comparison-tauri-20-vs-flutter-40-desktop-cross-platform-tauri
- Wayland no always-on-top (Electron maintainer confirmation) — https://github.com/electron/electron/issues/50403
- GTK click-through (`gdk_window_input_shape_combine_region`, X11 vs XWayland) — https://discourse.gnome.org/t/how-to-create-a-transparent-window-in-gdk-3-that-passes-mouse-events/25202 ; https://discourse.gnome.org/t/gtk3-set-pass-through-seems-not-work/20170 ; `gdk_window_set_pass_through` https://docs.gtk.org/gdk3/method.Window.set_pass_through.html
