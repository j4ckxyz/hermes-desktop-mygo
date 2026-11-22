package bench

import (
	"os"
	"testing"

	"hermes-desktop/internal/views"
)

func TestChatSwitchBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("benchmark")
	}
	s, err := ChatSwitch(100)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("chat switch: %s", s)
}

func TestStreamingBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("benchmark")
	}
	s, err := Streaming(200)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("streaming: %s", s)
}

func TestShotSettings(t *testing.T) {
	out := os.Getenv("HERMES_SHOT")
	if out == "" {
		t.Skip("set HERMES_SHOT=path.png")
	}
	if err := Shot(views.ScreenSettings, 1440, 1800, out); err != nil {
		t.Fatal(err)
	}
}
