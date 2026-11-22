# Benchmarks

Run with `make bench` (`go run ./cmd/hermes-bench`). Launches run the real
binary; the chat-switch and streaming numbers run the real views and core
headlessly against the fake instance (`internal/hermes/hermestest`).

## Latest run: Linux, 2026-11-22

Machine: 8 cores, **no GPU**: Xvfb with Mesa llvmpipe, so every pixel is
drawn on the CPU. These are worst-case numbers; on hardware GL, Metal or
Direct3D the drawing part is a fraction of this.

| Measure | p50 | p95 | Target |
|---|---|---|---|
| Launch, first run (connect screen): process start → first frame | 220 ms | 347 ms | < 300 ms |
| Launch, returning user: process start → first frame | 225 ms | 291 ms | < 300 ms |
| Launch, returning user: process start → sidebar drawn from the cache | 227 ms | 292 ms | < 300 ms |
| A frame with nothing changed (baseline) | 1.0 ms | 1.1 ms | n/a |
| Switch chats: select → the frame showing the chat | 2.3 ms | 3.3 ms | < 16 ms |
| Stream a reply: token → the frame showing it | 1.5 ms | 2.9 ms | no visible lag |

n = 15 launches per scenario (after one warm-up), 200 switches, 600 tokens.
The returning user's instance answers only after 3 s, so the sidebar can
only have come from the SQLite cache.

### Where launch time goes (trace, `HERMES_DESKTOP_TRACE=1`)

| Phase | Time from process start |
|---|---|
| Settings + instances read, cache opened, cached sidebar read (background) | ≈ 10 ms |
| Bundled fonts registered (background) | ≈ 33 ms |
| GTK ready (`App.WhenReady`) | ≈ 55–60 ms |
| Window created | ≈ 3 ms (CPU raster) to ≈ 100 ms (GL context on llvmpipe) |
| First frame presented | ≈ 110–130 ms with `MYGO_GPU=0`, ≈ 200 ms through llvmpipe GL |

App work is all off the main thread and done before GTK is ready; what
remains is the platform's start-up and the software GL path. The first-run
p95 (347 ms) is a single slow outlier of 15 on a shared machine.

### How the numbers are taken

- **Launch:** wall clock from `exec` to the line the app prints from
  `Window.OnReadyToShow` (`first-frame`), and from the view's first frame
  with sessions (`sidebar-ready`, printed in the frame after it).
- **Switch / stream:** `ui.Tester` frames with Reduce Motion on, so each
  `Frame()` is one frame rather than every animation frame until the view
  settles. Each chat keeps its own list state (measured rows, scroll).
- `MYGO_FRAME_STATS=3` shows the split of a frame: build, layout, paint
  and present (rasterisation).

### Changes these numbers drove

- The cached sidebar is read in the background before the window opens and
  handed to the first frame (`core.OpenWith`): returning-user launch to
  sidebar went from 309 ms to 227 ms (p50).
- Per-chat `ui.ListState`: switching back to a chat neither re-measures its
  rows nor loses its place.

## CI launch smoke test

The workflow launches the demo build on each runner and waits for its first
frame (`HERMES_DESKTOP_BENCH=launch`). Times are the app's own, from process
start, on cold, shared virtual machines (one run, not a benchmark):

| Runner | First frame |
|---|---|
| macOS (`macos-latest`) | 356 ms |
| Windows (`windows-latest`) | 438 ms |
| Linux (`ubuntu-latest`, `xvfb-run`, software GL) | 3.4 s |
