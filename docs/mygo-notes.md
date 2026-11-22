# MyGo research notes for Hermes Desktop (native `ui`, no webview)

Researched 2026-11-22 from a fresh clone of `github.com/egoist/mygo`. **Every `file:line` below is at tag `v0.10.0` = commit `ebde9399baf2734b3361856a53f4036859973d1d`** unless marked `main@15d9bc0e`. Docs are cited from the repo's `docs/` tree, which is the source of the website (https://mygo.run). Anything I could not confirm in source is marked ⚠️ UNVERIFIED.

Update: the proof of concept and the app now build and run with Go 1.27.4 against MyGo v0.10.0, under Xvfb with llvmpipe, and cross-compile with `CGO_ENABLED=0` to macOS, Windows and Linux (amd64/arm64). See the Makefile and `docs/benchmarks.md`.

---

## 0. Version to pin

| Source | Result |
|---|---|
| `git tag` (newest first) | `v0.10.0`, `v0.9.2`, `v0.9.1`, `v0.9.0`, … (plus non-semver tags `native-libraries`, `native-sqlitecore`, `speech-analyzer-1`, which are binary-asset tags, not module versions) |
| `v0.10.0` | commit `ebde9399`, 2026-11-17 18:14 UTC, "Release 0.10.0" |
| `main` HEAD | `15d9bc0e5d36`, 2026-11-22 08:51 UTC, **127 commits ahead** of v0.10.0 (`git describe` → `v0.10.0-127-g15d9bc0e`) |
| `proxy.golang.org/.../@v/list` | 82 versions, v0.1.0 … v0.10.0 |
| `proxy.golang.org/.../@latest` | `{"Version":"v0.10.0","Hash":"ebde9399…","Ref":"refs/tags/v0.10.0"}` |
| GitHub releases | v0.10.0 (Nov 17), v0.9.2 (Nov 16), v0.9.1 (Nov 15), v0.9.0 (Nov 11). There is no CHANGELOG.md in the repo; the release notes on GitHub are the changelog. |

**Pin: `github.com/egoist/mygo v0.10.0`**, and the CLI at the same version through the `go.mod` `tool` directive (`go tool mygo`, docs/cli.md "Install").

Why:
- It is the newest tagged release, the proxy's `@latest`, and it has release notes. Its notes say "No breaking changes to the Go API" relative to 0.9.x.
- The project moves very fast (about 4 releases in 7 days, ~25 commits/day, 188 open issues, 6.7k stars, repo created 2026-09-26). A pseudo-version of `main` would pin us to an unreviewed snapshot in the middle of active refactors. One example is the "retained nodes" rewrite of the frame engine (issue #1599, stages 2–3 in open PRs #1955/#1974).
- `main` since the tag contains fixes we might want later. Plan to move to `v0.10.1` when it is tagged rather than chase `main`:
  - #2087: Tester text-system pool, so the first frame drops from about 50 ms to 11 ms per Tester.
  - #2048: Metal frees drawables of hidden windows.
  - #2170: macOS Character Viewer emoji insertion.
  - #2160: `Tester.HoldModifiers` kept across pointer events.
  - #2164: ellipsis at narrow widths.
- If one of those fixes becomes blocking, the fallback pin is the pseudo-version `v0.10.1-0.20261122085155-15d9bc0e5d36`. ⚠️ UNVERIFIED: I computed it from the commit timestamp; the proxy did not answer my `.info` query for the short hash.
- Re-check before each bump. Version 0.x carries no compatibility promise, and there has already been a breaking element-API migration (`docs/ui/migration.md`, `mygo migrate-ui`, `mygo vet`).

---

## 1. Architecture

### Rendering backends (MyGo's own renderer, not gioui/skia/wgpu)
| OS | GPU renderer | Window surface | Text engine |
|---|---|---|---|
| macOS 12+ | **Metal**, `internal/gpu/metal` (`ui/gpu_darwin.go:12-17` → `metal.New(n.Layer)`) | layer-backed `NSView`, frames from `CADisplayLink` | Core Text (`internal/text/coretext_darwin.go`) |
| Windows 10/11 (x64, arm64) | **Direct3D 11**, `internal/gpu/d3d11` (`ui/gpu_windows.go:10-19`; `d3d11.NewComposed` when a material/transparency is used) | child window of class `MyGoSurface` | DirectWrite (`internal/text/dwrite_windows.go`) |
| Linux (x64, arm64) | **OpenGL**, `internal/gpu/gl`, through libepoxy (`ui/gpu_linux.go:14-27`) | `GtkGLArea` (a `GtkDrawingArea` + CPU raster when there is no GPU) | Pango/cairo/HarfBuzz/fontconfig (`internal/text/pango_linux.go:246-262,367-374`) |
| All | CPU fallback `internal/raster` (`MYGO_GPU=0`, VMs, `Tester`) | | |

- Pipeline: `docs/architecture.md:2883-2945` (view → `ui` build/layout/paint → `internal/scene` display list → gpu/raster). There are glyph and mask atlases. Shaders are written in Go syntax (`internal/shader`, forked from Ebitengine's Kage).
- Windowing: MyGo's own backends call **AppKit** (Objective-C runtime) on macOS, **GTK 3** on Linux (`dlopen` of `libgtk-3.so.0`, `internal/linux/ffi.go:555-558`; Wayland and X11), and **Win32** on Windows (`docs/architecture.md:54-110`).
- Minimum OS: macOS 12 (`docs/README.md:211`; `minimumSystemVersion` defaults to `12.0`, `docs/configuration.md:119`), Windows 10 and 11, Linux with GTK 3 (`docs/README.md:209-215`). No 32-bit platforms (`docs/architecture.md:32-48`). A native-UI-only app needs **no WebKitGTK and no WebView2**. Build tag `mygo_nowebview` drops WebView2 from the Windows backend (`docs/ui/windows.md:297-308`).

### cgo per platform: none on desktop
`docs/architecture.md:25-31`: "Everything builds with `CGO_ENABLED=0`, so any platform can be cross-compiled from any machine." Native calls go through **purego** (`github.com/ebitengine/purego v0.11.1`, `go.mod:6`) on macOS and Linux, and through `syscall`/COM on Windows. Only iOS and Android use cgo. Runtime libraries on Linux: GTK 3, Pango, cairo, libepoxy (GL). `libsecret` is optional and loads on first use; `libayatana-appindicator3` is only needed for tray icons.

### UI thread and event loop
- The main goroutine is locked to the main OS thread in `init` (`mygo.go:75`, `runtime.LockOSThread()`). `mygo.App.Run()` (`app.go:116`) must be called from `main` and blocks in the native loop. `App.WhenReady(fn)` (`app.go:174`).
- The view function runs **on the main thread for every frame** (`docs/ui/views.md:1-10`). Every public `mygo` method is safe from any goroutine (`docs/architecture.md:211-262`). Internally:
  - `postMain` (`loop.go:91`) is a FIFO plus `Backend.Signal`, implemented as a CFRunLoopSource on macOS and `g_idle_add` on GTK.
  - `onMain` (`loop.go:96`) runs `fn` and waits for it.
  - `await` (`loop.go:121`) pumps native events while on the main thread, so blocking dialogs don't deadlock.
- **Posting state from goroutines (how Hermes keeps I/O off the UI thread):**
  ```go
  // content.go:190 — run fn on the main thread, then rebuild the view
  func (w *Window) Update(fn func())
  // content.go:159 — just request a frame (coalesced), any goroutine
  func (w *Window) Invalidate()
  // loop.go:88 — run on main and wait, no redraw
  func RunOnMain(fn func())
  // ui/frame.go:41 — Context.Invalidate "requests a frame from any goroutine"
  func (c *Context) Invalidate()
  ```
  Pattern from the docs (`docs/ui/views.md:183-213`):
  ```go
  go func() {
      items, err := fetchItems()                     // network/disk off the UI thread
      win.Update(func() { app.items, app.err = items, err })
  }()
  ```
  For streaming work tied to an element's lifetime, use `ui.Local` (`ui/api_gen.go:1709`) plus `Element.OnUnmount(cancel)` (`ui/api_gen.go:6798`) (`docs/ui/views.md:215-255`).
- Bound IPC methods run on their own goroutines. Event listeners (`OnClose`, `OnFocus`, …) run on the main thread (`docs/architecture.md:255-259`).
- Idle cost: a settled window asks for no frames and arms no timer. The counter example idles at about 0.005% of a core on macOS (`docs/architecture.md:10-24`).

---

## 2. The `ui` package API

### Model: immediate-mode API, rebuilt every frame
The model is Dear-ImGui-like immediate mode with a fluent builder. You write a `func(c *ui.Context)` that rebuilds the whole tree each frame. Controls report their input inline: `if ui.Button(c,"Save").Clicked() {…}`. Widget state lives in the engine, keyed by position or `c.Key(id)` (`ui/frame.go:49`), or in app-owned structs such as `ListState`. Layout is cached across frames. Each frame still walks the whole tree (about 0.8 µs per element, issue #1599). `ui.Element` is a 16-byte handle that is only valid during one build pass (`docs/ui/views.md:12-20`, `docs/architecture.md:2885-2897`). `mygo vet` finds handles stored past their pass.

```go
// ui/api_gen.go:3250
func View(view func(c *Context)) *Content           // mygo.WindowOptions.Content
// ui/api_gen.go: constructors (all take c *Context)
func Column(c *Context) Element                       // :1256
func Row(c *Context) Element                          // :2195
func Box(c *Context) Element                          // :1010
func Grid(c *Context) Element                         // :1604  (CSS-grid-like: ColumnTracks/RowTracks/ColumnSpan…)
func Text(c *Context, s string) Element               // :2748
func RichText(c *Context, spans ...Span) Element      // :2182
func Image(c *Context, src ImageSource) Element       // :1666
func Icon(c *Context, s *SVG) Element                 // :1652
func Scroll(c *Context) Element                       // :2208 (also ScrollHorizontal :2234, ScrollBoth :2222)
func Spacer(c *Context) Element                       // :2466
func Button(c *Context, label string) Element         // :1045
func TextInput(c *Context, value *string) Element     // :2810
func TextArea(c *Context, value *string) Element      // :2760
// plus Split, Sidebar/SidebarItem/SidebarSection, Tabs, Popover, Modal, Tooltip, Toast, Select,
// Combobox, Switch, Checkbox, Table, Tree, Outline, … (full list: grep '^func [A-Z]\w*(c \*Context' ui/api_gen.go)
```
`Element.Children(fn func())` (`ui/frame.go:122`) nests children.

### Layout: flexbox, plus grid and absolute positioning
- Flex: `Grow` (`ui/api_gen.go:5549`), `Shrink` (:8008), `Basis` (:3949), `Gap/GapX/GapY` (:5477), `Padding(v ...float32)` (:6874), `Margin` (:6259), `AlignItems` (:3599), `AlignSelf` (:3612), `Justify` (:5935), `Wrap` (:8838), `Fill/FillWidth/FillHeight` (:5187), `Size/Width/Height/Min*/Max*` (:8031, :8789, …), `AspectRatio` (:3737).
- Grid: `Grid(c)` plus `ColumnTracks`/`RowTracks`/`ColumnSpan`/`RowSpan` (:4489, :7663, :4465, :7640).
- Stacking and overlays: `Absolute()` (:3439) with `Top/Left/Right/Bottom` (+`Percent`), `ZIndex` (:8900), `Overlay`, `Popover`, `Attach/AttachTo` (:3754) for anchored popups. There is no separate `Stack` constructor; use `Box` with absolute children.
- Measurement: `Element.Bounds()` (:4122) and `LayoutBounds()` (:6124) return the **previous frame's** boxes. `Context.MeasureText(width, spans...)` (:451), `Context.Size()` (:674).

### Styling
```go
func (e Element) Background(c Color) Element                                   // :3920
func (e Element) Radius(r ...float32) Element                                   // :7340  (1 or 4 corners)
func (e Element) Border(width float32, c Color) Element                         // :4001  (+BorderColor, BorderStyle, BorderLinearGradient, BorderConicGradient)
func (e Element) Shadow(x, y, blur, spread float32, c Color) Element            // :7982  (CSS box-shadow; stackable? ⚠️ UNVERIFIED)
func (e Element) InsetShadow(x, y, blur, spread float32, c Color) Element       // :5853
func (e Element) LinearGradient(g LinearGradient) Element                       // :6193
func (e Element) RadialGradient(g RadialGradient) Element                       // :7328
func (e Element) ConicGradient(g ConicGradient) Element                         // :4530
func (e Element) Opacity(o float32) Element                                     // :6818  (group opacity, composited like CSS)
func (e Element) Blur(sigma float32) Element                                    // :3977  (+Material/Effect for backdrop blur)
// colours: ui/color.go
func RGB(r, g, b uint8) Color; func RGBA(r, g, b uint8, alpha float32) Color; func Hex(s string) Color  // :26,:29,:33
```
- `RadialGradient{Stops []GradientStop; Center Point; Radius float32; Oklab bool}` (`ui/element.go:885-899`). It is **circles only** (issue #1443, still open; the ellipse PR #1448 is not in v0.10.0 or main). That is fine for round orbs.
- Gradients can mix in Oklab. Masks: `MaskLinearGradient`/`MaskRadialGradient` (:6340, :6353). Transforms: `Scale/Rotate/Translate/Skew/Perspective` (:7704 …).
- Theme: `c.Theme() *Theme` (:685) with fields such as `Background`, `Surface`, `Border`, `Text`, `TextMuted`, `Accent` (`ui/theme.go:17-31`). It follows the system light/dark appearance automatically. `c.SetTheme` (:608) sets a custom theme, and `ui.Themed` (:2893) applies one to a subtree.

### Text
- System engines: Core Text, DirectWrite, Pango, with system fallback for emoji and other scripts (`docs/ui/text.md:281-290`).
- `Font(family string)` (:5425) takes CSS-like lists `"Inter, Noto Sans JP"` and `"monospace"`. Also `FontSize` (:5454), `FontWeight(w int)` (:5465), `Bold` (:3988), `Italic` (:5910), `LineHeight` (:6181), `LetterSpacing` (:6157), `FontFeatures` (:5442), `TextAlign` (:8289).
- **Custom fonts:** `func RegisterFont(data []byte, family string) error` (`ui/window.go:41`) takes TTF/OTF/TTC, typically `//go:embed`. Weights and styles are matched among the faces registered under a family (`docs/ui/text.md:288-306`). `FontFamilies()` (`ui/window.go:70`). Known bug, closed: #2137, Windows `FontWeight(500)` picked 600 for Segoe UI Variable.
- **Ellipsis and wrapping:** text wraps by default. `MaxLines(n)` (:6436), `SingleLine()` (:8020), `NoWrap()` (:6630), `Ellipsis(s)` (:5095; default "…"), `TruncateMiddle()` (:8673), `TruncateStart()` (:8689).
- **Selection:** `Selectable()` (:7891). On a container it makes one shared selection across its `Text`/`RichText` descendants, with Copy and a context menu. `SelectedTexts` (:7917), `SetTextSelection` (:7956).
- **Rich spans:** `ui.Span{Text, Font, Size, Weight, Italic, Color, Underline, Strikethrough, Background, Radius, PaddingX, PaddingY, BaselineShift, …}` (`ui/richtext.go:15-40+`). `Background`+`Radius` gives "inline code" chips. Links inside sentences: `docs/ui/text.md:242`.

### Images and SVG icons
`ui.Image(c, src ImageSource)` (:1666). Sources come from `DecodeImage(data []byte)` (`ui/widgets.go:572`; PNG/JPEG/GIF/APNG…) and `NewBitmap(image.Image)` (`ui/bitmap.go:73`). SVG: `ParseSVG(data []byte) (*SVG, error)` (`ui/svg.go:58`) and `ui.Icon(c, svg)` (:1652), tinted with the text colour; `Painter.Icon` (`ui/svg.go:318`). Network images with caching and fade-in: `ui/netimage` `Image(c, url, ...Options)` (`ui/netimage/netimage.go:53`), useful for avatars.

### Scrolling, clipping, hit-testing, hover, cursor
- `ui.Scroll(c)` builds **all** children; use `List` for long collections. `ScrollState{X, Y, MaxX, MaxY}` (`ui/scroll.go:5-16`), `ScrollState.ScrollTo(x, y, Animation)` (`ui/scroll.go:41`), `Element.TrackScroll(*ScrollState)` (:8547), `ScrollIntoView` (:7727), `Sticky` (:8208), `ScrollSnap` (:7762), scrollbar styling (:7837).
- `Clip()` (:4351) clips children (rounded by the element's radius ⚠️ UNVERIFIED), plus `ClipInset` and `Painter.Clip(r, radius, fn)` (`ui/paint.go:1891`).
- `Hovered() bool` (:5710). Calling it during build makes hover changes rebuild the view; reading it inside `Draw` makes hover repaint-only (`docs/ui/performance.md:55-70`). `Pressed()` (:7177), `Clicked()` (:4326), `RightClicked()` (:7448), `DoubleClicked()` (:4826).
- `HitTest(fn func(x, y float32, r Rect) bool)` (:5689) for custom shapes. `PassThrough()` (:6930), `IgnoreInput` (:5762).
- `Cursor(c Cursor)` (:4646) with ~31 cursors (`CursorPointer`, `CursorText`, `CursorGrab`, …) (`ui/keys.go:280+`).
- `DragWindow()` (:4943) for custom title bars.
- Custom painting: `Element.Draw(fn func(p *Painter, r Rect))` (:5015). `Painter` has `Fill`, `FillRadii`, `FillGradient`, `FillRadialGradient`, `Stroke`, `Shadow`, `Line`, `Text`, `Image`, `Icon`, `FillPath`, `StrokePath`, `Clip`, `Transform` (`ui/paint.go:1666-1914`, `ui/path.go:1006+`), and GPU shaders through `ui/shader`.

---

## 3. Virtualised lists: built in

```go
// ui/list_builder.go:48
func List(c *Context, s *ListState, n int, row ...func(int)) Element
```
- `List` builds only the visible rows plus a few beyond them. Rows can have **variable heights**: they are measured as they show and the rest are estimated. It "keeps its place by a row rather than by an offset" (`docs/ui/list.md:1-15, 52-92`).
- `ListState` (`ui/list.go:38-231`):
  - `Key func(row int) any` (:47) gives stable identity.
  - `KeepOffset` (:53).
  - `FollowEnd bool` (:61) gives chat-style stick-to-bottom until the user scrolls up; `FollowAnimation *Animation` (:68) makes it glide.
  - `MoveAnimation` (:78), `Selected *int` (:91), `Label` (:112), `Header` (:117) for sticky section headers.
  - `RowHeight float32` (:200) gives fixed dense rows.
  - `EndReached func(last int)` (:228) with `NearEnd int` (:231) for paging.
- Methods: `ScrollTo(row, align)` (:381), `ScrollIntoView(row)` (:388), `ScrollToEnd()` (:395), `HoldPlace()` (:429), `Visible() (first, last int)` (:436), `AtEnd() bool` (:446).
- Measured cost: visible rows of a million-row list take 14.5 µs/frame (`docs/ui/performance.md:7-14`). One label changing among 3,000 rows takes 0.2 ms in a `List` versus 11 ms in a `Scroll` (`docs/ui/performance.md:38-50`).
- Chat pattern straight from the docs (`docs/ui/markdown.md:112-130`, `docs/ui/list.md:262-288`):
  ```go
  app.chat.Key = func(i int) any { return app.messages[i].ID }
  app.chat.FollowEnd = true
  app.chat.FollowAnimation = &ui.Smooth
  ui.List(c, &app.chat, len(app.messages), func(i int) { message(c, app.messages[i]) }).
      Grow(1).Gap(16).Justify(ui.End)
  ```
- **Caveat for chat, issue #1471 (open):** the unit of virtualisation is the row, i.e. a whole message. (a) A long Markdown reply is built whole once any part of it is visible; the issue measured about 10 MB of retained heap for a 25 KB reply. (b) A text **selection across rows is lost** when the rows holding its ends scroll out (`ui/textselection.go`). The proposed fix #1472 was closed unmerged and is being re-done. Also `GridView` (:1630) exists, but there is no masonry list (#2028).

---

## 4. Text input
- Single-line: `TextInput(c, *string)` (:2810). Multi-line: `TextArea(c, *string)` (:2760) with `Lines(min, max int)` (:6207) for an auto-growing composer. Large-document variants: `TextInputBuffer`/`TextAreaBuffer(c, *TextBuffer)` (:2837, :2787; rope-backed). Unstyled "Base" variants: `TextInputBase`, `TextAreaBase`.
- `Placeholder(s)` (:6969), `PlaceholderColor` (:6982), `Password()` (:6944), `MaxLength` (:6424), `ReadOnly` (:7378), `Submitted()` (:8245), `Changed()` (:4261), `OnPaste(fn)` (:6725), `TextRanges` for colouring mentions (:8356), `SearchField` (:2254), `TokenField` (:3040).
- **Chat composer keys:** `OnTextKey(fn func(k TextKey) bool)` (:6767) sees Enter, Esc, arrows and Cmd/Ctrl chords before the input does. The documented composer (`docs/ui/text-input.md:63-91`):
  ```go
  ui.TextArea(c, &app.draft).Lines(1, 8).OnTextKey(func(k ui.TextKey) bool {
      if k.Key == ui.KeyEnter && k.Mods == 0 { app.send(k.Text()); k.SetText(""); return true }
      return false // Shift+Enter inserts a newline
  })
  ```
- **IME:** native per platform, through NSTextInputClient on macOS, GtkIMMulticontext on Linux, and IMM32 on Windows (`docs/architecture.md:7242`). Keys typed while composing do not submit or trigger shortcuts (`docs/ui/text-input.md:137-150`). Tests can use `Tester.Compose` (`ui/headless.go:1129`). Bugs fixed recently: Hangul backspace #2098 (closed), Character Viewer emoji #2170 (on main after the tag).
- **Focus:** `Focus(...FocusOptions)` (:5275), `AutoFocus()` (:3867), `Focused()` (:5411), `FocusWithin()` (:5388), `Focusable` (:5399), `FocusGroup` (:5294). `Element.FocusBind` binds focus to app data (`ui/focus_binding.go:22`); `ui.Handle` provides persistent identity (`docs/ui/input.md:204-353`).
- **Clipboard:** `c.ReadClipboard()`/`c.WriteClipboard(s)` (:531, :813), `c.WriteClipboardThen` (:834), `Services.ReadClipboardData/WriteClipboardData` for multiple formats. App-level `mygo.Clipboard` (`modules.go:230`; `WriteText` :239, `WriteHTML` :251, `WriteImage` :263). The Edit-menu roles (cut, copy, paste, undo) act on the focused input (`docs/ui/input.md:397-406`).

---

## 5. Rich text / Markdown: built in, designed for LLM chat
Package **`github.com/egoist/mygo/ui/markdown`** has its own parser (`ui/markdown/parser`, not goldmark). It supports CommonMark, GFM tables, task lists, strikethrough, autolinks, footnotes and math (`docs/ui/markdown.md:1-20`).
```go
// ui/markdown/view.go
type Options struct { Theme *Theme; Extensions parser.Extensions; OnLink func(url string); LinkMenu func(*ui.Menu, string);
    LinkIcon …; LoadImage func(url string) (ui.ImageSource, error); CodeBlock func(*ui.Context, *CodeBlockView);
    Code map[string]func(*ui.Context, *CodeBlockView); WrapCode bool; Math …; InlineMath …; HTML …;
    Block func(c *ui.Context, d *parser.Document, i int) bool; Reveal *Reveal; ShareSelection bool; … }   // :17
func View(c *ui.Context, src string, o *Options) ui.Element     // :154  finished doc, reparses only what changed
func Stream(c *ui.Context, src string, o *Options) ui.Element   // :171  token streaming, healed partial syntax, fade-in
func Revealed(el ui.Element) bool                               // :202  all text shown (show timestamp/actions after)
```
- Streaming: appending text reparses only the open tail block. `**bol` renders bold before it closes, and an unclosed fence renders as code. `Reveal{Unit: ByWord|ByLine|…, CatchUp, Fade}` paces bursts (`docs/ui/markdown.md:26-110`).
- Code blocks are syntax-highlighted with the code editor's tokenizers, with a language label and a Copy button. You can replace them with `CodeBlock`/`DefaultCodeBlock` (`ui/markdown/code.go:13, 97`; `docs/ui/markdown.md:284-333`).
- Links: http, https and mailto open in the browser, and other schemes are inert; `OnLink` overrides this (`docs/ui/markdown.md:148-160`). Raw HTML renders as text, which keeps it safe (`docs/ui/markdown.md:334-365`). main@9b0ae9ef adds GitHub tagfilter-style escaping.
- Selection and copy work as plain text, HTML or Markdown ("Copy as Markdown", Shift+Cmd+C). `ShareSelection` hands the selection to an outer `Selectable` so it can span messages (`docs/ui/markdown.md:132-147`); see the #1471 caveat in §3.
- Theming: `markdown.Theme` (`ui/markdown/theme.go:16`) sets fonts, heading scale, code colours, list indent, quote bar and so on, in ems (`docs/ui/markdown.md:366-410`).
- Perf for a 50 KB reply: full parse 0.3 ms, appending a token 2 µs, a steady frame 1.3 ms, a streaming frame 0.8 ms (`docs/ui/markdown.md:464-480`).
- **If we ever need our own renderer:** don't use goldmark. Use `markdown.Options.Block` to override block types one at a time, or walk `parser.Parse(src)` (`ui/markdown/parser/document.go:341`) → `Document.Block(i)` and `Inline`s. Map each to `ui` as follows:
  - paragraph → `ui.RichText(c, spans...)`
  - inline code → `Span{Font:"monospace", Background, Radius, PaddingX}`
  - link → span plus a click handler (`docs/ui/text.md:242`)
  - list → `Row{marker, Column{items}}`
  - code block → `Box.Background.Radius` around `ScrollHorizontal` with monospace `Text`, or `ui/editor` read-only
  - heading → `Text.FontSize/FontWeight`
  - one long message → split into several `List` rows to sidestep #1471

---

## 6. Animations
- Frames are on demand only. A frame runs after input, `Update`, `Invalidate`, `After`, or while something animates, at the display's refresh rate (CADisplayLink, GTK frame clock, DWM). There are **no frames while the window is hidden** (`docs/ui/motion.md:543-600`; `docs/ui/performance.md:29-35`).
- APIs:
  ```go
  func (c *Context) AnimationFrame()            // ui/api_gen.go:304 — next vsync, REBUILDS the view
  func (c *Context) After(d time.Duration)      // :292 — one frame after d
  func (c *Context) Now() time.Time             // :481 — frame time (Tester-controllable)
  func (e Element) Animate(key any, target float32, d time.Duration) float32   // :3627
  func (e Element) AnimateTo(key any, target float32, a Animation) float32     // :3701 (springs, Delay for stagger)
  func (e Element) Spring(key any, target float32, ...SpringOptions) float32   // :8126
  func (e Element) Keyframes(key any, frames []Keyframe, ...KeyframeOptions) float32 // :6052
  func (e Element) Timeline(key any, o TimelineOptions) *Timeline              // :8438 (stagger labels)
  func (e Element) Loop(key any, period time.Duration, ease Easing) float32    // :6247 — calls c.AnimationFrame() (ui/widgets.go:71-86) → rebuilds each frame
  func (e Element) AnimatePose(p Pose, a Animation) Element                    // :3681 — translate/scale/rotate/fade, PAINT-ONLY frames
  // Painter (paint-only: "the next frame paints … again without building the view")
  func (p *Painter) Now() time.Time                 // ui/paint.go:137
  func (p *Painter) AnimationFrame()                // ui/paint.go:158
  func (p *Painter) AnimationFrameRate(fps float32) // ui/paint.go:173 — throttle (e.g. 30 fps) to save power
  func (p *Painter) After(d time.Duration)          // ui/paint.go:184
  ```
- **Recommended approach for our loaders, cheapest first:** use an `Element.Draw` callback that computes from `p.Now()` and calls `p.AnimationFrameRate(30)` or `p.AnimationFrame()`. The view never rebuilds; only that element repaints, and out-of-view elements request nothing (`ui/paint.go:150-158`). This is exactly how `plugins/loading` animates its 29 loaders (`plugins/loading/loading.go:184-201`).
  - **4×4 marching grid:** one `Box.Size(s, s).Draw(...)`. For each cell `(r, c)` compute `alpha = f((p.Now()-start)/period - (r*4+c)*stagger)` and `p.Fill(cellRect, color with alpha, radius)`. `plugins/loading.Blocks` (`spinners.go:91`) is a similar grid sweep that can serve as a reference.
  - **Breathing orb, 1→1.07:** in `Draw`, set `s := 1 + 0.035*(1-cos(2π·t/T))` and draw a `FillRadialGradient` scaled by `s`. Alternatively, toggle `AnimatePose(ui.Pose{Scale: 1.07}, anim)` targets, but a pose only moves when its target changes, so a loop needs re-targeting.
  - When the animation stops (the reply arrives), stop calling `AnimationFrame` and the window sleeps. Respect `c.Preferences().ReduceMotion` (`ui/api_gen.go:519`); `Loop` deliberately keeps running under reduced motion (`docs/ui/motion.md:543-550`).
- Avoid `Element.Loop` for always-on indicators in a large view, because each frame **rebuilds the whole view** (`ui/widgets.go:82`).

---

## 7. Menus: real OS menus, per platform
- Model, in the root package: `MenuItem{ID, Label, Type, Role, Accelerator string, Disabled, Hidden, Checked, Submenu []*MenuItem, Click func(*MenuItem, *Window), Badge, Subtitle, Icon …}` (`menu.go:124-179`). `NewMenu` (`menu.go:225`), `App.SetMenu` (`app.go:448`; the app menu bar on macOS), `Window.SetMenu` (`window.go:1544`; per-window bars on Linux and Windows), and live updates via `SetLabel`/`SetChecked`/`SetEnabled`/`SetAccelerator` (`menu.go:340-401`). The menu bar is **real** on every platform:
  - **macOS:** `NSMenu` built through the Objective-C runtime (`internal/darwin/menu.go:297`). A default menu bar with App/File/Edit/View/Window is installed until you set your own; keep `RoleAppMenu`/`RoleEditMenu` so Cmd+Q and copy/paste keep working (`docs/menus.md:243-250`).
  - **Windows:** Win32 `HMENU` (`CreateMenu`/`CreatePopupMenu`, `internal/windows/menu.go:98-100`; `SetMenu`, :248). Items are `MFT_OWNERDRAW` (`internal/windows/menu.go:202`; `menudraw.go:20`) so they look like Windows 11 menus, follow dark mode and use Acrylic on 22H2 (`docs/menus.md:257-266`). It is still a genuine HMENU and menu loop.
  - **Linux:** GTK 3 `GtkMenuBar`/`GtkMenu` (`internal/linux/ffi.go:757-758`).
- **Context menus:**
  - `mygo.Menu.Popup(win)`/`PopupAt(win, x, y)` (`menu.go:312, 319`) and `ui`'s `Element.ContextMenu(build func(*ui.Menu))` (`ui/api_gen.go:4591`) both default to **OS popup menus**. The path is `ui/menu.go:854` `showMenu` → `ui/window.go:1089` → `content.go:90-99` → `platform.Backend.PopupMenu` (`internal/platform/platform.go:81-84`). The per-OS implementations:
    - macOS: `-[NSMenu popUpMenuPositioningItem:atLocation:inView:]` (`internal/darwin/menu.go:1316, 1357`).
    - Windows: `TrackPopupMenuEx` (`internal/windows/menu.go:530, 561-580`).
    - Linux: `gtk_menu_popup_at_pointer`/`_at_rect` (`internal/linux/menu.go:500`; `ffi.go:771-772`).
  - Self-drawn alternatives: `c.SetMenuPresentation(ui.PopoverMenus)` (`ui/menupresentation.go:22-35`; `ui/api_gen.go:597`) draws all of the window's popup menus in-window with the theme. `Element.PopoverMenu`/`PopoverMenuButton` (:7045, :2011) are always drawn by MyGo. `ui.MenuButton` (:1776) uses the system menu.
  - `ui.Menu` items: `Item(label).Chosen()`, `.Checked`, `.Radio`, `.Disabled`, `.Shortcut(mods, key)` (display only), `.Icon`, `.Subtitle`, `.ID`, `Separator()`, `Submenu()`, `EditItems()` (`docs/ui/context-menu.md:35-75`).
- **Accelerators:** strings such as `"CmdOrCtrl+Shift+N"`. Modifiers are `CmdOrCtrl`, `Cmd`/`Super`/`Win`, `Ctrl`, `Alt`/`Option`, `Shift`. Keys include letters, digits, `,` and `/`, `F1`–`F24`, `Esc`, arrows and so on (`docs/menus.md:546-580`; parser in `internal/accelerator`). The OS shows them in the menu. Menu shortcuts keep working while a native-UI window has focus, and a key a menu item takes **goes to the menu, not the view** (`docs/ui/input.md:397-406, 465-467`).
- Caveats:
  - Linux and Windows have no menu bar by default.
  - On Linux a `TitleBarHidden` window hides its menu bar; Alt or F10 shows it (`docs/menus.md:278-285`).
  - On **Windows a window with a Mica/Acrylic material has no menu bar**; Alt or F10 opens the menus as a popup (`docs/windows.md:56`; `docs/architecture.md:7171`).
  - Tray menus on Linux need AppIndicator.

---

## 8. Keyboard
- **Window-level shortcuts, in the view:**
  ```go
  func (c *Context) Shortcut(mods Modifiers, key Key) bool      // ui/api_gen.go:644 — exact modifiers, anywhere in window
  func (c *Context) ShortcutRepeated() bool                     // :665
  func (c *Context) OnWindowShortcut(mods Modifiers, key Key, fn func()) // ui/actions.go:70
  func (e Element) Shortcut(mods Modifiers, key Key) bool       // :7995 — only while focus is inside e
  var Cmd Modifiers  // ui/keys.go:255-261: Super (Command) on darwin, Ctrl elsewhere
  // keys: ui.Key0…Key9 (ui/keys.go:55-64, consecutive), KeyN, KeyK, KeyComma (:45), KeyEscape (:17)
  ```
  This covers Cmd/Ctrl+1–9 via `ui.Key0+ui.Key(i)`, plus N, K, comma and Esc. Non-Latin layouts map to US key positions when Cmd/Ctrl/Alt is held (`docs/ui/input.md:404-410`).
- **Raw down/up and modifier-only events: YES.**
  - `c.Modifiers() Modifiers` (`ui/api_gen.go:472`) returns the modifiers held now, "as the last key, pointer or **modifier** event said". The view **redraws when they change**, so no polling is needed.
  - `c.KeysDown() []Key` (:428) and `c.KeyDown(k)` (:409) return non-modifier keys held, in press order.
  - `Element.HandleInput(func(InputEvent) bool)` (:5572) and `ObserveInput(func(InputEvent))` (:6685) receive `InputKeyDown`/`InputKeyUp` (`ui/handler.go:24-27`). These include **modifiers pressed and released on their own**, with `Key == KeyUnknown`, `Code` = `CodeControlLeft` etc. (HID usage IDs), and `Mods` (`docs/ui/input.md:718-772`).
  - Each backend reports modifier-only changes as `platform.ModifiersChanged` (`internal/platform/surface.go:463-465`), handled in `ui/input.go:146`:
    - macOS: `flagsChanged:` (`internal/darwin/surface.go:1422-1426`, :1058-1078).
    - Windows: `WM_KEYDOWN`/`WM_KEYUP` of a modifier VK (`internal/windows/surface.go:983-1012`).
    - Linux: GDK modifier keyvals (`internal/linux/surface.go:1068-1081`).
- **Focus and blur:**
  - `c.WindowActive() bool` (:787). The window releases all held keys when it loses the keyboard (`docs/ui/input.md:465-475`; per OS: `docs/architecture.md:7241`, e.g. `WM_KILLFOCUS`, resigning key window).
  - `Window.OnFocus/OnBlur(fn)` (`window.go:2108, 2111`) run on the main thread. `c.WindowHidden()` (:805).
- **"Cmd/Ctrl held 300 ms with no other key":** this can be done fully in the view with no timers of our own. See `holdTracker` in the PoC:
  ```go
  show, wait := app.hold.update(c.Now(), c.Modifiers(), len(c.KeysDown()))
  if wait > 0 { c.After(wait) }       // one wake-up at 300 ms
  if !c.WindowActive() { app.hold = holdTracker{} }
  ```
  - A chord such as Cmd+K shows up as `KeysDown() > 0` in the frame its key-down triggers, so it spoils the hold until Cmd is released.
  - **Gotcha:** an accelerator claimed by a **menu item** never reaches the window (`docs/ui/input.md:465-467`). Call `hold.cancel()` from those menu `Click` handlers. Alternatively, handle Cmd+N/K/, as view shortcuts and leave the menu item's `Accelerator` only for display. ⚠️ UNVERIFIED: whether a menu item with an Accelerator **and** a matching `c.Shortcut` double-fires. Avoid having both.
  - macOS quirk: a key released while Command is held is delivered via a local monitor (`docs/architecture.md:7241`).
- **Global shortcuts** (system-wide): `mygo.GlobalShortcut.Register(acc string, fn func()) error` (`modules.go:737`). It uses Carbon hot keys on macOS, `RegisterHotKey` on Windows, and on Linux X11 `XGrabKey` or the Wayland XDG GlobalShortcuts portal (`docs/architecture.md:7165`).

---

## 9. Native integration
| Need | API (root package `mygo` unless noted) | Notes |
|---|---|---|
| File dialogs | `Dialog.Open(OpenDialogOptions) ([]string, error)` (`dialog.go:141`), `Dialog.Save(SaveDialogOptions)` (:217), `Dialog.SaveCopy` (:178) | Blocking. Safe from any goroutine; on the main thread they pump events. |
| Message dialogs | `Dialog.Message(MessageOptions) (MessageResult, error)` (:247), `Dialog.Error(title, content)` (:295); in-window `ui.AlertDialog` (`ui/api_gen.go:941`) | |
| Notifications | `NewNotification(NotificationOptions).Show()` (`modules.go:1165, 1197`) | **macOS: packaged (signed bundle) apps only** (`modules.go:1129`; `docs/architecture.md:7166`), so not under plain `go run`. Linux uses org.freedesktop.Notifications; Windows uses notification-area balloons, not modern toasts. |
| Light/dark detection and changes | `Theme.IsDark()` (`modules.go:449`), `Theme.OnUpdated(fn) (off func())` (:682), `Theme.SetSource` (:561); in-view `c.Theme()` follows automatically; `c.Preferences()` (accent, reduce motion, contrast) | Linux reads the settings portal `color-scheme` (`docs/architecture.md:7178`). |
| Translucent sidebar | `WindowOptions.Vibrancy` (`window.go:163`) = `VibrancySidebar` (`vibrancy.go:25`) on macOS, `VibrancyMica`/`VibrancyAcrylic`/`VibrancyTabbed` (`vibrancy.go:65-69`) on Windows 11 22H2+; plus `c.Vibrancy()` (`ui/api_gen.go:766`) and `c.Root().Background(ui.Transparent)` | **Linux: ignored.** Windows: a material means no menu bar. Pattern in `docs/ui/windows.md:122-149`; `examples/vibrancy`. |
| Custom titlebar, traffic-light inset | `WindowOptions.TitleBarStyle = TitleBarHidden \| TitleBarHiddenInset` (`window.go:29-35, 112`), `TrafficLightPosition *Point` (:116), `TitleBarHeight` (:122), `Frameless` (:102); in view `c.TitleBar()` (`ui/api_gen.go:706`) gives `.Left/.Right/.Height/.Bottom`, and `Element.DragWindow()` | `docs/ui/windows.md:25-74`. Recent Linux/KDE hidden-titlebar fixes are on main (#2153, #2189). |
| Keychain / credentials | **Built in:** `Secrets.Set(key string, value []byte) error` (`secrets.go:85`), `Get` (:97), `Delete` (:115), `Secrets.Service(name)` (keytar-compatible) | macOS Keychain (`SecItem`); Windows Credential Manager (`CredWriteW`); Linux libsecret ≥ 0.19 over Secret Service on the session bus, returning **`ErrUnsupported` if there is no libsecret, no session bus or no Secret Service** (`docs/architecture.md:7170`). We don't need zalando/go-keyring. If we used it, it has the same Linux requirement (a Secret Service daemon such as gnome-keyring or KWallet on D-Bus). Plan an encrypted-file fallback for headless or minimal Linux. |
| Open URL | `Shell.OpenExternal(url)` (`modules.go:35`); in view `c.OpenURL(url)` / `c.OpenURLThen` (`ui/api_gen.go:492, 509`) | |
| Sounds | `Shell.Beep()` (`modules.go:221`); package `audio`: `DecodeSound` (`audio/audio.go:146`), `NewPlayer` (:244), `OpenStream` (:225); decodes MP3, OGG Vorbis, FLAC and AAC (`go.mod`) | |
| Single instance, deep links, tray, auto-update | `RequestSingleInstanceLock` (`single_instance.go`), `deeplink.go`, `NewTray`, `plugins/updater` | Useful extras. |

---

## 10. Testing: headless view testing is built in
- `ui.NewTester(view func(*Context), w, h int) *Tester` (`ui/api_gen.go:1859`; type in `ui/headless.go:276`) renders frames **in memory on the CPU**, with no window and no main-thread requirement (`docs/ui/testing.md:1-37`).
  - Input: `Click(text)` (:754), `ClickAt`, `RightClick`, `Menu`/`ChooseMenuItem` (:814/:832), `Type` (:1108), `Key(mods, key)` (:1056), `KeyDown/KeyUp` (:1063/:1074), `CodeDown/CodeUp` (:1089/:1095), **`HoldModifiers(mods)`** (:1051), `Compose` for IME (:1129), `Wheel`, `Scroll`, `Move`, `DropFiles`.
  - Window state: `SetFocused(bool)` (:1114), `SetDark` (:542), `SetSize`, `SetScale`, `SetPreferences`, `SetVibrancy`.
  - Clock: `Now`/`Advance(d)` (:355/:362) give a deterministic clock that drives `c.After`, `c.Now()`, animations and timers.
  - Inspection: `Texts()` (:615), `HasText` (:624), `Find(text) (Rect, bool)` (:604), `Focused` (:1236), `Access*` for the accessibility tree (role, name, value) (`ui/tester_access.go`), `Clipboard()` (:1160), `OpenedURLs()` (:1180), `FramePending()` (:597).
  - **Snapshots:** `Image() *image.RGBA` (:600), and `ui/golden.Match(t, tt, "testdata/x.png", tol...)` (`ui/golden/golden.go:35`).
- App-level tests: `internal/fake` is an in-memory backend, but apps can't install it yet (#1763, open). `plugins/automation` drives a running app from another process.
- **Linux CI requirement:** the Tester uses the real system text engine, so Linux test runners need Pango, cairo, HarfBuzz and fontconfig installed (`internal/text/pango_linux.go:246-262`). Text metrics differ per OS, so keep golden images per OS or use tolerances.
- Known test issues: #1902 (open), where each Tester holds a Pango font map and a thread for the life of the process, which hurts suites with thousands of Testers. #2085 (fixed on main by #2087, not in v0.10.0): the first frame per Tester costs about 50 ms.
- **Structure for Hermes:**
  1. Keep all state in plain structs (`type ChatState`) mutated only by methods. Pure logic, such as `holdTracker`, the shortcut map and the streaming buffer, gets ordinary `go test` with no UI.
  2. View functions read state and call those methods. Test them with `ui.NewTester` plus `HoldModifiers`, `Key` and `Advance`.
  3. Hide network, disk and keychain behind interfaces, injected so tests use fakes, and so the `win.Update` calls run inline in tests (pass a `post func(func())`).
  4. Use golden snapshots sparingly, for loader and theme visuals.

---

## 11. Build, cross-compiling and packaging
- **Pure Go on every desktop target.** CGO is off by default (`docs/distribution.md:30-44`; `docs/architecture.md:25-31`), so macOS amd64/arm64/universal, Windows amd64/arm64 and Linux amd64/arm64 all **cross-compile from Linux with plain `GOOS=… GOARCH=… go build`**. No zig cc, osxcross or mingw-w64 is needed. If we add a cgo dependency, we would need `CGO_ENABLED=1` and a cross `CC` (`docs/distribution.md:40-44`). Avoid that; for example, prefer pure-Go SQLite or MyGo's `plugins/sqlite`, which loads a prebuilt library via `ffi`.
- **`mygo` CLI** (`cmd/mygo`, run via `go tool mygo` with a `tool` line in `go.mod`). Native-UI projects use `mygo.json` and need no Bun or npm (`docs/configuration.md:36-40`). Commands: `mygo init -template native`, `mygo dev` (hot restart that keeps state, `mygo.KeepState`), `mygo build -platform darwin/universal,windows/amd64,windows/arm64,linux/amd64,linux/arm64` (`docs/cli.md:155-182`), `mygo doctor`, `mygo vet`.
  - **macOS:** produces a `.app` with `.icns` generated from `resources/icon.png` (1024²) and a `.dmg`. Developer ID signing, hardened runtime, notarization and stapling **need macOS**: on Linux, `cmd/mygo/bundle.go:402-406` warns "code signing needs macOS" and `cmd/mygo/build.go:353-354` skips the DMG. ⚠️ UNVERIFIED: whether an unsigned bundle built on Linux launches on Apple Silicon. Go's linker ad-hoc-signs darwin/arm64 binaries, but the bundle itself is unsealed. Use a macOS runner for release builds anyway. Issue #1796 (open) covers updates for Linux-built macOS apps.
  - **Windows:** `.exe` with an icon resource from the PNG, and an **NSIS installer** (NSIS must be installed on Linux, e.g. `apt install nsis`). Signing uses `signtool` on Windows or **`osslsigncode`** elsewhere; `windows.signCommand` covers Azure Trusted Signing and similar (`docs/distribution.md:544-570`).
  - **Linux:** executable, `.desktop` file, PNG icon, `.tar.gz` with `install.sh`, **.deb, .rpm, and a PKGBUILD** (`docs/distribution.md:46-52, 572-680`). **No AppImage, Flatpak or Snap.**
  - Auto-updates: signed manifests and delta updates (`docs/updates.md`), uploaded with `-upload` to GitHub Releases or S3.
- **CI example** (`docs/github-actions.md:10-75`): a matrix with macOS on `macos-latest` (`darwin/arm64,darwin/amd64`), Windows on **`ubuntu-latest`** (`windows/amd64,windows/arm64`, with `apt-get install nsis`), and Linux on `ubuntu-latest`. Each runs `go tool mygo build -platform "…" -upload`, using `actions/setup-go` with `go-version-file: go.mod`. Signing sections are at lines 120-262.

---

## 12. Performance
- **Cold start:** the comparison benchmark (`benchmarks/results.json`, 2026-11-05, MyGo **0.7.4**, Apple M5, macOS) measured about **173 ms** from process start to first frame, against GPUI 157 ms and egui 163 ms. There are no numbers for Windows or Linux ⚠️ UNVERIFIED. Linux loads no WebKit for native-only apps, which saves about 20 ms (`docs/architecture.md:365-375`).
- **Binary size:** about 9.7 MB for the counter app via `mygo build` (results.json, 0.7.4). The architecture doc gives 8.9 MB for `counter-native` (`docs/architecture.md:7022-7031`). Expect 15–25 MB for Hermes with markdown, editor tokenizers and netimage ⚠️ UNVERIFIED.
- **Memory and idle:** idle memory is about 34 MB on macOS (results.json) and 65–67 MB on Linux at 4K/2× (`docs/architecture.md:10-20`). Idle CPU is about 0.085 ms/s, effectively zero.
- **Frame cost** (`docs/ui/performance.md:29-70`): every frame that rebuilds runs the whole view, so the cost scales with the number of elements built, not with what changed. Examples: 1,000 labels take 397 µs; 3,000 rows in a `Scroll` take 11 ms per change and 17 ms per keystroke (typing builds twice). **Keep transcripts and sidebars in `List`** (0.2 ms), read `Hovered` in `Draw`, and animate via `Painter`/`AnimatePose`. A retained-node engine is in progress (#1599).
- **Text shaping:** done by the OS engines through purego calls, with layouts cached per paragraph. Known cliff: **O(n²) wrapping of a single very long paragraph on macOS** (`CTTypesetterSuggestLineBreak`). A 50 KB paragraph costs about 55–72 ms per frame and edit, and 1 MiB takes 14 s (issue #2128, open). Mitigation for chat: split huge pasted or unbroken text into chunks before display. A 50 KB Markdown reply with normal paragraphs takes 1.3 ms per frame (`docs/ui/markdown.md:464-480`).

---

## Gaps & workarounds

| Gap | Impact on Hermes Desktop | Workaround | Requires webview fallback? |
|---|---|---|---|
| #1471: a `List` row is the unit of virtualisation, so a long Markdown reply is built whole, and a selection across rows is lost when they scroll out | Long assistant replies cost memory and frame time. Selecting text across several messages breaks when scrolling. | Split long replies into several list rows (one per top-level block or group of blocks via `parser.Parse` + `Options.Block`). Make Selectable per message, not across the list. Track #1471/#1472. | n |
| #2128: O(n²) line breaking of one huge paragraph on macOS | Pasted logs or minified text in one paragraph can freeze the UI | Chunk paragraphs longer than ~2k runes into pseudo-paragraphs, or show them in a code block with `WrapCode=false` and horizontal scroll | n |
| No vibrancy on Linux; Windows material means no menu bar | The "translucent sidebar" only exists on macOS and Win11 22H2+ | Gate on `c.Vibrancy()` and fall back to an opaque `Theme.Surface`. On Windows, accept Alt/F10 popup menus, or skip Mica. | n |
| Menu-accelerator keys don't reach the view | The Cmd-hold badge logic can't see Cmd+N if a menu item owns it | Call `hold.cancel()` in menu `Click` handlers, or implement those shortcuts as `c.Shortcut` and use the menu for display only | n |
| Whole-view rebuild per frame (#1599 in progress) | A big non-virtualised view (settings, a long sidebar) gets janky while typing | Use `List` for every long collection, `Draw`-time hover, paint-only animations; keep the view tree small | n |
| `RadialGradient` is circle-only (#1443) | Elliptical glows aren't possible | Circles for orbs; `Scale` a wrapper, or write a `ui/shader` effect | n |
| macOS notifications need a packaged/signed app; Windows uses legacy balloons | Notifications don't fire in `go run` dev on macOS; Windows looks dated | Test from `mygo dev` or a built `.app`. Accept balloons, or use a Windows toast lib later (⚠️ would add COM work) | n |
| Secrets on Linux need libsecret and a Secret Service | Token storage fails on minimal or headless Linux (`ErrUnsupported`) | Detect `errors.Is(err, errors.ErrUnsupported)` and fall back to an encrypted file with a passphrase, or env-var tokens | n |
| No AppImage, Flatpak or Snap | Distro-agnostic Linux distribution | Ship deb, rpm, PKGBUILD and tar.gz; wrap the tarball with appimagetool in CI ourselves | n |
| macOS signing, notarization and DMG need macOS | We can't do a full release from Linux | macOS runner in CI (as in the docs' workflow); Linux builds the Windows and Linux artifacts | n |
| Apps can't install the fake backend in their own tests (#1763) | We can't unit-test code that calls `mygo.Dialog`/`Notifications` directly | Wrap those in our own interfaces and inject fakes | n |
| Tester resource growth (#1902 open, #2085 fixed on main only) | Large UI test suites get slow and leak threads on Linux | Fewer, larger Tester tests; run `-p` with limits; bump to v0.10.1 | n |
| `ui.Scroll` is not virtual; no masonry (#2028) | Only matters for galleries | Use `List`/`GridView` | n |
| Fast-moving 0.x API (breaking migrations already happened) | Upgrades can break builds | Pin exactly (v0.10.0); upgrade deliberately with `mygo migrate-ui` / `mygo vet` | n |

None of these needs the webview. The features where a web page would be clearly better (CSS-grade rich documents, complex tables in replies) are already covered acceptably by `ui/markdown`.

---

## Open risks
1. **Project maturity and velocity.** The repo is 2 months old, has about 25 commits a day, 188 open issues, and much of the code is AI-assisted (comments and PRs are signed "Generated by Claude Code"). APIs and behaviour change weekly. Pinning is mandatory, and every bump needs a test pass.
2. **Bus factor.** It is essentially one maintainer (egoist) with a surge of contributors. There is no stability promise before 1.0.
3. **Platform parity.** Most benchmarks and fixes are macOS-first. Windows (D3D11, IME, DPI) and Linux (Wayland vs X11, GNOME vs KDE title bars, GL drivers, fallback to the CPU raster in VMs) need early hands-on testing on real machines. Recent fixes for KDE and XWayland are on main only.
4. **Immediate-mode cost model.** Chat transcripts with many long messages are exactly the case #1471 and #1599 describe. We need prototype profiling (`MYGO_FRAME_STATS`, the inspector) with realistic transcripts of 200+ messages with code blocks early.
5. **Go 1.27.3 requirement.** CI and dev machines need a very recent Go (`actions/setup-go` reads it from go.mod).
6. **Unverified items:** stacking multiple `Shadow`s; `Clip()` following the radius; launching an unsigned Linux-built `.app` on Apple Silicon; accelerator plus `Shortcut` double-firing; Windows/Linux cold-start numbers; the pseudo-version string.
7. **Linux runtime dependencies** (GTK 3, Pango, libepoxy, optional libsecret and AppIndicator) must be declared in the deb/rpm. MyGo's packages depend on GTK 3 and WebKitGTK by default. Set `"webview": false` in the config to leave WebKitGTK out of the deb, rpm and PKGBUILD, `install.sh` and `mygo doctor` (`docs/distribution.md:600-603`). Add libsecret and libepoxy via `linux.depends` if they are not already pulled in transitively.

---

## Proof of concept (built and run since: see above)
Location: a scratch directory (not in the repo) (`main.go`, `main_test.go`, `go.mod`).
- It opens a window with a purple rounded card (`Radius`, `Shadow`, `RadialGradient` highlight), title and status text, and a breathing orb drawn paint-only (`Draw` + `p.Now()` + `p.AnimationFrameRate(30)`). Cmd/Ctrl+K toggles a search input, Esc closes it, Cmd/Ctrl+1–9 selects. Holding Cmd/Ctrl alone for 300 ms shows number badges (`holdTracker`, driven by `c.Modifiers()`, `c.KeysDown()`, `c.After()` and `c.WindowActive()`). A goroutine posts state back via `win.Update`.
- Tests: a pure `holdTracker` test, plus a headless `ui.NewTester` test using `HoldModifiers(ui.Cmd)`, `Advance(…)`, `SetFocused(false)` and `Key(ui.Cmd, ui.KeyK)`.
- **Build status:** built and run (Go 1.27.4, Xvfb + llvmpipe); its patterns (holdTracker, paint-only animation, Window.Update) live on in internal/views.

---

## Learned while building (v0.10.0)

- **`TextArea` has a default `MinHeight(Space(20))`** (80 px): `Lines(1, 8)` alone still gives three lines' height; add `MinHeight(0)` for a one-line composer.
- **`Element.Selected(true)` styles the element** (accent fill, `AccentText` text). For custom controls that only need to report selection, use `AccessSelected`.
- **A hidden `List` builds no rows**, so hiding a list does not keep its rows' text layouts; the shared text layout cache (8 MB) does that instead. Keep one `ListState` per chat so switching back neither re-measures nor loses the scroll position.
- **`ui.Icon` tints the whole SVG with the text colour**, overriding its own fills; colour icons with `TextColor`.
- **`ui.Tester.Frame()` runs frames until the view settles**: with paint-only animations running it times several frames. Benchmarks set `Preferences{ReduceMotion: true}`.
- **Windows materials:** `Window.SetVibrancy` changes the material only of a window created with one, and such a window has no menu bar (Alt/F10 open the menus). macOS changes vibrancy at any time.
- **The Markdown theme has no padding or radius for inline code spans**, only a background.
- **`ListState.HoldPlace()`** keeps a following list where it is for a frame, so a fold opening near the end does not scroll away.
- **`LinearGradient{Start: x}` without `End` paints nothing visible:** only both zero mean 0 and 1, so `End` 0 puts `To` everywhere past `Start`. Give both, or `Stops`.
- **An `Absolute` overlay over a `List` needs `ZIndex(1)`** to draw above the rows, and `PassThrough()` so it does not take their clicks.
- **`mygo build` (v0.10.0) writes the install scripts:** `install.sh` (macOS and Linux, checking the archive's Ed25519 signature), `install.ps1` (Windows, checking the installer against `SHA256SUMS`) and a `SHA256SUMS` per build. Builds of several platforms in separate jobs each write their own `SHA256SUMS`: join them before publishing, as `release.yml` does. The Windows installer is `<slug>-setup-<version>-<arch>.exe`, and `install.ps1` finds it by that name in `SHA256SUMS`, so release files keep MyGo's names.
- **A relaunch on macOS goes through `/usr/bin/open`**, which keeps the arguments but not the environment: whatever a relaunched app must still know (the update test's `--home` and `--debug`) goes in arguments.
- **`App.SetName` in a packaged build replaces mygo.json's name**, which the single-instance lock is keyed on: set it only in unpackaged builds (`App.Version() == ""`).
- **`Network.OnChanged` fires on changes of online/metered only**, not on every change of interface; `Power.OnResume` fires after sleep. Both run on the main thread.
