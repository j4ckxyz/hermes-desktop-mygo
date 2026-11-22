package views

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/cache"
	"hermes-desktop/internal/core"
	"hermes-desktop/internal/hermes/hermestest"
	"hermes-desktop/internal/secrets"
	"hermes-desktop/internal/settings"
	"hermes-desktop/internal/theme"
)

func TestMain(m *testing.M) {
	theme.RegisterFonts()
	m.Run()
}

// ---- pure state ----

func TestHoldTracker(t *testing.T) {
	var h holdTracker
	t0 := time.Unix(0, 0)
	if show, wait := h.update(t0, ui.Cmd, 0); show || wait != holdDelay {
		t.Fatalf("armed: %v %v", show, wait)
	}
	if show, _ := h.update(t0.Add(299*time.Millisecond), ui.Cmd, 0); show {
		t.Fatal("too early")
	}
	if show, _ := h.update(t0.Add(300*time.Millisecond), ui.Cmd, 0); !show {
		t.Fatal("badges after 300 ms")
	}
	h = holdTracker{}
	h.update(t0, ui.Cmd, 0)
	h.update(t0.Add(50*time.Millisecond), ui.Cmd, 1) // Cmd+K
	if show, _ := h.update(t0.Add(500*time.Millisecond), ui.Cmd, 0); show {
		t.Fatal("a chord spoils the hold until release")
	}
	h.update(t0.Add(600*time.Millisecond), 0, 0) // released
	h.update(t0.Add(700*time.Millisecond), ui.Cmd, 0)
	if show, _ := h.update(t0.Add(1001*time.Millisecond), ui.Cmd, 0); !show {
		t.Fatal("a new hold works after release")
	}
	h = holdTracker{}
	h.update(t0, ui.Cmd|ui.Shift, 0)
	if show, _ := h.update(t0.Add(time.Second), ui.Cmd|ui.Shift, 0); show {
		t.Fatal("Cmd+Shift is not a hold")
	}
	h = holdTracker{}
	h.update(t0, ui.Cmd, 0)
	h.cancel() // a menu accelerator fired
	if show, _ := h.update(t0.Add(time.Second), ui.Cmd, 0); show {
		t.Fatal("cancel spoils")
	}
}

func TestSplitReply(t *testing.T) {
	md := "Para one.\n\n```go\nfunc x() {\n\n}\n```\n\nhttps://example.com/cron\n\n" + strings.Repeat("word ", 700)
	ch := splitReply(md)
	if len(ch) != 3 {
		t.Fatalf("chunks %d: %q", len(ch), ch)
	}
	if !strings.Contains(ch[0], "func x() {\n\n}") {
		t.Fatal("a blank line inside a code fence must not split it")
	}
	if ch[1] != "https://example.com/cron" || !isBareURL(ch[1]) {
		t.Fatalf("bare URL gets its own chunk: %q", ch[1])
	}
	if isBareURL("see https://x.y") || isBareURL("ftp://x") {
		t.Fatal("not bare URLs")
	}
}

func TestRelTimeAndClock(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{30 * time.Second: "now", 3 * time.Minute: "3m", 7 * time.Hour: "7h", 4 * 24 * time.Hour: "4d", 15 * 24 * time.Hour: "2w"} {
		if got := relTime(now, now.Add(-d)); got != want {
			t.Errorf("relTime(%v) = %q, want %q", d, got, want)
		}
	}
	if clock(42*time.Second) != "0:42" || clock(5*time.Minute+46*time.Second) != "5:46" {
		t.Fatal("clock")
	}
	if initials("Jack G") != "JG" || initials("helix") != "HE" {
		t.Fatal("initials")
	}
}

// ---- headless views against the fake instance ----

type harness struct {
	t     *testing.T
	srv   *hermestest.Server
	app   *core.App
	u     *UI
	tt    *ui.Tester
	queue chan func()
	saved []settings.Settings
	fake  *fakeNative
}

type fakeNative struct {
	icons    []string
	material []bool
	confirms []string
	saved    map[string]string
	answer   bool
}

func (f *fakeNative) Confirm(m, d, a string) bool {
	f.confirms = append(f.confirms, m)
	return f.answer
}
func (f *fakeNative) SaveText(name, content string) error {
	if f.saved == nil {
		f.saved = map[string]string{}
	}
	f.saved[name] = content
	return nil
}
func (f *fakeNative) Platform() string         { return "linux" }
func (f *fakeNative) SetAppIcon(colour string) { f.icons = append(f.icons, colour) }
func (f *fakeNative) SetSidebarMaterial(on bool) string {
	f.material = append(f.material, on)
	return ""
}

func newHarness(t *testing.T, opt hermestest.Options, connected bool) *harness {
	t.Helper()
	if opt.Token == "" {
		opt.Token = "tok"
	}
	h := &harness{t: t, srv: hermestest.New(opt), queue: make(chan func(), 4096), fake: &fakeNative{answer: true}}
	t.Cleanup(h.srv.Close)
	c, err := cache.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	sec := secrets.NewWithBackend(&secrets.MemoryBackend{})
	inst := settings.Instance{ID: "i1", Name: "Main", URL: h.srv.URL, Auth: settings.AuthToken}
	var list settings.Instances
	if connected {
		sec.Set("i1", secrets.Credentials{Token: opt.Token})
		list.List = []settings.Instance{inst}
	}
	h.app = core.New(core.Deps{Post: func(fn func()) { h.queue <- fn }, Cache: c, Secrets: sec}, list)
	s := settings.Default()
	s.DarkCanvas, s.Appearance = theme.Midnight, theme.Dark
	if connected {
		h.app.Open(inst)
	}
	h.u = New(h.app, s, h.fake)
	h.u.SettingsChanged = func(s settings.Settings) { h.saved = append(h.saved, s) }
	h.tt = ui.NewTester(h.u.View, 1440, 900)
	t.Cleanup(func() {
		if h.app.Conn != nil {
			h.app.Conn.Close()
		}
	})
	return h
}

// settle runs posted work and frames until cond holds.
func (h *harness) settle(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for drained := false; !drained; {
			select {
			case fn := <-h.queue:
				fn()
			default:
				drained = true
			}
		}
		h.tt.Frame()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s; texts: %q", what, h.tt.Texts())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harness) synced() {
	h.settle("sync", func() bool { c := h.app.Conn; return c != nil && c.Synced && c.Status == core.Connected })
}

func TestSidebarShowsAgentsAndSessions(t *testing.T) {
	h := newHarness(t, hermestest.Options{Canonical: true}, true)
	h.synced()
	for _, s := range []string{"Hermes", "sidequest", "helix", "Shorten morning news and surf links", "234 more sessions", "3 more sessions", "Jack G"} {
		if !h.tt.HasText(s) {
			t.Errorf("sidebar is missing %q", s)
		}
	}
	if h.tt.HasText("Older chat 1") {
		t.Error("older sessions stay behind \"234 more sessions\"")
	}
	if err := h.tt.Click("234 more sessions"); err != nil {
		t.Fatal(err)
	}
	h.settle("expanded", func() bool { return h.tt.HasText("Show fewer") || h.u.expanded["default"] })
}

func TestCmdHoldShowsBadgesAndJumps(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	h.tt.HoldModifiers(ui.Cmd)
	h.tt.Advance(100 * time.Millisecond)
	if h.tt.HasText("Shortcut 1") {
		t.Fatal("badges must wait 300 ms")
	}
	h.tt.Advance(250 * time.Millisecond)
	if !h.tt.HasText("Shortcut 1") {
		t.Fatalf("no badge after 350 ms: %v", h.tt.Texts())
	}
	if !h.tt.HasText("Shortcut 5") || h.tt.HasText("Shortcut 10") {
		t.Fatal("every visible session gets a number, up to 9")
	}
	// Cmd+3 opens the third session shown.
	targets := append([]string(nil), h.u.side.targets...)
	h.tt.Key(ui.Cmd, ui.Key3)
	if h.u.Selected != targets[2] {
		t.Fatalf("Cmd+3 selected %q, want %q", h.u.Selected, targets[2])
	}
	h.tt.Frame()
	if h.tt.HasText("Shortcut 1") {
		t.Fatal("a chord hides the badges")
	}
	h.tt.HoldModifiers(0)
	h.tt.SetWindowActive(false)
	h.tt.HoldModifiers(ui.Cmd)
	h.tt.Advance(time.Second)
	if h.tt.HasText("Shortcut 1") {
		t.Fatal("no badges in a window without focus")
	}
}

func TestShortcuts(t *testing.T) {
	h := newHarness(t, hermestest.Options{Canonical: true, Reply: "design_turn.jsonl", Delay: time.Millisecond}, true)
	h.synced()
	// Cmd+, opens settings; Esc goes back.
	h.tt.Key(ui.Cmd, ui.KeyComma)
	h.tt.Frame()
	if h.u.Screen != ScreenSettings || !h.tt.HasText("Look & Feel") {
		t.Fatal("Cmd+, opens settings")
	}
	h.tt.Key(0, ui.KeyEscape)
	if h.u.Screen != ScreenMain {
		t.Fatal("Esc leaves settings")
	}
	// Cmd+K opens search; typing filters; Enter opens.
	h.tt.Key(ui.Cmd, ui.KeyK)
	h.tt.Frame()
	if !h.u.search.open {
		t.Fatal("Cmd+K opens search")
	}
	h.tt.Type("Hey #2")
	h.tt.Frame()
	h.tt.Key(0, ui.KeyEnter)
	if h.u.Selected != "20261122_060000_0a0b0c" || h.u.search.open {
		t.Fatalf("Enter opens the match: %q", h.u.Selected)
	}
	h.settle("chat", func() bool { return h.tt.HasText("Hey! What can I help with?") })
	// Cmd+N starts a new chat with the agent of the open one.
	h.tt.Key(ui.Cmd, ui.KeyN)
	h.tt.Frame()
	if h.u.Selected != "" || !h.tt.HasText("New chat with Hermes") {
		t.Fatal("Cmd+N")
	}
	// Send from the new chat: it is created, then streams.
	h.tt.Type("find my notes")
	h.tt.Key(0, ui.KeyEnter)
	h.settle("created", func() bool { return h.u.Selected != "" })
	id := h.u.Selected
	h.settle("running", func() bool {
		ch := h.app.Conn.Chats[id]
		return ch != nil && ch.Running() && len(ch.Turns) == 1 && len(ch.Turns[0].Tools) == 3
	})
	if !h.tt.HasText("Running command") {
		t.Fatalf("live tool pills: %q", h.tt.Texts())
	}
	// Esc stops the running agent.
	h.tt.Key(0, ui.KeyEscape)
	h.settle("stopped", func() bool { return !h.app.Conn.Chats[id].Running() })
	if h.srv.CallCount("session.interrupt") != 1 {
		t.Fatal("Esc must interrupt")
	}
}

func TestSessionContextMenu(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	if err := h.tt.RightClick("Hey #2"); err != nil {
		t.Fatal(err)
	}
	items := strings.Join(h.tt.Menu(), "|")
	for _, want := range []string{"Rename…", "Pin", "Copy", "Copy session ID", "Export as Markdown…", "Delete…"} {
		if !strings.Contains(items, want) {
			t.Errorf("menu lacks %q: %s", want, items)
		}
	}
	if err := h.tt.ChooseMenuItem("Copy session ID"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.tt.Clipboard() != "20261122_060000_0a0b0c" {
		t.Fatalf("clipboard %q", h.tt.Clipboard())
	}
	h.tt.RightClick("Hey #2")
	h.tt.ChooseMenuItem("Delete…")
	h.tt.Frame()
	if len(h.fake.confirms) != 1 {
		t.Fatal("delete asks first, with a native dialog")
	}
	h.settle("deleted", func() bool { return h.srv.SessionRow("20261122_060000_0a0b0c") == nil })
	if h.tt.HasText("Hey #2") {
		t.Fatal("gone from the sidebar")
	}
}

func TestThemeChangesLiveAndPersists(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	h.u.Screen = ScreenSettings
	h.tt.Frame()
	before := h.u.Palette().Bg
	if err := h.tt.Click("Black"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.u.Palette().Bg != theme.CanvasNamed(theme.Black).Bg || h.u.Palette().Bg == before {
		t.Fatal("the canvas changes on the next frame")
	}
	acc, ok := h.tt.Access("Teal accent")
	if !ok {
		t.Fatal("accent swatches are labelled")
	}
	h.tt.ClickAt(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	h.tt.Frame()
	if h.u.Settings.Accent != "Teal" || h.u.Palette().Accent != theme.CanvasAccent("Teal") {
		t.Fatalf("accent %q", h.u.Settings.Accent)
	}
	if n := len(h.saved); n == 0 || h.saved[n-1].Accent != "Teal" || h.saved[n-1].DarkCanvas != theme.Black {
		t.Fatalf("settings persisted: %+v", h.saved)
	}
	// Light mode follows too.
	h.tt.Click("Light")
	h.tt.Frame()
	if h.u.Palette().Dark {
		t.Fatal("light appearance")
	}
}

func TestConnectScreen(t *testing.T) {
	h := newHarness(t, hermestest.Options{Token: "good-token"}, false)
	h.tt.Frame()
	if h.u.Screen != ScreenConnect || !h.tt.HasText("Connect to Hermes") {
		t.Fatal("first launch shows the connect screen")
	}
	h.u.connect.address = h.srv.URL
	h.u.connect.token = "wrong"
	h.tt.Frame()
	h.tt.Click("Connect")
	h.settle("refusal", func() bool { return h.u.connect.err != "" })
	if !strings.Contains(h.u.connect.err, "refused") {
		t.Fatalf("error %q", h.u.connect.err)
	}
	h.u.connect.token = "good-token"
	h.tt.Click("Connect")
	h.settle("connected", func() bool { return h.u.Screen == ScreenMain && h.app.Conn != nil && h.app.Conn.Synced })
	if len(h.app.Instances.List) != 1 || h.app.Instances.List[0].Name != "Main" {
		t.Fatalf("saved %+v", h.app.Instances.List)
	}
	cr, _ := h.app.Secrets.Get(h.app.Instances.List[0].ID)
	if cr.Token != "good-token" || h.u.connect.token != "" {
		t.Fatal("the token goes to the keychain and leaves the form")
	}
}

func TestToolGroupExpands(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	h.u.Select("20261122_080205_fb120f")
	h.settle("transcript", func() bool { return h.tt.HasText("Ran 11 commands and made 7 tool calls") })
	h.u.listFor(h.u.Selected).ScrollTo(0, ui.Start)
	h.tt.Frame()
	if h.tt.HasText("hermes cron show morning-0") {
		t.Fatal("a finished turn's tools start collapsed")
	}
	h.tt.Click("Ran 11 commands and made 7 tool calls")
	h.tt.Frame()
	if !h.tt.HasText("hermes cron show morning-0") {
		t.Fatalf("expanded pills: %q", h.tt.Texts())
	}
}

func TestPromptCards(t *testing.T) {
	h := newHarness(t, hermestest.Options{Reply: "design_turn.jsonl", Delay: time.Millisecond}, true)
	h.synced()
	id := "20261122_060000_0a0b0c"
	h.u.Select(id)
	h.settle("chat", func() bool { ch := h.app.Conn.Chats[id]; return ch != nil && ch.Fetched })
	h.tt.Type("install jq")
	h.tt.Key(0, ui.KeyEnter)
	h.settle("running", func() bool { return h.tt.HasText("Running command") })

	// sudo: a masked field; Enter sends the password, which then leaves memory.
	rid := h.srv.Ask(id, "sudo", map[string]any{"command": "apt install jq"})
	h.settle("sudo card", func() bool { return h.tt.HasText("Hermes needs your sudo password") })
	if !h.tt.HasText("apt install jq") {
		t.Fatal("the command shows")
	}
	h.tt.Type("hunter2")
	if h.tt.HasText("hunter2") {
		t.Fatal("the password must be masked")
	}
	h.tt.Key(0, ui.KeyEnter)
	h.settle("sudo reply", func() bool { return h.srv.Response(rid) != nil })
	if string(h.srv.Response(rid)) != `{"value":"hunter2"}` {
		t.Fatalf("%s", h.srv.Response(rid))
	}
	if len(h.u.forms) != 0 {
		t.Fatal("the form (and the password in it) is dropped once sent")
	}

	// A secret, skipped.
	rid = h.srv.Ask(id, "secret", map[string]any{"env_var": "NOTION_API_KEY", "prompt": "Paste your Notion integration key"})
	h.settle("secret card", func() bool { return h.tt.HasText("Hermes needs NOTION_API_KEY") })
	if err := h.tt.Click("Skip"); err != nil {
		t.Fatal(err)
	}
	h.settle("secret reply", func() bool { return h.srv.Response(rid) != nil })
	if string(h.srv.Response(rid)) != `{"value":""}` {
		t.Fatalf("%s", h.srv.Response(rid))
	}

	// Two questions: a choice picked, a text left blank (skipped).
	rid = h.srv.Ask(id, "clarify", map[string]any{"questions": []any{
		map[string]any{"qid": "q1", "question": "Which Todoist project?", "choices": []string{"Inbox", "School"}},
		map[string]any{"qid": "q2", "question": "Anything to add?"},
	}})
	h.settle("questions", func() bool { return h.tt.HasText("Hermes has 2 questions") })
	if err := h.tt.Click("School"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if err := h.tt.Click("Send answers"); err != nil {
		t.Fatal(err)
	}
	h.settle("answers", func() bool { return h.srv.Response(rid) != nil })
	if got := string(h.srv.Response(rid)); got != `{"answers":{"q1":"School","q2":null},"outcome":"submitted"}` {
		t.Fatalf("%s", got)
	}
}

func TestTranslucentSidebarAppliesLive(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	h.u.Screen = ScreenSettings
	h.tt.SetSize(1440, 2600) // the whole Look & Feel page, no scrolling
	h.tt.Frame()
	sw, ok := h.tt.AccessByRole(ui.RoleSwitch, "Translucent sidebar")
	if !ok {
		t.Fatal("the switch is labelled")
	}
	h.tt.ClickAt(sw.Bounds.X+sw.Bounds.W/2, sw.Bounds.Y+sw.Bounds.H/2)
	h.tt.Frame()
	if !h.u.Settings.TranslucentSidebar || len(h.fake.material) != 1 || !h.fake.material[0] {
		t.Fatalf("setting %v, native calls %v", h.u.Settings.TranslucentSidebar, h.fake.material)
	}
	// With the material showing, the main screen still draws (transparent
	// root, tinted sidebar, opaque chat).
	h.tt.SetVibrancy(true)
	h.u.Screen = ScreenMain
	h.tt.Frame()
	if !h.tt.HasText("Shorten morning news and surf links") {
		t.Fatal("sidebar over the material")
	}
	h.tt.ClickAt(sw.Bounds.X+sw.Bounds.W/2, sw.Bounds.Y+sw.Bounds.H/2) // not on settings: no effect
	h.u.Screen = ScreenSettings
	h.tt.Frame()
	h.tt.ClickAt(sw.Bounds.X+sw.Bounds.W/2, sw.Bounds.Y+sw.Bounds.H/2)
	h.tt.Frame()
	if h.u.Settings.TranslucentSidebar || len(h.fake.material) != 2 || h.fake.material[1] {
		t.Fatalf("off again: %v", h.fake.material)
	}
}

func TestAppIconPicker(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	h.u.Screen = ScreenSettings
	h.tt.SetSize(1440, 2000)
	h.settle("marbles", func() bool { return h.u.marbles != nil })
	sw, ok := h.tt.Access("Teal icon")
	if !ok {
		t.Fatal("icon swatches are labelled")
	}
	h.tt.ClickAt(sw.Bounds.X+sw.Bounds.W/2, sw.Bounds.Y+sw.Bounds.H/2)
	h.tt.Frame()
	if h.u.Settings.AppIcon != "Teal" || len(h.fake.icons) != 1 || h.fake.icons[0] != "Teal" {
		t.Fatalf("setting %q, applied %v", h.u.Settings.AppIcon, h.fake.icons)
	}
}

func TestF2RenamesOpenChat(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	id := "20261122_060000_0a0b0c"
	h.u.Select(id)
	h.tt.Frame()
	h.tt.Click("Hey #2") // focus the sidebar row, not the composer
	h.tt.Key(0, ui.KeyF2)
	h.tt.Frame()
	if h.u.renaming != id {
		t.Fatal("F2 starts renaming the open chat")
	}
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("Greetings")
	h.tt.Key(0, ui.KeyEnter)
	h.settle("renamed", func() bool { r := h.srv.SessionRow(id); return r != nil && r["title"] == "Greetings" })
}
