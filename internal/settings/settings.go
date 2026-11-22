// Package settings holds the app's preferences and persists them as JSON
// in the user's config directory. Saving runs on a background goroutine,
// debounced, with an atomic rename, so the UI thread never touches the disk.
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"hermes-desktop/internal/appicon"
	"hermes-desktop/internal/theme"
)

// Loader IDs: five marching grids, five spheres of light, five spheres of mist.
var Loaders = []string{
	"g1", "g2", "g3", "g4", "g5",
	"l1", "l2", "l3", "l4", "l5",
	"m1", "m2", "m3", "m4", "m5",
}

// Settings are the user's preferences. Zero values are filled by Normalize.
type Settings struct {
	Version int `json:"version"`

	// Look & Feel
	Appearance        theme.Appearance `json:"appearance"`
	LightCanvas       string           `json:"lightCanvas"`
	DarkCanvas        string           `json:"darkCanvas"`
	Accent            string           `json:"accent"`
	Loader            string           `json:"loader"`
	Font              string           `json:"font"`
	TextSize          int              `json:"textSize"`
	AnimateCharacters bool             `json:"animateCharacters"`
	Sounds            bool             `json:"sounds"`
	BackgroundArtwork bool             `json:"backgroundArtwork"`
	// AppIcon is the colour of the marble in the app's icon.
	AppIcon            string `json:"appIcon"`
	TranslucentSidebar bool   `json:"translucentSidebar"`

	// LinkPreviews fetches the titles of links in replies.
	LinkPreviews bool `json:"linkPreviews"`

	// ShowBackgroundRuns lists the sessions no one chatted in (cron runs,
	// subagents) beside the conversations, in the sidebar and in search.
	ShowBackgroundRuns bool `json:"showBackgroundRuns,omitempty"`

	// AutoUpdate checks for new versions as the app opens and every few
	// hours, and downloads the ones released while it runs.
	AutoUpdate bool `json:"autoUpdate"`

	// Notifications
	NotifyOnFinish   bool `json:"notifyOnFinish"`
	NotifyOnApproval bool `json:"notifyOnApproval"`

	// PinnedAgents lists the agents pinned to the top of the sidebar, one
	// "instance/profile" per line.
	PinnedAgents string `json:"pinnedAgents,omitempty"`

	// Instances: the one the app opens, by ID.
	ActiveInstance string `json:"activeInstance,omitempty"`
	// Window frame, restored at launch.
	WindowW float64 `json:"windowW,omitempty"`
	WindowH float64 `json:"windowH,omitempty"`
}

const currentVersion = 1

// Default returns the design's defaults.
func Default() Settings {
	return Settings{
		Version:           currentVersion,
		Appearance:        theme.Automatic,
		LightCanvas:       theme.White,
		DarkCanvas:        theme.Midnight,
		Accent:            theme.DefaultAccent,
		Loader:            "g3",
		Font:              theme.FontRounded,
		AnimateCharacters: true,
		Sounds:            true,
		BackgroundArtwork: true,
		NotifyOnFinish:    true,
		NotifyOnApproval:  true,
		LinkPreviews:      true,
		AutoUpdate:        true,
		AppIcon:           "Pink",
	}
}

// Normalize replaces values that are unknown (a hand-edited or older file)
// with the defaults, so the views never see an invalid choice.
func (s *Settings) Normalize() {
	d := Default()
	switch s.Appearance {
	case theme.Automatic, theme.Light, theme.Dark:
	default:
		s.Appearance = d.Appearance
	}
	if !slices.Contains(theme.LightCanvases, s.LightCanvas) {
		s.LightCanvas = d.LightCanvas
	}
	if !slices.Contains(theme.DarkCanvases, s.DarkCanvas) {
		s.DarkCanvas = d.DarkCanvas
	}
	if theme.AccentNamed(s.Accent).Name != s.Accent {
		s.Accent = d.Accent
	}
	if !slices.Contains(Loaders, s.Loader) {
		s.Loader = d.Loader
	}
	if !slices.Contains(theme.Fonts, s.Font) {
		s.Font = d.Font
	}
	s.TextSize = max(0, min(s.TextSize, theme.MaxTextSize))
	if _, ok := appicon.Colours[s.AppIcon]; !ok {
		s.AppIcon = d.AppIcon
	}
	s.Version = currentVersion
}

// Theme returns the part of the settings the palette depends on.
func (s *Settings) Theme() theme.Settings {
	return theme.Settings{
		Appearance:  s.Appearance,
		LightCanvas: s.LightCanvas,
		DarkCanvas:  s.DarkCanvas,
		Accent:      s.Accent,
		Font:        s.Font,
		TextSize:    s.TextSize,
	}
}

// DefaultPath is settings.json in the OS config directory
// (~/.config/Hermes Desktop on Linux, ~/Library/Application Support on
// macOS, %AppData% on Windows).
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Hermes Desktop", "settings.json"), nil
}

// Load reads the settings at path. A missing file gives the defaults; a
// corrupt one gives the defaults and the error, so the app still opens.
func Load(path string) (Settings, error) {
	s := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Default(), err
	}
	s.Normalize()
	return s, nil
}

// Save writes s to path atomically: a temporary file, then a rename.
func Save(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(data, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Saver persists settings in the background. Request takes a copy and
// returns at once; the last copy requested within Delay is written.
type Saver struct {
	Path  string
	Delay time.Duration
	// OnError is called (from the saver's goroutine) when a write fails.
	OnError func(error)

	mu      sync.Mutex
	pending *Settings
	timer   *time.Timer
	idle    sync.Cond
	writing bool
}

// Request schedules s to be saved.
func (sv *Saver) Request(s Settings) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	sv.pending = &s
	if sv.timer == nil {
		d := sv.Delay
		if d == 0 {
			d = 250 * time.Millisecond
		}
		sv.timer = time.AfterFunc(d, sv.flush)
	}
}

func (sv *Saver) flush() {
	sv.mu.Lock()
	s := sv.pending
	sv.pending, sv.timer = nil, nil
	sv.writing = true
	sv.mu.Unlock()
	if s != nil {
		if err := Save(sv.Path, *s); err != nil && sv.OnError != nil {
			sv.OnError(err)
		}
	}
	sv.mu.Lock()
	sv.writing = false
	if sv.idle.L != nil {
		sv.idle.Broadcast()
	}
	sv.mu.Unlock()
}

// Flush writes what is pending now and waits for it, as the app quits.
func (sv *Saver) Flush() {
	sv.mu.Lock()
	if sv.idle.L == nil {
		sv.idle.L = &sv.mu
	}
	if sv.timer != nil && sv.timer.Stop() {
		sv.timer = nil
		sv.mu.Unlock()
		sv.flush()
		return
	}
	for sv.writing || sv.timer != nil {
		sv.idle.Wait()
	}
	sv.mu.Unlock()
}

// AgentPinned reports whether an agent is pinned.
func (s *Settings) AgentPinned(instance, profile string) bool {
	key := instance + "/" + profile
	for _, l := range strings.Split(s.PinnedAgents, "\n") {
		if l == key {
			return true
		}
	}
	return false
}

// SetAgentPinned pins or unpins an agent.
func (s *Settings) SetAgentPinned(instance, profile string, on bool) {
	key := instance + "/" + profile
	var out []string
	for _, l := range strings.Split(s.PinnedAgents, "\n") {
		if l != "" && l != key {
			out = append(out, l)
		}
	}
	if on {
		out = append(out, key)
	}
	s.PinnedAgents = strings.Join(out, "\n")
}
