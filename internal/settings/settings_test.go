package settings

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hermes-desktop/internal/theme"
)

func TestLoadMissingGivesDefaults(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s != Default() {
		t.Fatalf("got %+v", s)
	}
}

func TestRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "settings.json")
	s := Default()
	s.Appearance = theme.Dark
	s.DarkCanvas = theme.Gray
	s.Accent = "Teal"
	s.Loader = "m5"
	s.TextSize = 3
	s.Sounds = false
	if err := Save(p, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != s {
		t.Fatalf("got %+v want %+v", got, s)
	}
	// No temporary files left behind.
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestCorruptAndInvalidValues(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	s, err := Load(bad)
	if err == nil || s != Default() {
		t.Fatalf("corrupt file: err=%v s=%+v", err, s)
	}
	odd := filepath.Join(dir, "odd.json")
	os.WriteFile(odd, []byte(`{"appearance":"Sepia","darkCanvas":"White","accent":"Mauve","loader":"x9","font":"Comic","textSize":42,"sounds":false}`), 0o600)
	s, err = Load(odd)
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if s.Appearance != d.Appearance || s.DarkCanvas != d.DarkCanvas || s.Accent != d.Accent || s.Loader != d.Loader || s.Font != d.Font {
		t.Fatalf("invalid values kept: %+v", s)
	}
	if s.TextSize != theme.MaxTextSize || s.Sounds {
		t.Fatalf("valid values lost: %+v", s)
	}
}

func TestSaverDebouncesAndFlushes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	var errs atomic.Int32
	sv := &Saver{Path: p, Delay: time.Hour, OnError: func(error) { errs.Add(1) }}
	for i := range 5 {
		s := Default()
		s.TextSize = i
		sv.Request(s)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("saved before the delay")
	}
	sv.Flush()
	got, err := Load(p)
	if err != nil || got.TextSize != 4 {
		t.Fatalf("flush wrote %+v, %v", got, err)
	}
	sv.Flush() // nothing pending: returns at once
	if errs.Load() != 0 {
		t.Fatal("unexpected save error")
	}

	sv2 := &Saver{Path: p, Delay: 5 * time.Millisecond}
	s := Default()
	s.Accent = "Green"
	sv2.Request(s)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := Load(p); got.Accent == "Green" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("debounced save never happened")
}

func TestInstances(t *testing.T) {
	p := InstancesPath(filepath.Join(t.TempDir(), "settings.json"))
	is, err := LoadInstances(p)
	if err != nil || len(is.List) != 0 {
		t.Fatal(is, err)
	}
	a := Instance{ID: NewInstanceID(), Name: "Main", URL: "https://hermes-vps.your-tailnet.ts.net", Auth: AuthToken}
	b := Instance{ID: NewInstanceID(), Name: "Laptop", URL: "http://127.0.0.1:9119", Local: true, Auth: AuthPassword, Username: "jack"}
	is.Put(a)
	is.Put(b)
	a.Name = "Main VPS"
	is.Put(a)
	if err := SaveInstances(p, is); err != nil {
		t.Fatal(err)
	}
	got, err := LoadInstances(p)
	if err != nil || len(got.List) != 2 || got.List[0].Name != "Main VPS" || got.List[0].Host() != "hermes-vps.your-tailnet.ts.net" {
		t.Fatalf("%+v %v", got, err)
	}
	if got.List[1].AuthLabel() != "Dashboard login" || got.List[0].AuthLabel() != "API key" {
		t.Fatal("labels")
	}
	got.Remove(a.ID)
	if _, ok := got.Find(a.ID); ok || len(got.List) != 1 {
		t.Fatal("remove")
	}
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "\"password\":") || strings.Contains(string(data), "\"token\":") {
		t.Fatalf("instances.json must hold no credentials: %s", data)
	}
}
