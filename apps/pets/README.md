# NusaShell Pets

Desktop pet overlay untuk NusaShell. Runtime ini memakai atlas WebP hatch-pet
v2 berukuran 8 kolom × 11 baris, window borderless SDL2, dan native X11 Shape
sehingga kanvas transparan di luar mascot tidak menjadi kotak hitam. WebP di
sini adalah atlas sprite, bukan animated WebP container: setiap state/arah
dipilih sebagai cell lalu dimainkan oleh renderer.

## Install via release

Pets dirilis sebagai stream terpisah (`pets-v<version>`, `pets-latest.json`,
Linux x64/arm64). Installer `install.sh` menawarkannya interaktif di Linux
(`--install-pets`, `NUSASHELL_INSTALL_PETS=1`, atau prompt `Install desktop
pet (Linux only)?`), memasang `nusashell-pets` ke `~/.local/share/nusashell-pets`
dan menyiapkan launcher `~/.local/bin/nusashell-pets` yang otomatis mengarah
ke `--assets <current>/assets/pets`. Pet menerima payload release bersama
folder `assets/` (atlas WebP + `config.json`), jadi ia tidak membutuhkan
source checkout.

## Struktur

```text
cmd/pets/main.go              # SDL event loop, lifecycle, dan wiring
internal/char/                # atlas WebP v2 + legacy image/GIF loader
internal/config/              # config.json dan default runtime
internal/direction/            # pointer -> 16 arah clockwise
internal/events/              # envelope /ws NusaShell -> pet state
internal/interaction/         # click-vs-drag policy, pure Go
internal/shape/               # alpha -> binary mask, pure Go
internal/state/               # state machine, pure Go
internal/ws/                  # reconnecting WebSocket client, pure Go
internal/app/                 # SDL2 window lifetime
internal/renderer/             # SDL2 texture upload, playback, frame shape
internal/bubble/               # two-line layout, paint, and activity dwell clock
internal/platform/            # X11 bounding/input Shape regions
assets/pets/                   # spritesheet.webp, pet.json, config.json
```

Package SDL2/X11 memakai build tag `sdl2`, jadi unit test default tidak
memerlukan header SDL2. Runtime ini ditujukan untuk Linux X11 atau XWayland;
native Wayland belum menyediakan kontrak always-on-top dan shaped input yang
dibutuhkan pet.

## Asset dan konfigurasi

`assets/pets/spritesheet.webp` adalah hasil hatch-pet v2 dari robot NusaShell.
`pet.json` menyimpan metadata paket dan `spriteVersionNumber: 2`. Reference
`agent-offline-mascot.png` dan `nusashell-mark.png` tetap dibundel sebagai
source/reference assets, bukan artwork runtime utama.

Konfigurasi minimal:

```json
{
  "name": "nusa-shell-pet",
  "spritesheet": "spritesheet.webp",
  "max_width": 192,
  "max_height": 208,
  "ws_url": "ws://127.0.0.1:9999/ws",
  "electron_path": "",
  "click_through": false,
  "shape_alpha_cutoff": 8,
  "bubble_enabled": true,
  "bubble_font": ""
}
```

`spritesheet` relatif terhadap `--assets`; path absolut juga didukung. Atlas
cell berukuran 192×208 dan tidak di-upscale. Flag `--image` tetap tersedia
sebagai fallback legacy eksplisit untuk debugging, tetapi konfigurasi paket
menggunakan atlas. Playback runtime sengaja diperlambat ke 0.5× dari timing
atlas agar perubahan pose lebih mudah terbaca; delay setiap frame dikalikan
dua tanpa mengubah artwork atau urutan state.

`bubble_enabled` menyalakan speech bubble di atas kepala pet (default `true`).
`bubble_font` memilih file TTF eksplisit; saat kosong, runtime mencari font
sistem (DejaVu, Liberation, Noto). Header memakai face Bold asli 14 px:
file `*-Bold.ttf` yang berdampingan dengan font regular diprioritaskan,
lalu pencarian font Bold sistem. Detail memakai font regular 12 px. Glyph
diraster pada 3× resolusi lalu diturunkan ke ukuran asli untuk menghaluskan
stroke. Teks tetap memakai `golang.org/x/image/font` murni Go, tanpa SDL_ttf.

Bubble berupa panel charcoal gelap dengan teks terang, border sage tipis,
pencahayaan vertikal halus, dan caret mengarah ke kepala. Kedua baris teks
rata kiri pada posisi yang sama, tanpa bullet/indikator di judul. Radius dan
caret memakai satu kontur vector ber-antialias. Panel selebar maksimal
184 px ditambatkan ke bawah area
bubble, dengan ujung caret 8 px sebelum area artwork pet; posisinya stabil
saat teks berubah. Isinya tepat dua baris: judul aktivitas dan satu detail.
Teks panjang (termasuk path tanpa spasi) dipotong dengan `…`, bukan dilipat.
Raster bubble disimpan sampai teks/font/pack berubah, bukan digambar ulang
setiap frame animasi. Upload SDL memakai straight alpha agar tepi tidak
digelapkan dua kali. X11 Shape tetap berupa mask biner: antialias raster
memperhalus kurva, tetapi bukan transparansi per-pixel compositor desktop.

Setiap isi bubble bertahan minimal **4 detik**. Event cepat digabung menjadi
update terbaru tanpa antrean replay; animasi state pet tetap segera mengikuti
event backend. Rotasi wording Thinking berlangsung setiap **5 detik** dan
juga mendapat dwell minimal 4 detik sebelum diganti oleh event lain. Kembali
ke idle menyembunyikan bubble setelah dwell yang sedang berjalan selesai.
Status yang berlangsung sangat singkat bisa tidak ditampilkan karena
digantikan event terbaru sebelum dwell berakhir.

Contoh: `Thinking…` / `Thinking it through…`, `Executing…` /
`read_file(...)`, dan `Waiting…` / pertanyaan dari event. Wording Thinking
adalah copy status lokal, bukan kutipan reasoning model: feed `/ws` tidak
membawa stream reasoning. Nama tool dibaca dari `agent.tool.started.name`;
argumen tool tidak ditampilkan. Pesan event eksplisit diprioritaskan dan
tidak dibawa ke aktivitas berikutnya.

Baris standard yang dipakai runtime dipetakan sebagai berikut:

| State pet | Baris atlas | Makna visual |
| --- | ---: | --- |
| `idle` | 0 | idle/breathing/blink |
| `running-right` | 1 | pose lari ke kanan, diputar loop selama drag |
| `running-left` | 2 | pose lari ke kiri, diputar loop selama drag |
| `thinking` | 7 | active working/processing |
| `reasoning` | 8 | focused review/reasoning |
| `done` | 3 | waving completion, diputar sekali |
| `error` | 5 | slumped/failed reaction, diputar sekali |
| `waiting` | 6 | needs-input/approval |

Baris 1 (`running-right`) dan 2 (`running-left`) adalah kontrak hatch-pet yang
dipakai sebagai pose drag: saat tombol kiri ditahan dan pet digeser secara
horizontal, pet menampilkan animasi lari menghadap arah seret. Arah dipilih
dari akumulasi gerakan horizontal dengan hysteresis 16 px supaya pose tidak
flikter saat pointer bergetar, dan dipertahankan ketika gerakan dominan
vertikal. Saat tombol dilepas, pose kembali ke state machine saat itu (state
backend bisa saja berubah selama drag berlangsung).
Baris 9–10 berisi 16 arah tatapan clockwise: `000` up, `090` screen-right,
`180` down, `270` screen-left, dengan 22.5° per cell. Saat pointer berada di
luar deadzone, pet memilih cell arah tersebut. Saat pointer meninggalkan
window atau berada di deadzone, pet kembali ke idle normal.

## Build dan test

```bash
sudo apt-get install libsdl2-dev libx11-dev libxext-dev
make build
make test
make test-race
make vet
make test-sdl2
```

Untuk build langsung:

```bash
go build -tags sdl2 -o bin/pets ./cmd/pets
```

## Run

```bash
./bin/pets --assets assets/pets \
  --electron-path /path/to/nusashell-electron
```

Tanpa `--electron-path`, klik tetap aman tetapi tidak menjalankan apa pun.
Perilaku pointer:

- klik kiri pada bagian mascot menjalankan `electron_path` setelah release;
- drag hanya aktif selama tombol kiri masih di-hold, memindahkan pet dan tidak
  menjalankan aplikasi; hover biasa tidak pernah memindahkan window;
- selama drag, pet memakai pose `running-right`/`running-left` sesuai arah
  seret horizontal; frame animasi tetap maju mengikuti delay authored (0.5×),
  tidak ikut dipercepat oleh cadence polling pointer;
- selama drag, posisi dan status tombol kiri dibaca dari root pointer X11,
  sehingga release di luar silhouette tetap menghentikan drag;
- cadence drag mengikuti refresh rate monitor yang sedang memuat pusat pet dan
  diperbarui saat pet berpindah monitor; bila refresh rate tidak tersedia,
  runtime menggunakan 60 Hz;
- posisi pointer mengubah arah tatapan secara live ketika mode interactive;
- speech bubble dua baris di atas kepala menampilkan aktivitas dan detail
  terbaru dengan dwell 4 detik; area transparan di sekitarnya tetap meneruskan
  event pointer;
- klik kanan mengganti mode interactive dan whole-window click-through;
- `SIGUSR1` juga mengganti mode, termasuk untuk mengembalikan pet dari mode
  click-through;
- `SIGINT`/`SIGTERM` menghentikan SDL dan koneksi WebSocket dengan bersih.

Pet terhubung ke endpoint root NusaShell `/ws`, bukan endpoint `/ws/pet`.
Envelope event `{ "type": "...", "payload": { ... } }` dipetakan sebagai:

| Event | State pet |
| --- | --- |
| `agent.turn.started` | thinking |
| `agent.tool.started`, `agent.compacting`, `agent.provider.retry` | reasoning |
| `agent.tool.completed`, `agent.compacted`, `agent.compaction.failed` | thinking |
| `agent.ask.pending` | waiting |
| `agent.ask.answered`, `agent.ask.cancelled` | thinking |
| `agent.auto_continue`, `agent.steer.queued`, `agent.steer.applied`, `agent.steer.cancelled` | thinking |
| `agent.turn.done` | one-shot done, lalu idle |
| `agent.turn.error` | one-shot error, lalu idle |

Implementasi transparansi memakai [SDL2 window lifecycle](https://wiki.libsdl.org/SDL2/SDL_CreateWindow)
untuk window/rendering dan [X11 Shape Extension](https://www.x.org/releases/current/doc/libXext/shapelib.pdf)
untuk bounding/input region. SDL's optional `SDL_CreateShapedWindow` tidak
dipakai karena runtime SDL laptop dapat dibangun tanpa shaped-window driver.

Shape window diperbarui ketika cell/frame berubah, sehingga area transparan
tetap meneruskan event dan silhouette animasi tidak menjadi kotak hitam. Saat
drag dimulai, runtime mem-poll root pointer X11, bukan mengandalkan pointer
capture atau event window. Ini membuat posisi pet tetap melekat pada cursor
meski pointer meninggalkan silhouette, dan state tombol kiri menghentikan drag
meski event release tidak kembali ke pet. Cadence polling memakai refresh rate
monitor aktif dari SDL; `refresh_rate: 0` berarti tidak tersedia dan memakai
fallback 60 Hz.

Implementasi ini mengacu pada [SDL_GetWindowDisplayIndex](https://wiki.libsdl.org/SDL2/SDL_GetWindowDisplayIndex),
[SDL_GetCurrentDisplayMode](https://wiki.libsdl.org/SDL2/SDL_GetCurrentDisplayMode),
dan struktur [SDL_DisplayMode](https://wiki.libsdl.org/SDL2/SDL_DisplayMode).

### Verifikasi bubble

`go test ./internal/bubble ./internal/events` menguji layout, ellipsis UTF-8,
event tool, rotasi copy, dwell, dan coalescing menggunakan waktu deterministik.
`go test -tags sdl2 ./internal/renderer -run TestBubble` membandingkan hasil
render texture SDL dengan composite yang dipakai shape native, tanpa display
server. Untuk menyimpan contact sheet, set `PETS_BUBBLE_PREVIEW_DIR` ke folder
output yang sudah ada; test menulis `bubble-preview.png` ke sana.

## Changelog

### 0.1.3

- Header 14 px bold dan detail 12 px regular, rata kiri tanpa titik status.
- Font supersampling, kontur ber-antialias, serta highlight panel yang halus.
- Perbaikan upload alpha SDL dan cache raster bubble untuk menjaga tepi serta
  biaya render saat animasi berjalan.

### 0.1.2

- Palet bubble dibalik: panel gelap dengan teks terang dan aksen sage;
  layout dua baris serta dwell 4 detik tetap sama.

### 0.1.1

- Bubble diturunkan mendekati kepala dan didesain ulang menjadi panel dua baris.
- Aktivitas tool menampilkan `Executing…` dan nama tool; Thinking memakai
  wording status bergantian.
- Dwell 4 detik dan coalescing mencegah bubble berkedip saat event datang cepat.

## Batas fase ini

- Linux X11/XWayland only; native Wayland belum didukung.
- Standard animation, one-shot states, 16 look directions, drag-run pose,
  head speech bubble, dynamic frame shape, dan WebP v2 packaging sudah aktif;
  native Wayland tetap ditunda.
- `.experimental/pets-sdl2-spike` tetap menjadi bukti awal; runtime target ada
  di folder ini dan tidak mengimpor experiment tersebut.
